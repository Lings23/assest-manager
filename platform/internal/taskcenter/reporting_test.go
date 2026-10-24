package taskcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"asset-platform/internal/asset"
	"asset-platform/internal/iam"
)

func TestHTTPCountsUsesOneCallAndOnlyAuthorizedScope(t *testing.T) {
	queries := []AssetQuery{
		{Global: true}, {OwnerID: "owner"}, {DepartmentIDs: []string{"department"}},
		{DepartmentIDs: []string{"department", "child"}}, {},
	}
	for _, q := range queries {
		var calls atomic.Int64
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if r.Method != "POST" || r.URL.Path != "/internal/v1/assets/counts" || r.Header.Get("X-Internal-Token") != "test-internal" {
				t.Error("wrong internal count request")
			}
			var got AssetQuery
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil || !reflect.DeepEqual(got, q) {
				t.Errorf("scope=%+v want=%+v err=%v", got, q, err)
			}
			_ = json.NewEncoder(w).Encode([]asset.Count{{AssetType: "data", Total: 42}})
		}))
		client, err := NewHTTPAssetClient(server.URL, "test-internal")
		if err != nil {
			t.Fatal(err)
		}
		counts, err := client.Counts(t.Context(), q)
		server.Close()
		if err != nil || calls.Load() != 1 || len(counts) != 1 || counts[0].Total != 42 {
			t.Fatalf("calls=%d counts=%v err=%v", calls.Load(), counts, err)
		}
	}
}

func TestPostgresCountTransportComparison(t *testing.T) {
	dsn := os.Getenv("ASSET_COUNT_PARITY_DATABASE_URL")
	if dsn == "" {
		t.Skip("ASSET_COUNT_PARITY_DATABASE_URL is not configured")
	}
	store, err := asset.OpenStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mux := http.NewServeMux()
	asset.NewInternalHTTPHandler(store, "test-internal").Register(mux)
	var calls, received, sent atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			http.Error(w, "read request", 500)
			return
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
		sent.Add(int64(len(body)))
		capture := httptest.NewRecorder()
		mux.ServeHTTP(capture, r)
		calls.Add(1)
		received.Add(int64(capture.Body.Len()))
		for k, values := range capture.Header() {
			w.Header()[k] = values
		}
		w.WriteHeader(capture.Code)
		_, _ = w.Write(capture.Body.Bytes())
	}))
	defer server.Close()
	client, err := NewHTTPAssetClient(server.URL, "test-internal")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []AssetQuery{{Global: true, ViewSensitive: true}, {}} {
		calls.Store(0)
		received.Store(0)
		sent.Store(0)
		started := time.Now()
		counts, err := client.Counts(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		countTime, countCalls, countBytes, countSent := time.Since(started), calls.Load(), received.Load(), sent.Load()
		if len(counts) != 7 || calls.Load() != 1 {
			t.Fatalf("counts=%v calls=%d", counts, calls.Load())
		}
		calls.Store(0)
		received.Store(0)
		sent.Store(0)
		started = time.Now()
		for _, count := range counts {
			records, err := client.Export(t.Context(), count.AssetType, q)
			if err != nil || int64(len(records)) != count.Total {
				t.Fatalf("type=%s exported=%d count=%d err=%v", count.AssetType, len(records), count.Total, err)
			}
		}
		t.Logf("TRANSPORT global=%v sensitive=%v count_ms=%.3f count_calls=%d count_request_bytes=%d count_response_bytes=%d export_ms=%.3f export_calls=%d export_request_bytes=%d export_response_bytes=%d", q.Global, q.ViewSensitive, float64(countTime)/float64(time.Millisecond), countCalls, countSent, countBytes, float64(time.Since(started))/float64(time.Millisecond), calls.Load(), sent.Load(), received.Load())
	}
}

type deniedCountAuthorizer struct{ calls int }

func (a *deniedCountAuthorizer) Authorize(context.Context, string, string) (iam.AuthorizationContext, error) {
	a.calls++
	return iam.AuthorizationContext{}, iam.ErrForbidden
}

func TestAssetCountsDoesNotCallAssetServiceAfterDeniedAuthorization(t *testing.T) {
	auth := &deniedCountAuthorizer{}
	s := NewService(nil, auth, nil, nil)
	if _, err := s.AssetCounts(t.Context(), "Bearer denied"); !errors.Is(err, iam.ErrForbidden) || auth.calls != 1 {
		t.Fatalf("err=%v authorization calls=%d", err, auth.calls)
	}
}

func TestReportingAuthorizationScopes(t *testing.T) {
	for _, tc := range []struct {
		scope iam.DataScope
		want  AssetQuery
	}{
		{iam.ScopeOwn, AssetQuery{OwnerID: "user"}},
		{iam.ScopeDepartment, AssetQuery{DepartmentIDs: []string{"dept"}}},
		{iam.ScopeDescendants, AssetQuery{DepartmentIDs: []string{"dept"}}},
		{iam.ScopeGlobal, AssetQuery{Global: true}},
	} {
		got := queryFromAuthorization(iam.AuthorizationContext{Principal: iam.Principal{UserID: "user", DataScope: tc.scope}, DepartmentIDs: []string{"dept"}})
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("scope=%s got=%+v", tc.scope, got)
		}
	}
}
