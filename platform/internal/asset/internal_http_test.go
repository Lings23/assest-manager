package asset

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"asset-platform/internal/servicekit"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestInternalAssetErrorsDistinguishBusinessFailureFromDatabaseOutage(t *testing.T) {
	pool, err := pgxpool.New(t.Context(), "postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the handler's real Store operations against an unavailable pool.
	// No connection is made to a production or test database.
	pool.Close()
	mux := http.NewServeMux()
	NewInternalHTTPHandler(&Store{pool: pool}, "test-internal").Register(mux)
	for _, test := range []struct {
		name, path, body, code string
		status                 int
	}{
		{"batch DB unavailable", "responsible-department/batch", `{"task_id":"task","owner_id":"owner","rows":[{"key":"row","fields":{"department_name":"test"}}]}`, "INTERNAL_ERROR", 500},
		{"export DB unavailable", "responsible-department/export-page", `{"global":true,"limit":5000}`, "INTERNAL_ERROR", 500},
		{"invalid batch fields", "responsible-department/batch", `{"task_id":"task","owner_id":"owner","rows":[{"key":"row","fields":{}}]}`, "VALIDATION_FAILED", 400},
		{"unknown batch type", "not-a-real-type/batch", `{"task_id":"task","owner_id":"owner","rows":[]}`, "NOT_FOUND", 404},
		{"unknown export type", "not-a-real-type/export-page", `{"global":true}`, "NOT_FOUND", 404},
		{"malformed request", "responsible-department/export-page", `{"unknown":true}`, "INVALID_REQUEST", 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/internal/v1/assets/"+test.path, strings.NewReader(test.body))
			request.Header.Set("X-Internal-Token", "test-internal")
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, request)
			var response servicekit.ErrorResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != test.status || response.Error.Code != test.code {
				t.Fatalf("HTTP error=%d/%s want=%d/%s body=%s", recorder.Code, response.Error.Code, test.status, test.code, recorder.Body.String())
			}
		})
	}
}

func TestInternalBatchLimitsRequireCredentialsAndUseEffectiveLimit(t *testing.T) {
	for _, limit := range []int64{0, 256 << 10, 16 << 20} {
		mux := http.NewServeMux()
		h := NewInternalHTTPHandler(nil, "test-internal")
		if limit > 0 {
			h = NewInternalHTTPHandler(nil, "test-internal", limit)
		}
		h.Register(mux)
		for _, token := range []string{"", "wrong", "test-internal"} {
			r := httptest.NewRequest(http.MethodPost, "/internal/v1/assets/batch-limits", strings.NewReader("{}"))
			r.Header.Set("X-Internal-Token", token)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if token != "test-internal" {
				if w.Code != 403 {
					t.Fatalf("capabilities leaked: %d", w.Code)
				}
				continue
			}
			var got BatchLimits
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			want := limit
			if want == 0 {
				want = 1 << 20
			}
			want = min(want, InternalDecodeLimitBytes)
			if w.Code != 200 || got.MaxRows != 500 || got.MaxBodyBytes != want {
				t.Fatalf("effective limit=%+v status=%d want=%d", got, w.Code, want)
			}
		}
	}
}
