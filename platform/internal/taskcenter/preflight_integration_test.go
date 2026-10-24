package taskcenter

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"asset-platform/internal/asset"
)

func TestPostgresAsyncPreflightRecoveryAndConfirmationFence(t *testing.T) {
	s := executionTestStore(t)
	task, created, err := s.Create(t.Context(), Task{Kind: "import", AssetType: "responsible-department", IdempotencyKey: uuid(), RequestedBy: uuid(), Input: map[string]any{"phase": "preflight", "source_object_key": "imports/source.csv"}})
	if err != nil || !created || task.DispatchVersion != 1 {
		t.Fatalf("create=%+v created=%v err=%v", task, created, err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM task_report.outbox WHERE aggregate_id=$1`, task.ID)
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM task_report.tasks WHERE id=$1`, task.ID)
	})
	old, err := s.ClaimTask(t.Context(), task.ID, 1, "preflight-a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	expireExecution(t, s, old)
	if n, err := s.RecoverExpired(t.Context(), 100); err != nil || n < 1 {
		t.Fatalf("recover=%d %v", n, err)
	}
	if err = s.Complete(t.Context(), "test", uuid(), old, map[string]any{"error_object_key": "old"}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale preflight published: %v", err)
	}
	if _, err = s.pool.Exec(t.Context(), `UPDATE task_report.tasks SET next_run_at=clock_timestamp() WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	current, err := s.WorkerTask(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	exec, err := s.ClaimTask(t.Context(), task.ID, current.DispatchVersion, "preflight-b", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Complete(t.Context(), "test", uuid(), exec, map[string]any{"total": 1, "valid": 1, "error_count": 0}); err != nil {
		t.Fatal(err)
	}
	ready, err := s.Get(t.Context(), task.ID, task.RequestedBy)
	if err != nil || ready.State != AwaitingConfirmation || !isPreflight(ready) {
		t.Fatalf("ready=%+v err=%v", ready, err)
	}
	if _, err = s.ClaimTask(t.Context(), task.ID, ready.DispatchVersion, "must-not-import", time.Minute); !errors.Is(err, ErrConflict) {
		t.Fatalf("unconfirmed claim=%v", err)
	}
	confirmed, err := s.ConfirmAndEnqueue(t.Context(), task.ID, task.RequestedBy)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.DispatchVersion != ready.DispatchVersion+1 || confirmed.Attempt != 0 || isPreflight(confirmed) {
		t.Fatalf("confirmation did not fence/reset: %+v", confirmed)
	}
	if _, err = s.ClaimTask(t.Context(), task.ID, ready.DispatchVersion, "stale-stage", time.Minute); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale generation claimed: %v", err)
	}
}

func TestPostgresCancelledPreflightRejectsLateReportAndDigestMismatch(t *testing.T) {
	s := executionTestStore(t)
	user := uuid()
	objects := &streamingFailureObjects{source: strings.NewReader("department_name\nExample\n")}
	service := NewService(s, exportDownloadAuthorizer{user}, nil, objects)
	key := uuid()
	task, _, err := service.Create(t.Context(), "token", "import", "responsible-department", key, "", "department_name\nExample\n")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM task_report.outbox WHERE aggregate_id=$1`, task.ID)
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM task_report.tasks WHERE id=$1`, task.ID)
	})
	duplicate, created, err := service.Create(t.Context(), "token", "import", "responsible-department", key, "", "department_name\nExample\n")
	if err != nil || created || duplicate.ID != task.ID {
		t.Fatalf("same digest failed: %v", err)
	}
	if _, _, err = service.Create(t.Context(), "token", "import", "responsible-department", key, "", "department_name\nDifferent\n"); !errors.Is(err, ErrConflict) {
		t.Fatalf("different content accepted: %v", err)
	}
	exec, err := s.ClaimTask(t.Context(), task.ID, 1, "worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Cancel(t.Context(), "token", task.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Complete(t.Context(), "test", uuid(), exec, map[string]any{"error_object_key": "late"}); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("cancelled preflight published", err)
	}
}

func TestPreflightNeverCallsAssetBatchAndUsesExecutionScopedReport(t *testing.T) {
	for _, source := range []string{"department_name\nExample\n", "department_name,department_code\n,INVALID\n"} {
		store := newWorkerMemoryStore()
		objects := &streamingFailureObjects{source: strings.NewReader(source)}
		worker := NewWorker(store, nil, workerAssets{batch: func(context.Context, []asset.ImportRow) (asset.BatchResult, error) {
			t.Fatal("preflight imported assets")
			return asset.BatchResult{}, nil
		}}, objects, slog.New(slog.NewTextHandler(io.Discard, nil)))
		exec := Execution{Task: Task{ID: uuid(), Kind: "import", AssetType: "responsible-department", Input: map[string]any{"phase": "preflight", "source_object_key": "source"}}, Token: uuid()}
		result, err := worker.runPreflight(t.Context(), exec)
		if err != nil {
			t.Fatal(err)
		}
		if key := stringInput(result, "error_object_key"); key != "" && !strings.Contains(key, "/"+exec.Token+".") {
			t.Fatal("unfenced error key", key)
		}
	}
}
