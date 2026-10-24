package taskcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"math"
	"reflect"
	"sort"
	"strings"
	"time"
)

func (s *Store) ClaimTask(ctx context.Context, id string, version int64, workerID string, lease time.Duration) (Execution, error) {
	if version < 1 || workerID == "" || lease <= 0 {
		return Execution{}, ErrInvalid
	}
	token := uuid()
	// Materialize the primary-key row lock before evaluating SET's database clock.
	// A direct UPDATE can compute lease_until before an unchanged row-lock wait.
	t, err := scanTask(s.pool.QueryRow(ctx, `WITH locked AS MATERIALIZED (
	 SELECT id AS locked_task_id FROM task_report.tasks WHERE id=$1 FOR UPDATE
	) UPDATE task_report.tasks SET state='running',attempt=attempt+1,progress=1,
 worker_id=$3,execution_token=$4,lease_until=clock_timestamp()+$5::bigint*interval '1 millisecond',last_error='',updated_at=clock_timestamp()
 FROM locked WHERE id=locked.locked_task_id AND dispatch_version=$2 AND dispatch_version>0 AND state IN ('pending','failed')
 AND next_run_at<=clock_timestamp() AND attempt<max_attempts
 AND (kind='export' OR (kind='import' AND (result @> '{"confirmed":true}'::jsonb OR input->>'phase'='preflight'))) RETURNING `+columns, id, version, workerID, token, lease.Milliseconds()))
	if errors.Is(err, ErrNotFound) {
		return Execution{}, ErrConflict
	}
	if err != nil {
		return Execution{}, err
	}
	return Execution{Task: t, WorkerID: workerID, Token: token, DispatchVersion: version, LeaseUntil: *t.LeaseUntil}, nil
}

const executionPredicate = `id=$1 AND state='running' AND execution_token=$2::uuid AND worker_id=$3 AND dispatch_version=$4 AND lease_until>clock_timestamp()`

