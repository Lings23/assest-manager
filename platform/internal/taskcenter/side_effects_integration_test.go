package taskcenter

import (
	"asset-platform/internal/asset"
	"asset-platform/internal/iam"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type exportDownloadAuthorizer struct{ user string }

func (a exportDownloadAuthorizer) Authorize(context.Context, string, string) (iam.AuthorizationContext, error) {
	return iam.AuthorizationContext{Principal: iam.Principal{UserID: a.user, DataScope: iam.ScopeGlobal}}, nil
}

func TestPostgresLateS3UploadCannotReplaceCurrentDownload(t *testing.T) {
	s := executionTestStore(t)
	task := executionTestTask(t, s, "export")
	old, err := s.ClaimTask(t.Context(), task.ID, 1, "old-export", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	oldArrived, releaseOld := make(chan struct{}), make(chan struct{})
	var release sync.Once
	var mu sync.Mutex
	objects := map[string][]byte{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Error("S3 request unsigned")
		}
		if r.Method == http.MethodPut {
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			if strings.Contains(r.URL.Path, old.Token) {
				close(oldArrived)
				<-releaseOld
			}
			mu.Lock()
			objects[r.URL.Path] = data
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}
		mu.Lock()
		data, ok := objects[r.URL.Path]
		mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write(data)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { release.Do(func() { close(releaseOld) }) })
	remote, err := NewS3Store(server.URL, "access", "secret", "asset-exports", 7)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	makeAssets := func(name string) workerAssets {
		return workerAssets{export: func(context.Context) ([]asset.Record, error) {
			return []asset.Record{{ID: uuid(), Fields: map[string]any{"department_name": name}}}, nil
		}}
	}
	oldWorker := NewWorker(s, nil, makeAssets("OLD-LATE-CONTENT"), remote, logger)
	type exportResult struct {
		value map[string]any
		err   error
	}
	oldResult := make(chan exportResult, 1)
	go func() { result, err := oldWorker.runExport(t.Context(), old); oldResult <- exportResult{result, err} }()
	select {
	case <-oldArrived:
	case <-time.After(3 * time.Second):
		t.Fatal("old upload did not reach remote server")
	}
	expireExecution(t, s, old)
	if err = s.RecoverExpiredTask(t.Context(), task.ID, 1); err != nil {
		t.Fatal(err)
	}
	forceDue(t, s, task.ID)
	current, err := s.ClaimTask(t.Context(), task.ID, 2, "current-export", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	currentWorker := NewWorker(s, nil, makeAssets("NEW-CURRENT-CONTENT"), remote, logger)
	result, err := currentWorker.runExport(t.Context(), current)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Complete(t.Context(), "late-upload", uuid(), current, result); err != nil {
		t.Fatal(err)
	}
	release.Do(func() { close(releaseOld) })
	select {
	case late := <-oldResult:
		if late.err != nil {
			t.Fatal(late.err)
		}
		if err = s.Complete(t.Context(), "late-upload", uuid(), old, late.value); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("stale complete=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("old upload did not complete")
	}
	service := NewService(s, exportDownloadAuthorizer{task.RequestedBy}, nil, remote)
	body, _, err := service.Download(t.Context(), "owner-token", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	downloaded, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(downloaded), "NEW-CURRENT-CONTENT") || strings.Contains(string(downloaded), "OLD-LATE-CONTENT") {
		t.Fatalf("download overwritten: %q", downloaded)
	}
	mu.Lock()
	count := len(objects)
	mu.Unlock()
	if count != 2 {
		t.Fatalf("token-scoped remote objects=%d", count)
	}
}

func TestPostgresAssetCommitWithLostHTTPResponseReplaysCumulativeProgress(t *testing.T) {
	s := executionTestStore(t)
	url := os.Getenv("TASKCENTER_TEST_DATABASE_URL")
	if url == "" {
		url = os.Getenv("TASK_TEST_DATABASE_URL")
	}
	assets, err := asset.OpenStore(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer assets.Close()
	task := executionTestTask(t, s, "import")
	input, _ := json.Marshal(map[string]any{"csv": "department_name,department_code\na,LOST-A\nb,LOST-B\n"})
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
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/v1/assets/batch-limits" {
			_ = json.NewEncoder(w).Encode(asset.BatchLimits{MaxRows: asset.MaxImportBatchRows, MaxBodyBytes: 1 << 20})
			return
		}
		var request struct {
			TaskID       string            `json:"task_id"`
			OwnerID      string            `json:"owner_id"`
			DepartmentID string            `json:"department_id"`
			Rows         []asset.ImportRow `json:"rows"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
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
		// Drop the HTTP response only after the actual asset+versions+reservations commit.
		if calls.Add(1) == 1 {
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
		t.Fatalf("lost response retry=%+v %v", failed, err)
	}
	forceDue(t, s, task.ID)
	second := worker.process(t.Context(), deliver(2))
	if !second.ack || second.err != nil {
		t.Fatalf("replay outcome=%+v", second)
	}
	done, err := s.WorkerTask(t.Context(), task.ID)
	if err != nil || done.State != Succeeded || done.Attempt != 2 || done.Result["created"] != float64(0) || done.Result["skipped"] != float64(2) || done.Result["committed"] != float64(2) {
		t.Fatalf("cumulative=%+v %v", done, err)
	}
	var reservations, versions, records int
	if err = s.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM asset.import_idempotency WHERE task_id=$1),
 (SELECT count(*) FROM asset.asset_versions WHERE asset_id IN(SELECT asset_id::text FROM asset.import_idempotency WHERE task_id=$1)),
 (SELECT count(*) FROM asset.responsible_department_assets WHERE id IN(SELECT asset_id::text FROM asset.import_idempotency WHERE task_id=$1))`, task.ID).Scan(&reservations, &versions, &records); err != nil {
		t.Fatal(err)
	}
	if reservations != 2 || versions != 2 || records != 2 {
		t.Fatalf("duplicate side effects: %d/%d/%d", reservations, versions, records)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM asset.asset_versions WHERE asset_id IN(SELECT asset_id::text FROM asset.import_idempotency WHERE task_id=$1)`, task.ID)
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM asset.responsible_department_assets WHERE id IN(SELECT asset_id::text FROM asset.import_idempotency WHERE task_id=$1)`, task.ID)
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM asset.import_idempotency WHERE task_id=$1`, task.ID)
	})
}
