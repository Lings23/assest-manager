package taskcenter

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func executionTestStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("TASK_TEST_DATABASE_URL")
	if configured := os.Getenv("TASKCENTER_TEST_DATABASE_URL"); configured != "" {
		url = configured
	}
	if url == "" {
		t.Skip("TASK_TEST_DATABASE_URL is not configured")
	}
	if err := Migrate(t.Context(), url, false); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}
func executionTestTask(t *testing.T, s *Store, kind string) Task {
	t.Helper()
	task, _, err := s.Create(t.Context(), Task{Kind: kind, AssetType: "responsible-department", IdempotencyKey: uuid(), RequestedBy: uuid(), Input: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = s.pool.Exec(ctx, `DELETE FROM task_report.outbox WHERE aggregate_id=$1`, task.ID)
		_, _ = s.pool.Exec(ctx, `DELETE FROM task_report.dead_letters WHERE task_id=$1`, task.ID)
		_, _ = s.pool.Exec(ctx, `DELETE FROM task_report.tasks WHERE id=$1`, task.ID)
	})
	return task
}
func expireExecution(t *testing.T, s *Store, e Execution) {
	t.Helper()
	if _, err := s.pool.Exec(t.Context(), `UPDATE task_report.tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, e.ID); err != nil {
		t.Fatal(err)
	}
}
func forceDue(t *testing.T, s *Store, id string) {
	t.Helper()
	if _, err := s.pool.Exec(t.Context(), `UPDATE task_report.tasks SET next_run_at=clock_timestamp()-interval '1 second' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresAtomicClaimAndOldExecutionFencing(t *testing.T) {
	s := executionTestStore(t)
	task := executionTestTask(t, s, "export")
	var won atomic.Int32
	var first Execution
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e, err := s.ClaimTask(t.Context(), task.ID, 1, uuid(), time.Minute)
			if err == nil {
				won.Add(1)
				mu.Lock()
				first = e
				mu.Unlock()
			} else if !errors.Is(err, ErrConflict) {
				t.Errorf("claim: %v", err)
			}
		}()
	}
	wg.Wait()
	if won.Load() != 1 {
		t.Fatalf("claims=%d", won.Load())
	}
	state, err := s.WorkerTask(t.Context(), task.ID)
	if err != nil || state.Attempt != 1 {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	expireExecution(t, s, first)
	if err = s.RenewLease(t.Context(), first, time.Minute); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired renew=%v", err)
	}
	if err = s.RecoverExpiredTask(t.Context(), task.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimTask(t.Context(), task.ID, 1, "old-message", time.Minute); !errors.Is(err, ErrConflict) {
		t.Fatalf("old version claim=%v", err)
	}
	// Retry delay is database enforced even with a notification for the new version.
	if _, err = s.ClaimTask(t.Context(), task.ID, 2, "early", time.Minute); !errors.Is(err, ErrConflict) {
		t.Fatalf("early claim=%v", err)
	}
	forceDue(t, s, task.ID)
	second, err := s.ClaimTask(t.Context(), task.ID, 2, "new", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for name, write := range map[string]func() error{
		"renew": func() error { return s.RenewLease(t.Context(), first, time.Minute) },
		"rows": func() error {
			return s.RecordImportRows(t.Context(), first, []map[string]any{{"department_name": "old"}})
		},
		"progress": func() error { return s.MarkRowsCommitted(t.Context(), first, 2, 2) },
		"complete": func() error {
			return s.Complete(t.Context(), "test", uuid(), first, map[string]any{"object_key": "old"})
		},
		"retry": func() error { _, err := s.RetryOrDead(t.Context(), "test", uuid(), first, "old failure"); return err },
	} {
		if err := write(); !errors.Is(err, ErrLeaseLost) {
			t.Errorf("%s old token=%v", name, err)
		}
	}
	event := uuid()
	if err = s.Complete(t.Context(), "fence-test", event, second, map[string]any{"object_key": "new"}); err != nil {
		t.Fatal(err)
	}
	state, err = s.WorkerTask(t.Context(), task.ID)
	if err != nil || state.State != Succeeded || state.Attempt != 2 || state.ExecutionToken != "" || state.Result["object_key"] != "new" {
		t.Fatalf("final=%+v err=%v", state, err)
	}
	if seen, err := s.InboxSeen(t.Context(), "fence-test", event); err != nil || !seen {
		t.Fatalf("inbox seen=%v err=%v", seen, err)
	}
	data, _ := json.Marshal(state)
	var dto map[string]any
	_ = json.Unmarshal(data, &dto)
	for _, key := range []string{"execution_token", "worker_id", "dispatch_version", "lease_until"} {
		if _, ok := dto[key]; ok {
			t.Errorf("private field leaked: %s", key)
		}
	}
}

func TestPostgresPreflightIsolationAndCommittedRowsReplay(t *testing.T) {
	s := executionTestStore(t)
	task := executionTestTask(t, s, "import")
	if _, err := s.Transition(t.Context(), task.ID, Pending, Running, 1, nil, ""); err != nil {
		t.Fatal(err)
	}
	if n, err := s.RecoverExpired(t.Context(), 100); err != nil || n != 0 {
		t.Fatalf("preflight recovered=%d err=%v", n, err)
	}
	if _, err := s.ClaimTask(t.Context(), task.ID, 1, "worker", time.Minute); !errors.Is(err, ErrConflict) {
		t.Fatalf("unconfirmed claim=%v", err)
	}
	if _, err := s.Transition(t.Context(), task.ID, Running, AwaitingConfirmation, 100, map[string]any{"error_count": 0}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfirmAndEnqueue(t.Context(), task.ID, task.RequestedBy); err != nil {
		t.Fatal(err)
	}
	first, err := s.ClaimTask(t.Context(), task.ID, 1, "first", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{{"department_name": "a"}, {"department_name": "b"}}
	if err = s.RecordImportRows(t.Context(), first, rows); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkRowsCommitted(t.Context(), first, 2, 2); err != nil {
		t.Fatal(err)
	}
	expireExecution(t, s, first)
	if err = s.RecoverExpiredTask(t.Context(), task.ID, 1); err != nil {
		t.Fatal(err)
	}
	forceDue(t, s, task.ID)
	second, err := s.ClaimTask(t.Context(), task.ID, 2, "second", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordImportRows(t.Context(), second, rows); err != nil {
		t.Fatal(err)
	}
	if count, err := s.CommittedRows(t.Context(), second); err != nil || count != 1 {
		t.Fatalf("preserved committed=%d err=%v", count, err)
	}
	if err = s.MarkRowsCommitted(t.Context(), second, 2, 3); err != nil {
		t.Fatal(err)
	}
	if count, err := s.CommittedRows(t.Context(), second); err != nil || count != 2 {
		t.Fatalf("committed=%d err=%v", count, err)
	}
	if _, err = s.Transition(t.Context(), task.ID, Running, AwaitingConfirmation, 0, nil, ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("unfenced async transition=%v", err)
	}
	if _, err = s.FailPermanent(t.Context(), task.ID, "unfenced"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unfenced async failure=%v", err)
	}
}

func TestPostgresFailureOutboxRecoveryAndQuarantine(t *testing.T) {
	s := executionTestStore(t)
	task := executionTestTask(t, s, "export")
	for version := int64(1); version <= 3; version++ {
		forceDue(t, s, task.ID)
		e, err := s.ClaimTask(t.Context(), task.ID, version, "retry-test", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		event := uuid()
		result, err := s.RetryOrDead(t.Context(), "retry-test", event, e, "business failure")
		if err != nil {
			t.Fatal(err)
		}
		if version < 3 && (result.State != Failed || result.DispatchVersion != version+1 || result.NextRunAt == nil) {
			t.Fatalf("retry=%+v", result)
		}
		if version == 3 && result.State != DeadLetter {
			t.Fatalf("exhausted=%+v", result)
		}
		seen, err := s.InboxSeen(t.Context(), "retry-test", event)
		if err != nil || !seen {
			t.Fatalf("retry inbox=%v %v", seen, err)
		}
	}
	var requested, dead, letters int
	if err := s.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM task_report.outbox WHERE aggregate_id=$1 AND topic='task.requested'),(SELECT count(*) FROM task_report.outbox WHERE aggregate_id=$1 AND topic='task.dead'),(SELECT count(*) FROM task_report.dead_letters WHERE task_id=$1)`, task.ID).Scan(&requested, &dead, &letters); err != nil {
		t.Fatal(err)
	}
	if requested != 3 || dead != 1 || letters != 1 {
		t.Fatalf("durable events=%d/%d/%d", requested, dead, letters)
	}
	raw := []byte("invalid-" + uuid())
	for i := 0; i < 2; i++ {
		if err := s.Quarantine(t.Context(), raw, map[string]any{"message_id": "invalid"}, "invalid payload"); err != nil {
			t.Fatal(err)
		}
	}
	var quarantined int
	var id string
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*),min(id::text) FROM task_report.message_quarantine WHERE body=$1`, string(raw)).Scan(&quarantined, &id); err != nil {
		t.Fatal(err)
	}
	if quarantined != 1 {
		t.Fatalf("quarantine duplicates=%d", quarantined)
	}
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM task_report.outbox WHERE aggregate_id=$1 AND topic='task.dead'`, id).Scan(&dead); err != nil || dead != 1 {
		t.Fatalf("quarantine outbox=%d %v", dead, err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM task_report.outbox WHERE aggregate_id=$1`, id)
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM task_report.message_quarantine WHERE id=$1`, id)
	})
}

func TestPostgresCancelAndClaimCompete(t *testing.T) {
	s := executionTestStore(t)
	for i := 0; i < 12; i++ {
		task := executionTestTask(t, s, "export")
		var wg sync.WaitGroup
		var claimed, cancelled atomic.Bool
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := s.ClaimTask(t.Context(), task.ID, 1, "cancel-race", time.Minute)
			if err == nil {
				claimed.Store(true)
			} else if !errors.Is(err, ErrConflict) {
				t.Errorf("claim=%v", err)
			}
		}()
		go func() {
			defer wg.Done()
			_, err := s.Cancel(t.Context(), task.ID, task.RequestedBy)
			if err == nil {
				cancelled.Store(true)
			} else if !errors.Is(err, ErrNotFound) {
				t.Errorf("cancel=%v", err)
			}
		}()
		wg.Wait()
		if claimed.Load() == cancelled.Load() {
			t.Fatalf("claim=%v cancel=%v", claimed.Load(), cancelled.Load())
		}
	}
}

func TestPostgresCutoverIsRepeatableAndKeepsUnconfirmedImport(t *testing.T) {
	s := executionTestStore(t)
	unconfirmed := executionTestTask(t, s, "import")
	old := executionTestTask(t, s, "export")
	if _, err := s.Transition(t.Context(), unconfirmed.ID, Pending, Running, 1, nil, ""); err != nil {
		t.Fatal(err)
	}
	oldEvent := uuid()
	_, err := s.pool.Exec(t.Context(), `DELETE FROM task_report.outbox WHERE aggregate_id=$1`, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.pool.Exec(t.Context(), `UPDATE task_report.tasks SET state='running',dispatch_version=0,next_run_at=NULL,attempt=1 WHERE id=$1`, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.pool.Exec(t.Context(), `INSERT INTO task_report.outbox(event_id,topic,aggregate_id,payload) VALUES($1,'task.requested',$2::uuid,jsonb_build_object('task_id',$2::uuid::text,'kind','export'))`, oldEvent, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = s.Cutover(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	imported, err := s.WorkerTask(t.Context(), unconfirmed.ID)
	if err != nil || imported.DispatchVersion != 0 || imported.State != Failed {
		t.Fatalf("unconfirmed=%+v %v", imported, err)
	}
	updated, err := s.WorkerTask(t.Context(), old.ID)
	if err != nil || updated.DispatchVersion != 1 || updated.State != Failed || updated.Attempt != 1 {
		t.Fatalf("cutover=%+v %v", updated, err)
	}
	safe, err := s.LegacyMessageSafe(t.Context(), oldEvent, old.ID, "export")
	if err != nil || !safe {
		t.Fatalf("legacy identity=%v %v", safe, err)
	}
	var canonical int
	var superseded bool
	if err = s.pool.QueryRow(t.Context(), `SELECT count(*) FROM task_report.outbox WHERE aggregate_id=$1 AND dispatch_version=1 AND topic='task.requested'`, old.ID).Scan(&canonical); err != nil || canonical != 1 {
		t.Fatalf("canonical=%d %v", canonical, err)
	}
	if err = s.pool.QueryRow(t.Context(), `SELECT superseded_at IS NOT NULL FROM task_report.outbox WHERE event_id=$1`, oldEvent).Scan(&superseded); err != nil || !superseded {
		t.Fatalf("superseded=%v %v", superseded, err)
	}
}

func TestPostgresRenewDoesNotResurrectLeaseAfterUnchangedLockWait(t *testing.T) {
	s := executionTestStore(t)
	task := executionTestTask(t, s, "export")
	e, err := s.ClaimTask(t.Context(), task.ID, 1, "locked-renew", 400*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(t.Context(), `SELECT id FROM task_report.tasks WHERE id=$1 FOR UPDATE`, task.ID); err != nil {
		t.Fatal(err)
	}
	renewed := make(chan error, 1)
	go func() { renewed <- s.RenewLease(t.Context(), e, time.Minute) }()
	select {
	case err := <-renewed:
		t.Fatalf("renew bypassed row lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	time.Sleep(450 * time.Millisecond)
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-renewed:
		if !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("expired lease resurrected: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("renew did not unblock")
	}
}

func TestPostgresBinaryQuarantineAndRedeliveryDedup(t *testing.T) {
	s := executionTestStore(t)
	raw := append([]byte(uuid()+"\x00"), 0xff)
	metadata := map[string]any{"message_id": "invalid\x00id", "headers": map[string]any{"nan": math.NaN(), "inf": math.Inf(1), "binary": []byte{0, 0xff}}, "redelivered": false}
	if err := s.Quarantine(t.Context(), raw, metadata, "invalid\x00payload"); err != nil {
		t.Fatal(err)
	}
	metadata["redelivered"] = true
	if err := s.Quarantine(t.Context(), raw, metadata, "invalid\x00payload"); err != nil {
		t.Fatal(err)
	}
	var id string
	var count int
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*),min(id::text) FROM task_report.message_quarantine WHERE body=$1`, quarantineText(string(raw), 65536)).Scan(&count, &id); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("redelivered quarantine count=%d", count)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM task_report.outbox WHERE aggregate_id=$1`, id)
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM task_report.message_quarantine WHERE id=$1`, id)
	})
}