func execArgs(e Execution) []any { return []any{e.ID, e.Token, e.WorkerID, e.DispatchVersion} }
func (s *Store) RenewLease(ctx context.Context, e Execution, lease time.Duration) error {
	if lease <= 0 {
		return ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = lockExecution(ctx, tx, e); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE task_report.tasks SET lease_until=clock_timestamp()+$5::bigint*interval '1 millisecond',updated_at=clock_timestamp() WHERE `+executionPredicate, append(execArgs(e), lease.Milliseconds())...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return tx.Commit(ctx)
}
func lockExecution(ctx context.Context, tx pgx.Tx, e Execution) (Task, error) {
	// Lock first, then evaluate the lease using fresh database time after any wait.
	t, err := scanTask(tx.QueryRow(ctx, baseSelect+` WHERE id=$1 FOR UPDATE`, e.ID))
	if err != nil {
		return Task{}, err
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_report.tasks WHERE `+executionPredicate+`)`, execArgs(e)...).Scan(&valid)
	if err != nil {
		return Task{}, err
	}
	if !valid {
		return Task{}, ErrLeaseLost
	}
	return t, nil
}
func guardExecution(ctx context.Context, tx pgx.Tx, e Execution, progress int) error {
	tag, err := tx.Exec(ctx, `UPDATE task_report.tasks SET progress=GREATEST(progress,$5),updated_at=clock_timestamp() WHERE `+executionPredicate, append(execArgs(e), progress)...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}
func inbox(ctx context.Context, tx pgx.Tx, consumer, eventID, outcome string) error {
	_, err := tx.Exec(ctx, `INSERT INTO task_report.inbox(consumer,event_id,outcome) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, consumer, eventID, outcome)
	return err
}
func enqueueTask(ctx context.Context, tx pgx.Tx, t Task) error {
	eventID := uuid()
	data, err := json.Marshal(map[string]any{"schema_version": 2, "event_id": eventID, "task_id": t.ID, "kind": t.Kind, "dispatch_version": t.DispatchVersion})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO task_report.outbox(event_id,topic,aggregate_id,dispatch_version,payload,available_at) VALUES($1,'task.requested',$2,$3,$4,$5) ON CONFLICT(aggregate_id,dispatch_version) WHERE topic='task.requested' DO NOTHING`, eventID, t.ID, t.DispatchVersion, data, t.NextRunAt)
	return err
}
func deadTask(ctx context.Context, tx pgx.Tx, t Task, reason string) error {
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO task_report.dead_letters(task_id,payload,reason) VALUES($1,$2,$3) ON CONFLICT(task_id) DO UPDATE SET payload=EXCLUDED.payload,reason=EXCLUDED.reason,failed_at=clock_timestamp()`, t.ID, data, reason); err != nil {
		return err
	}
	eventID := uuid()
	data, err = json.Marshal(map[string]any{"schema_version": 2, "event_id": eventID, "task_id": t.ID, "kind": t.Kind, "dispatch_version": t.DispatchVersion, "reason": reason})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO task_report.outbox(event_id,topic,aggregate_id,payload) VALUES($1,'task.dead',$2,$3)`, eventID, t.ID, data)
	return err
}
func (s *Store) Complete(ctx context.Context, consumer, eventID string, e Execution, result map[string]any) error {
	state := "succeeded"
	if isPreflight(e.Task) {
		state = "awaiting_confirmation"
	}
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = lockExecution(ctx, tx, e); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE task_report.tasks SET state=$6,progress=100,result=result||$5::jsonb,last_error='',worker_id=NULL,execution_token=NULL,lease_until=NULL,updated_at=clock_timestamp() WHERE `+executionPredicate, append(execArgs(e), data, state)...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	if err = inbox(ctx, tx, consumer, eventID, state); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func finishFailure(ctx context.Context, tx pgx.Tx, t Task, reason string, execution *Execution) (Task, error) {
	predicate := "id=$1"
	args := []any{t.ID, reason}
	if execution != nil {
		predicate += " AND state='running' AND execution_token=$3::uuid AND worker_id=$4 AND dispatch_version=$5 AND lease_until>clock_timestamp()"
		args = append(args, execution.Token, execution.WorkerID, execution.DispatchVersion)
	}
	t, err := scanTask(tx.QueryRow(ctx, `UPDATE task_report.tasks SET state=CASE WHEN attempt>=max_attempts THEN 'dead_letter' ELSE 'failed' END,
 dispatch_version=dispatch_version+CASE WHEN attempt<max_attempts THEN 1 ELSE 0 END,
 next_run_at=CASE WHEN attempt<max_attempts THEN clock_timestamp()+power(2,LEAST(attempt,8))*interval '1 second' ELSE NULL END,
 last_error=$2,worker_id=NULL,execution_token=NULL,lease_until=NULL,updated_at=clock_timestamp() WHERE `+predicate+` RETURNING `+columns, args...))
	if errors.Is(err, ErrNotFound) && execution != nil {
		return Task{}, ErrLeaseLost
	}
	if err != nil {
		return Task{}, err
	}
	if t.State == DeadLetter {
		err = deadTask(ctx, tx, t, reason)
	} else {
		err = enqueueTask(ctx, tx, t)
	}
	return t, err
}
func (s *Store) RetryOrDead(ctx context.Context, consumer, eventID string, e Execution, reason string) (Task, error) {
	reason = quarantineText(reason, 4096)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback(ctx)
	t, err := lockExecution(ctx, tx, e)
	if err != nil {
		return Task{}, err
	}
	if err = guardExecution(ctx, tx, e, 0); err != nil {
		return Task{}, err
	}
	t, err = finishFailure(ctx, tx, t, reason, &e)
	if err != nil {
		return Task{}, err
	}
	if err = inbox(ctx, tx, consumer, eventID, string(t.State)); err != nil {
		return Task{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Task{}, err
	}
	return t, nil
}
func (s *Store) RecoverExpired(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, baseSelect+` WHERE dispatch_version>0 AND state='running' AND lease_until<=clock_timestamp() ORDER BY lease_until,id FOR UPDATE SKIP LOCKED LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	var tasks []Task
	for rows.Next() {
		t, e := scanTask(rows)
		if e != nil {
			rows.Close()
			return 0, e
		}
		tasks = append(tasks, t)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	for _, t := range tasks {
		if _, err = finishFailure(ctx, tx, t, "execution lease expired", nil); err != nil {
			return 0, err
		}
	}
	return len(tasks), tx.Commit(ctx)
}
func (s *Store) RecoverExpiredTask(ctx context.Context, id string, version int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	t, err := scanTask(tx.QueryRow(ctx, baseSelect+` WHERE id=$1 AND dispatch_version=$2 AND dispatch_version>0 AND state='running' AND lease_until<=clock_timestamp() FOR UPDATE`, id, version))
	if errors.Is(err, ErrNotFound) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if _, err = finishFailure(ctx, tx, t, "execution lease expired", nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) TerminateExhausted(ctx context.Context, consumer, eventID, id string, version int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	t, err := scanTask(tx.QueryRow(ctx, baseSelect+` WHERE id=$1 AND dispatch_version=$2 AND dispatch_version>0 AND state IN ('pending','failed') AND attempt>=max_attempts FOR UPDATE`, id, version))
	if errors.Is(err, ErrNotFound) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	t, err = finishFailure(ctx, tx, t, "maximum attempts exhausted", nil)
	if err != nil {
		return err
	}
	if err = inbox(ctx, tx, consumer, eventID, "dead_letter"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) RecordImportRows(ctx context.Context, e Execution, rows []map[string]any) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = lockExecution(ctx, tx, e); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `CREATE TEMP TABLE import_rows_stage (LIKE task_report.import_rows INCLUDING DEFAULTS) ON COMMIT DROP`); err != nil {
		return err
	}
	values := make([][]any, 0, len(rows))
	for i, row := range rows {
		data, marshalErr := json.Marshal(row)
		if marshalErr != nil {
			return marshalErr
		}
		values = append(values, []any{e.ID, i + 2, fmt.Sprintf("%s:%d", e.ID, i+2), data})
	}
	if _, err = tx.CopyFrom(ctx, pgx.Identifier{"import_rows_stage"}, []string{"task_id", "row_number", "row_key", "payload"}, pgx.CopyFromRows(values)); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO task_report.import_rows(task_id,row_number,row_key,payload) SELECT task_id,row_number,row_key,payload FROM import_rows_stage ON CONFLICT(task_id,row_number) DO NOTHING`); err != nil {
		return err
	}
	if err = guardExecution(ctx, tx, e, 1); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) MarkRowsCommitted(ctx context.Context, e Execution, fromRow, toRow int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = lockExecution(ctx, tx, e); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE task_report.import_rows SET committed_at=COALESCE(committed_at,clock_timestamp()),error='' WHERE task_id=$1 AND row_number BETWEEN $2 AND $3`, e.ID, fromRow, toRow); err != nil {
		return err
	}
	var progress int
	err = tx.QueryRow(ctx, `SELECT COALESCE(LEAST(99,100*count(committed_at)/NULLIF(count(*),0)),1)::int FROM task_report.import_rows WHERE task_id=$1`, e.ID).Scan(&progress)
	if err != nil {
		return err
	}
	if err = guardExecution(ctx, tx, e, progress); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) CommittedRows(ctx context.Context, e Execution) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err = lockExecution(ctx, tx, e); err != nil {
		return 0, err
	}
	var count int
	err = tx.QueryRow(ctx, `SELECT count(*) FROM task_report.import_rows WHERE task_id=$1 AND committed_at IS NOT NULL`, e.ID).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, tx.Commit(ctx)
}
func quarantineText(value string, limit int) string {
	value = strings.ReplaceAll(strings.ToValidUTF8(value, "�"), "\x00", "�")
	if len(value) > limit {
		value = strings.ToValidUTF8(value[:limit], "�")
	}
	return value
}
func quarantineValue(value any, depth int) any {
	if depth > 8 {
		return "[depth limit]"
	}
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return quarantineText(typed, 4096)
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return fmt.Sprint(typed)
		}
		return typed
	case float32:
		if math.IsNaN(float64(typed)) || math.IsInf(float64(typed), 0) {
			return fmt.Sprint(typed)
		}
		return typed
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return typed
	case []byte:
		return quarantineText(string(typed), 4096)
	}
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Map && v.Type().Key().Kind() == reflect.String {
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		result := map[string]any{}
		for _, key := range keys[:min(len(keys), 64)] {
			result[quarantineText(key.String(), 256)] = quarantineValue(v.MapIndex(key).Interface(), depth+1)
		}
		return result
	}
	if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
		result := make([]any, 0, min(v.Len(), 64))
		for i := 0; i < min(v.Len(), 64); i++ {
			result = append(result, quarantineValue(v.Index(i).Interface(), depth+1))
		}
		return result
	}
	return quarantineText(fmt.Sprint(value), 4096)
}
func (s *Store) Quarantine(ctx context.Context, raw []byte, metadata map[string]any, reason string) error {
	safe := quarantineValue(metadata, 0).(map[string]any)
	identity := make(map[string]any, len(metadata))
	for key, value := range safe {
		if key != "redelivered" {
			identity[key] = value
		}
	}
	identityJSON, err := json.Marshal(identity)
	if err != nil {
		return err
	}
	meta, err := json.Marshal(safe)
	if err != nil {
		return err
	}
	if len(meta) > 8192 {
		meta = []byte(`{"truncated":true}`)
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(append(append([]byte{}, raw...), identityJSON...)))
	text := quarantineText(string(raw), 65536)
	reason = quarantineText(reason, 2048)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	id := uuid()
	var inserted string
	err = tx.QueryRow(ctx, `INSERT INTO task_report.message_quarantine(id,fingerprint,body,metadata,reason) VALUES($1,$2,$3,$4,$5) ON CONFLICT(fingerprint) DO NOTHING RETURNING id`, id, fingerprint, text, meta, reason).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	eventID := uuid()
	payload, _ := json.Marshal(map[string]any{"schema_version": 2, "event_id": eventID, "kind": "message_quarantine", "quarantine_id": id, "reason": reason})
	if _, err = tx.Exec(ctx, `INSERT INTO task_report.outbox(event_id,topic,aggregate_id,payload) VALUES($1,'task.dead',$2,$3)`, eventID, id, payload); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) LegacyMessageSafe(ctx context.Context, eventID, id, kind string) (bool, error) {
	var valid bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_report.outbox old JOIN task_report.tasks t ON t.id=old.aggregate_id
 WHERE old.event_id=$1 AND old.aggregate_id=$2::uuid AND old.topic='task.requested' AND old.dispatch_version IS NULL AND old.payload->>'task_id'=$2::uuid::text AND old.payload->>'kind'=$3 AND t.kind=$3
 AND (t.state IN ('succeeded','cancelled','dead_letter') OR EXISTS(SELECT 1 FROM task_report.outbox canonical WHERE canonical.aggregate_id=t.id AND canonical.dispatch_version=t.dispatch_version AND canonical.topic='task.requested' AND canonical.superseded_at IS NULL)))`, eventID, id, kind).Scan(&valid)
	return valid, err
}
