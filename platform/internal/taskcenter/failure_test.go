package taskcenter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"asset-platform/internal/asset"
)

func TestPermanentFailurePreservesCauseAndClassification(t *testing.T) {
	cause := errors.New("immutable input is invalid")
	err := PermanentFailure(cause)
	if !errors.Is(err, cause) || !IsPermanentTaskError(fmt.Errorf("execute: %w", err)) {
		t.Fatalf("permanent error lost type or cause: %v", err)
	}
	if PermanentFailure(err) != err || PermanentFailure(nil) != nil {
		t.Fatal("permanent wrapper changed an already classified or nil error")
	}
	for _, transient := range []error{nil, context.DeadlineExceeded, context.Canceled, errors.New("network unavailable")} {
		if IsPermanentTaskError(transient) {
			t.Fatalf("infrastructure failure became permanent: %v", transient)
		}
	}
}

func TestHTTPAssetClientOnlyClassifiesKnownBusinessErrorsPermanent(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		body      string
		permanent bool
	}{
		{"invalid request", 400, `{"error":{"code":"INVALID_REQUEST","message":"invalid batch"}}`, true},
		{"invalid fields", 400, `{"error":{"code":"VALIDATION_FAILED","message":"invalid field"}}`, true},
		{"unknown asset type", 404, `{"error":{"code":"NOT_FOUND","message":"resource not found"}}`, true},
		{"legacy batch envelope may contain DB failure", 400, `{"error":{"code":"BATCH_FAILED","message":"database unavailable"}}`, false},
		{"legacy export envelope may contain DB failure", 400, `{"error":{"code":"EXPORT_FAILED","message":"database unavailable"}}`, false},
		{"server outage", 500, `{"error":{"code":"INTERNAL_ERROR","message":"database unavailable"}}`, false},
		{"server validation envelope still a server failure", 503, `{"error":{"code":"VALIDATION_FAILED"}}`, false},
		{"timeout", 408, `{"error":{"code":"INVALID_REQUEST"}}`, false},
		{"rate limit", 429, `{"error":{"code":"INVALID_REQUEST"}}`, false},
		{"temporary proxy page", 404, `upstream unavailable`, false},
		{"credential repair", 403, `{"error":{"code":"FORBIDDEN"}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := NewHTTPAssetClient(server.URL, "internal-test")
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Export(t.Context(), "responsible-department", AssetQuery{Global: true})
			if err == nil || IsPermanentTaskError(err) != test.permanent {
				t.Fatalf("classification permanent=%v error=%v", IsPermanentTaskError(err), err)
			}
		})
	}
}

// This fake uses terminated for permanent termination as well as exhausted
// termination; retries remains specific to RetryOrDead.
func (s *workerMemoryStore) FailExecution(_ context.Context, _, _ string, _ Execution, reason string) (Task, error) {
	s.terminated.Add(1)
	if s.retryErr != nil {
		return Task{}, s.retryErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.task.State = DeadLetter
	s.task.LastError = reason
	s.task.NextRunAt = nil
	s.seen = true
	return s.task, nil
}

func TestWorkerPermanentFailureCommitsBeforeAckAndNeverSchedulesRetry(t *testing.T) {
	for _, committed := range []bool{true, false} {
		t.Run(fmt.Sprintf("commit=%v", committed), func(t *testing.T) {
			store := newWorkerMemoryStore()
			if !committed {
				store.retryErr = errors.New("commit outcome unknown")
			}
			assets := workerAssets{export: func(context.Context) ([]asset.Record, error) {
				return nil, fmt.Errorf("asset request: %w", PermanentFailure(asset.ErrUnknownType))
			}}
			action := testWorker(store, assets).process(t.Context(), workerDelivery("export", 1))
			if action.ack != committed || store.terminated.Load() != 1 || store.retries.Load() != 0 || store.completes.Load() != 0 || store.task.Attempt != 1 {
				t.Fatalf("permanent failure action=%+v attempts=%d terminate=%d retry=%d", action, store.task.Attempt, store.terminated.Load(), store.retries.Load())
			}
			if committed && (store.task.State != DeadLetter || !store.seen || store.task.NextRunAt != nil || store.task.DispatchVersion != 1) {
				t.Fatalf("permanent failure scheduled another attempt: %+v", store.task)
			}
		})
	}
}

func TestWorkerImmutableInputFailuresArePermanent(t *testing.T) {
	for _, test := range []struct {
		name, kind, assetType string
		input                 map[string]any
	}{
		{"missing CSV", "import", "responsible-department", map[string]any{}},
		{"unsupported CSV column", "import", "responsible-department", map[string]any{"csv": "unknown_column\nvalue\n"}},
		{"invalid CSV row", "import", "responsible-department", map[string]any{"csv": "department_name,department_code\n,INVALID\n"}},
		{"unknown export schema", "export", "not-a-real-type", map[string]any{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newWorkerMemoryStore()
			store.task.Kind = test.kind
			store.task.AssetType = test.assetType
			store.task.Input = test.input
			action := testWorker(store, workerAssets{}).process(t.Context(), workerDelivery(test.kind, 1))
			if !action.ack || store.terminated.Load() != 1 || store.retries.Load() != 0 || store.task.State != DeadLetter || store.task.Attempt != 1 {
				t.Fatalf("immutable input retried: action=%+v task=%+v retries=%d", action, store.task, store.retries.Load())
			}
		})
	}
}