func TestPostgresConcurrentRecoveryCountsOnlyActualExecutions(t *testing.T) {
	s := executionTestStore(t)
	task := executionTestTask(t, s, "export")
	for version := int64(1); version <= 3; version++ {
		forceDue(t, s, task.ID)
		execution, err := s.ClaimTask(t.Context(), task.ID, version, "doomed", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		expireExecution(t, s, execution)
		var recovered atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := s.RecoverExpiredTask(t.Context(), task.ID, version)
				if err == nil {
					recovered.Add(1)
				} else if !errors.Is(err, ErrConflict) {
					t.Errorf("recovery: %v", err)
				}
			}()
		}
		wg.Wait()
		if recovered.Load() != 1 {
			t.Fatalf("generation %d recovered %d times", version, recovered.Load())
		}
		current, err := s.WorkerTask(t.Context(), task.ID)
		if err != nil || current.Attempt != int(version) {
			t.Fatalf("attempts after recovery=%+v %v", current, err)
		}
		if version == 3 && current.State != DeadLetter {
			t.Fatalf("exhausted recovery=%+v", current)
		}
	}
	var dead int
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM task_report.outbox WHERE aggregate_id=$1 AND topic='task.dead'`, task.ID).Scan(&dead); err != nil || dead != 1 {
		t.Fatalf("recovery dead outbox=%d %v", dead, err)
	}
	abnormal := executionTestTask(t, s, "export")
	if _, err := s.pool.Exec(t.Context(), `UPDATE task_report.tasks SET attempt=max_attempts WHERE id=$1`, abnormal.ID); err != nil {
		t.Fatal(err)
	}
	event := uuid()
	if err := s.TerminateExhausted(t.Context(), "exhausted", event, abnormal.ID, 1); err != nil {
		t.Fatal(err)
	}
	seen, err := s.InboxSeen(t.Context(), "exhausted", event)
	if err != nil || !seen {
		t.Fatalf("exhausted inbox=%v %v", seen, err)
	}
}

func TestPostgresClaimAllocatesLeaseAfterUnchangedRowLockWait(t *testing.T) {
	s := executionTestStore(t)
	task := executionTestTask(t, s, "export")
	tx, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(t.Context(), `SELECT id FROM task_report.tasks WHERE id=$1 FOR UPDATE`, task.ID); err != nil {
		t.Fatal(err)
	}
	type claimResult struct {
		execution Execution
		err       error
	}
	claimed := make(chan claimResult, 1)
	go func() {
		e, err := s.ClaimTask(t.Context(), task.ID, 1, "locked-claim", 400*time.Millisecond)
		claimed <- claimResult{e, err}
	}()
	select {
	case result := <-claimed:
		t.Fatalf("claim bypassed lock: %v", result.err)
	case <-time.After(50 * time.Millisecond):
	}
	time.Sleep(450 * time.Millisecond)
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-claimed:
		if result.err != nil {
			t.Fatal(result.err)
		}
		current, err := s.WorkerTask(t.Context(), task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !result.execution.LeaseUntil.After(current.ObservedAt.Add(250 * time.Millisecond)) {
			t.Fatalf("claim allocated stale lease: until=%v observed=%v", result.execution.LeaseUntil, current.ObservedAt)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("claim did not unblock")
	}
}
