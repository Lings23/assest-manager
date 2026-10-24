package taskcenter

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func permanentFailureCounts(t *testing.T, s *Store, taskID string) (int, int, int) {
	t.Helper()
	var requested, dead, letters int
	if err := s.pool.QueryRow(t.Context(), `SELECT
 (SELECT count(*) FROM task_report.outbox WHERE aggregate_id=$1 AND topic='task.requested'),
 (SELECT count(*) FROM task_report.outbox WHERE aggregate_id=$1 AND topic='task.dead'),
 (SELECT count(*) FROM task_report.dead_letters WHERE task_id=$1)`, taskID).Scan(&requested, &dead, &letters); err != nil {
		t.Fatal(err)
	}
	return requested, dead, letters
}

func TestPostgresPermanentFailureAtomicInboxDeadOutbox(t *testing.T) {
	s := executionTestStore(t)
	task := executionTestTask(t, s, "export")
	execution, err := s.ClaimTask(t.Context(), task.ID, 1, "permanent", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// The Inbox write occurs after the task, dead letter and Outbox writes.
	// A rejected event ID must roll the entire durable outcome back.
	if _, err = s.FailExecution(t.Context(), "permanent", "not-a-uuid", execution, "unknown asset type"); err == nil {
		t.Fatal("invalid Inbox event accepted")
	}
	current, err := s.WorkerTask(t.Context(), task.ID)
	if err != nil || current.State != Running || current.ExecutionToken != execution.Token || current.Attempt != 1 || current.DispatchVersion != 1 {
		t.Fatalf("failed transaction changed execution: task=%+v error=%v", current, err)
	}
	if requested, dead, letters := permanentFailureCounts(t, s, task.ID); requested != 1 || dead != 0 || letters != 0 {
		t.Fatalf("failed transaction retained side effects: requested=%d dead=%d letters=%d", requested, dead, letters)
	}
	eventID := uuid()
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM task_report.inbox WHERE consumer='permanent' AND event_id=$1`, eventID)
	})
	current, err = s.FailExecution(t.Context(), "permanent", eventID, execution, "unknown asset type")
	if err != nil {
		t.Fatal(err)
	}
	if current.State != DeadLetter || current.Attempt != 1 || current.DispatchVersion != 1 || current.NextRunAt != nil || current.WorkerID != "" || current.ExecutionToken != "" || current.LeaseUntil != nil || current.LastError != "unknown asset type" {
		t.Fatalf("permanent task was not immediately terminated and fenced: %+v", current)
	}
	if requested, dead, letters := permanentFailureCounts(t, s, task.ID); requested != 1 || dead != 1 || letters != 1 {
		t.Fatalf("permanent failure events: requested=%d dead=%d letters=%d", requested, dead, letters)
	}
	var outcome, reason string
	var payload []byte
	if err = s.pool.QueryRow(t.Context(), `SELECT i.outcome,d.reason,o.payload FROM task_report.inbox i,task_report.dead_letters d,task_report.outbox o WHERE i.consumer='permanent' AND i.event_id=$1 AND d.task_id=$2 AND o.aggregate_id=$2 AND o.topic='task.dead'`, eventID, task.ID).Scan(&outcome, &reason, &payload); err != nil {
		t.Fatal(err)
	}
	var deadMessage map[string]any
	if err = json.Unmarshal(payload, &deadMessage); err != nil {
		t.Fatal(err)
	}
	if outcome != "dead_letter" || reason != current.LastError || deadMessage["task_id"] != task.ID || deadMessage["kind"] != "export" || deadMessage["dispatch_version"] != float64(1) || deadMessage["reason"] != current.LastError {
		t.Fatalf("durable permanent outcome mismatch: outcome=%q reason=%q message=%+v", outcome, reason, deadMessage)
	}
	if _, err = s.FailExecution(t.Context(), "permanent", eventID, execution, "duplicate termination"); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale repeated termination=%v", err)
	}
	if _, dead, letters := permanentFailureCounts(t, s, task.ID); dead != 1 || letters != 1 {
		t.Fatalf("duplicate permanent dead notification: dead=%d letters=%d", dead, letters)
	}
}

func TestPostgresPermanentFailureFencesExpiredAndReplacedExecutions(t *testing.T) {
	s := executionTestStore(t)
	task := executionTestTask(t, s, "export")
	first, err := s.ClaimTask(t.Context(), task.ID, 1, "old", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, modify := range []func(*Execution){
		func(e *Execution) { e.Token = uuid() },
		func(e *Execution) { e.WorkerID = "other" },
		func(e *Execution) { e.DispatchVersion++ },
	} {
		wrong := first
		modify(&wrong)
		if _, err := s.FailExecution(t.Context(), "permanent-fence", uuid(), wrong, "wrong owner"); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("wrong execution could permanently terminate task: %v", err)
		}
	}
	expireExecution(t, s, first)
	if _, err = s.FailExecution(t.Context(), "permanent-fence", uuid(), first, "expired"); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired execution permanently terminated task: %v", err)
	}
	if err = s.RecoverExpiredTask(t.Context(), task.ID, 1); err != nil {
		t.Fatal(err)
	}
	forceDue(t, s, task.ID)
	second, err := s.ClaimTask(t.Context(), task.ID, 2, "new", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	staleEvent := uuid()
	if _, err = s.FailExecution(t.Context(), "permanent-fence", staleEvent, first, "stale permanent failure"); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("replaced execution permanently terminated new owner: %v", err)
	}
	if seen, err := s.InboxSeen(t.Context(), "permanent-fence", staleEvent); err != nil || seen {
		t.Fatalf("stale execution consumed Inbox event: seen=%v error=%v", seen, err)
	}
	current, err := s.WorkerTask(t.Context(), task.ID)
	if err != nil || current.State != Running || current.ExecutionToken != second.Token || current.Attempt != 2 {
		t.Fatalf("stale execution changed new owner: task=%+v error=%v", current, err)
	}
	if requested, dead, letters := permanentFailureCounts(t, s, task.ID); requested != 2 || dead != 0 || letters != 0 {
		t.Fatalf("rejected execution retained side effects: requested=%d dead=%d letters=%d", requested, dead, letters)
	}
	validEvent := uuid()
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM task_report.inbox WHERE consumer='permanent-fence' AND event_id=$1`, validEvent)
	})
	current, err = s.FailExecution(t.Context(), "permanent-fence", validEvent, second, "invalid remaining input")
	if err != nil || current.State != DeadLetter || current.Attempt != 2 || current.DispatchVersion != 2 {
		t.Fatalf("valid second execution was not permanently terminated: task=%+v error=%v", current, err)
	}
}

func TestPostgresPermanentFailureRechecksLeaseAfterRowLockWait(t *testing.T) {
	s := executionTestStore(t)
	task := executionTestTask(t, s, "export")
	execution, err := s.ClaimTask(t.Context(), task.ID, 1, "blocked-failure", 350*time.Millisecond)
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
	failed := make(chan error, 1)
	go func() {
		_, err := s.FailExecution(t.Context(), "permanent-wait", uuid(), execution, "late permanent result")
		failed <- err
	}()
	select {
	case err := <-failed:
		t.Fatalf("permanent failure bypassed row lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	time.Sleep(400 * time.Millisecond)
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-failed:
		if !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("expired blocked execution committed permanent failure: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("blocked permanent failure did not finish")
	}
	if _, dead, letters := permanentFailureCounts(t, s, task.ID); dead != 0 || letters != 0 {
		t.Fatalf("blocked execution retained side effects: dead=%d letters=%d", dead, letters)
	}
}
