package taskcenter

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"asset-platform/internal/asset"
)

func TestPostgresWidePartialCommitLostHTTPRecovery(t *testing.T) {
	s := executionTestStore(t)
	dsn := os.Getenv("TASKCENTER_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("TASK_TEST_DATABASE_URL")
	}
	assets, err := asset.OpenStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer assets.Close()
	task := executionTestTask(t, s, "import")
	const total = 600
	var source strings.Builder
	source.WriteString("department_name,department_code,remarks\n")
	for i := 0; i < total; i++ {
		source.WriteString("wide,PARTIAL,")
		source.WriteString(strings.Repeat("<&>中文", 180))
		source.WriteByte('\n')
	}
	input, _ := json.Marshal(map[string]any{"csv": source.String()})
	if _, err = s.pool.Exec(t.Context(), `UPDATE task_report.tasks SET input=$2 WHERE id=$1`, task.ID, input); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Transition(t.Context(), task.ID, Pending, Running, 1, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Transition(t.Context(), task.ID, Running, AwaitingConfirmation, 100, map[string]any{"error_count": 0}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConfirmAndEnqueue(t.Context(), task.ID, task.RequestedBy); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM asset.asset_versions WHERE asset_id IN(SELECT asset_id::text FROM asset.import_idempotency WHERE task_id=$1)`, task.ID)
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM asset.responsible_department_assets WHERE id IN(SELECT asset_id::text FROM asset.import_idempotency WHERE task_id=$1)`, task.ID)
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM asset.import_idempotency WHERE task_id=$1`, task.ID)
	})
	var calls, firstBatchRows atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/v1/assets/batch-limits" {
			_ = json.NewEncoder(w).Encode(asset.BatchLimits{MaxRows: 500, MaxBodyBytes: 1 << 20})
			return
		}
		var request asset.ImportBatchRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "decode", 400)
			return
		}
		result, err := assets.CreateBatchFast(r.Context(), request.TaskID, "responsible-department", request.OwnerID, request.DepartmentID, request.Rows)
		if err != nil {
			t.Error(err)
			http.Error(w, "database", 500)
			return
		}
		n := calls.Add(1)
		if n == 1 {
			firstBatchRows.Store(int32(len(request.Rows)))
		}
		if n == 2 {
			// First byte-bounded batch and progress already committed; second
			// asset transaction commits but its HTTP response is lost.
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = connection.Close()
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	}))
	defer server.Close()
	client, err := NewHTTPAssetClient(server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(s, nil, client, &workerObjects{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	deliver := func(version int64) Delivery {
		var id string
		var payload []byte
		if err := s.pool.QueryRow(t.Context(), `SELECT event_id,payload FROM task_report.outbox WHERE aggregate_id=$1 AND dispatch_version=$2 AND topic='task.requested'`, task.ID, version).Scan(&id, &payload); err != nil {
			t.Fatal(err)
		}
		return Delivery{Body: payload, MessageID: id, Headers: map[string]any{"event_id": id}, RoutingKey: "task.requested"}
	}
	first := worker.process(t.Context(), deliver(1))
	if !first.ack || first.err != nil {
		t.Fatalf("first outcome=%+v", first)
	}
	failed, err := s.WorkerTask(t.Context(), task.ID)
	if err != nil || failed.State != Failed || failed.Attempt != 1 || failed.DispatchVersion != 2 {
		t.Fatalf("partial retry=%+v err=%v", failed, err)
	}
	var staged, committed, reserved int
	if err = s.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM task_report.import_rows WHERE task_id=$1),(SELECT count(*) FROM task_report.import_rows WHERE task_id=$1 AND committed_at IS NOT NULL),(SELECT count(*) FROM asset.import_idempotency WHERE task_id=$1)`, task.ID).Scan(&staged, &committed, &reserved); err != nil {
		t.Fatal(err)
	}
	if staged != total || committed != int(firstBatchRows.Load()) || committed <= 0 || reserved <= committed || reserved >= total {
		t.Fatalf("partial state=%d/%d/%d", staged, committed, reserved)
	}
	forceDue(t, s, task.ID)
	second := worker.process(t.Context(), deliver(2))
	if !second.ack || second.err != nil {
		t.Fatalf("second outcome=%+v", second)
	}
	done, err := s.WorkerTask(t.Context(), task.ID)
	if err != nil || done.State != Succeeded || done.Attempt != 2 || done.Result["created"] != float64(total-reserved) || done.Result["skipped"] != float64(reserved) || done.Result["committed"] != float64(total) {
		t.Fatalf("recovered result=%+v err=%v", done, err)
	}
	var keys, records, versions, committedFinal int
	if err = s.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM asset.import_idempotency WHERE task_id=$1),(SELECT count(*) FROM asset.responsible_department_assets WHERE id IN(SELECT asset_id::text FROM asset.import_idempotency WHERE task_id=$1)),(SELECT count(*) FROM asset.asset_versions WHERE asset_id IN(SELECT asset_id::text FROM asset.import_idempotency WHERE task_id=$1)),(SELECT count(*) FROM task_report.import_rows WHERE task_id=$1 AND committed_at IS NOT NULL)`, task.ID).Scan(&keys, &records, &versions, &committedFinal); err != nil {
		t.Fatal(err)
	}
	if keys != total || records != total || versions != total || committedFinal != total {
		t.Fatalf("duplicate/lost rows=%d/%d/%d/%d", keys, records, versions, committedFinal)
	}
	// Redelivering the completed dispatch must not even call Asset Service again.
	before := calls.Load()
	replay := worker.process(t.Context(), deliver(2))
	if !replay.ack || replay.err != nil || calls.Load() != before {
		t.Fatalf("completed replay=%+v calls=%d→%d", replay, before, calls.Load())
	}
}
