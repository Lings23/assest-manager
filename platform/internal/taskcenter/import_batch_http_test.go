package taskcenter

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"asset-platform/internal/asset"
)

func TestImportBatcherMatchesActualHTTPBytes(t *testing.T) {
	const hard int64 = 1 << 20
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Token") != "test-token" {
			t.Error("missing internal credential")
			w.WriteHeader(403)
			return
		}
		if r.URL.Path == "/internal/v1/assets/batch-limits" {
			_ = json.NewEncoder(w).Encode(asset.BatchLimits{MaxRows: 500, MaxBodyBytes: hard})
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, hard))
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		var request asset.ImportBatchRequest
		if err := json.Unmarshal(body, &request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if r.ContentLength != int64(len(body)) || len(request.Rows) < 1 || len(request.Rows) > 500 {
			t.Error("wire contract differs")
		}
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(asset.BatchResult{Created: len(request.Rows)})
	}))
	defer server.Close()
	client, err := NewHTTPAssetClient(server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	limits, err := client.BatchLimits(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	task := Task{ID: workerTaskID, RequestedBy: "owner<&中文", DepartmentID: "dept>"}
	batcher, err := newImportBatcher(task, limits)
	if err != nil {
		t.Fatal(err)
	}
	all := make([]map[string]any, 1000)
	for i := range all {
		all[i] = map[string]any{"department_name": "中文<&>", "department_code": "code", "remarks": strings.Repeat("<&>中文", 180)}
	}
	created := 0
	for start := 0; start < len(all); {
		rows, end, _, err := batcher.next(all, start)
		if err != nil {
			t.Fatal(err)
		}
		result, err := client.Batch(t.Context(), task.ID, "responsible-department", task.RequestedBy, task.DepartmentID, rows)
		if err != nil {
			t.Fatal(err)
		}
		created += result.Created
		start = end
	}
	if created != 1000 || requests.Load() <= 2 {
		t.Fatalf("actual rows=%d batches=%d", created, requests.Load())
	}
}
