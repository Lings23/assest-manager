package taskcenter

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"

	"asset-platform/internal/servicekit"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct{ pool *pgxpool.Pool }

func OpenStore(ctx context.Context, databaseURL string, observation ...*servicekit.Metrics) (*Store, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	var metrics *servicekit.Metrics
	if len(observation) > 0 {
		metrics = observation[0]
	}
	if metrics != nil {
		config.ConnConfig.Tracer = servicekit.NewDatabaseTracer(metrics, "primary")
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if metrics != nil {
		metrics.RegisterPool("primary", pool)
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	store := &Store{pool: pool}
	if err = store.CheckSchema(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}
func (s *Store) Close() { s.pool.Close() }

func (s *Store) Create(ctx context.Context, task Task) (Task, bool, error) {
	if (task.Kind != "import" && task.Kind != "export") || task.AssetType == "" || task.IdempotencyKey == "" || task.RequestedBy == "" || task.MaxAttempts < 0 || task.MaxAttempts > 10 {
		return Task{}, false, ErrInvalid
	}
	if task.MaxAttempts == 0 {
		task.MaxAttempts = 3
	}
	task.ID = uuid()
	task.State = Pending
	input, _ := json.Marshal(task.Input)
	if task.Kind == "export" {
		task.DispatchVersion = 1
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Task{}, false, err
	}
	defer tx.Rollback(ctx)
	row := tx.QueryRow(ctx, `INSERT INTO task_report.tasks(id,kind,asset_type,state,idempotency_key,requested_by,department_id,max_attempts,input,dispatch_version,next_run_at)
	 VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,'')::uuid,$8,$9,$10,CASE WHEN $10::bigint>0 THEN clock_timestamp() END) ON CONFLICT(requested_by,kind,idempotency_key) DO NOTHING RETURNING created_at,updated_at,next_run_at`, task.ID, task.Kind, task.AssetType, task.State, task.IdempotencyKey, task.RequestedBy, task.DepartmentID, task.MaxAttempts, input, task.DispatchVersion)
	if err = row.Scan(&task.CreatedAt, &task.UpdatedAt, &task.NextRunAt); errors.Is(err, pgx.ErrNoRows) {
		_ = tx.Rollback(ctx)
		existing, getErr := s.GetByKey(ctx, task.RequestedBy, task.Kind, task.IdempotencyKey)
		return existing, false, getErr
	} else if err != nil {
		return Task{}, false, err
	}
	if task.Kind == "export" {
		if err = enqueueTask(ctx, tx, task); err != nil {
			return Task{}, false, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Task{}, false, err
	}
	return task, true, nil
}

func (s *Store) GetByKey(ctx context.Context, user, kind, key string) (Task, error) {
	return scanTask(s.pool.QueryRow(ctx, baseSelect+` WHERE requested_by=$1 AND kind=$2 AND idempotency_key=$3`, user, kind, key))
}
func (s *Store) Get(ctx context.Context, id, user string) (Task, error) {
	return scanTask(s.pool.QueryRow(ctx, baseSelect+` WHERE id=$1 AND requested_by=$2`, id, user))
}
func (s *Store) List(ctx context.Context, user, kind string) ([]Task, error) {
	rows, err := s.pool.Query(ctx, baseSelect+` WHERE requested_by=$1 AND ($2='' OR kind=$2) ORDER BY created_at DESC LIMIT 100`, user, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		t, e := scanTask(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) Transition(ctx context.Context, id string, from, to State, progress int, result map[string]any, lastError string) (Task, error) {
	if !((from == Pending && to == Running) || (from == Running && to == AwaitingConfirmation)) {
		return Task{}, ErrConflict
	}
	data, _ := json.Marshal(result)
	t, err := scanTask(s.pool.QueryRow(ctx, `UPDATE task_report.tasks SET state=$3,progress=$4,result=COALESCE(NULLIF($5::jsonb,'null'::jsonb),'{}'::jsonb),last_error=$6,updated_at=now() WHERE id=$1 AND state=$2 AND kind='import' AND dispatch_version=0 RETURNING `+columns, id, from, to, progress, data, lastError))
	if errors.Is(err, ErrNotFound) {
		return Task{}, ErrConflict
	}
	return t, err
}

func (s *Store) Cancel(ctx context.Context, id, user string) (Task, error) {
	return scanTask(s.pool.QueryRow(ctx, `UPDATE task_report.tasks SET state='cancelled',updated_at=now() WHERE id=$1 AND requested_by=$2 AND state IN('pending','awaiting_confirmation','failed') RETURNING `+columns, id, user))
}

const columns = `id,kind,asset_type,state,idempotency_key,requested_by,COALESCE(department_id::text,''),progress,attempt,max_attempts,input,result,last_error,created_at,updated_at,dispatch_version,next_run_at,COALESCE(worker_id,''),COALESCE(execution_token::text,''),lease_until`
const baseSelect = `SELECT ` + columns + ` FROM task_report.tasks`
const baseSelectPrefix = `WITH changed AS (`

type rowScanner interface{ Scan(...any) error }

func scanTask(row rowScanner) (Task, error) {
	var t Task
	var in, out []byte
	err := row.Scan(&t.ID, &t.Kind, &t.AssetType, &t.State, &t.IdempotencyKey, &t.RequestedBy, &t.DepartmentID, &t.Progress, &t.Attempt, &t.MaxAttempts, &in, &out, &t.LastError, &t.CreatedAt, &t.UpdatedAt, &t.DispatchVersion, &t.NextRunAt, &t.WorkerID, &t.ExecutionToken, &t.LeaseUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err == nil {
		_ = json.Unmarshal(in, &t.Input)
		_ = json.Unmarshal(out, &t.Result)
	}
	return t, err
}
func uuid() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	s := hex.EncodeToString(b)
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
