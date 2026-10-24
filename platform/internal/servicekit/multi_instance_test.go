package servicekit

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestReadinessIsCachedAndDrainingDoesNotChangeLiveness(t *testing.T) {
	var probes atomic.Int32
	cfg, err := LoadConfig("test", ":8080")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Readiness = NewReadiness(func(context.Context) error { probes.Add(1); return errors.New("database unavailable") })
	h := NewHandler(cfg, NewLogger("test"), func(*http.ServeMux) {})
	for i := 0; i < 10; i++ {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("GET", "/health/ready", nil))
		if r.Code != 503 {
			t.Fatal(r.Code)
		}
	}
	if probes.Load() != 1 {
		t.Fatal("uncached probe storm", probes.Load())
	}
	cfg.Readiness.Drain()
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/health/live", nil))
	if r.Code != 200 {
		t.Fatal(r.Code)
	}
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/health/ready", nil))
	if r.Code != 503 {
		t.Fatal(r.Code)
	}
}

func TestPoolBudgetPrecedenceAndInvalidConfiguration(t *testing.T) {
	for _, test := range []struct {
		url, env string
		want     int32
		bad      bool
	}{
		{"postgres://localhost/db", "", 5, false},
		{"postgres://localhost/db?pool_max_conns=3", "", 3, false},
		{"postgres://localhost/db?pool_max_conns=3", "7", 7, false},
		{"postgres://localhost/db", "0", 0, true},
		{"postgres://localhost/db", "invalid", 0, true},
	} {
		t.Setenv("DB_MAX_CONNS", test.env)
		cfg, err := pgxpool.ParseConfig(test.url)
		if err != nil {
			t.Fatal(err)
		}
		err = ConfigurePool(cfg, 5)
		if (err != nil) != test.bad || (!test.bad && cfg.MaxConns != test.want) {
			t.Fatalf("%+v max=%d err=%v", test, cfg.MaxConns, err)
		}
	}
}

func TestInvalidHTTPBudgetsFailStartup(t *testing.T) {
	for _, key := range []string{"DRAIN_DELAY", "HTTP_READ_TIMEOUT", "HTTP_WRITE_TIMEOUT", "HTTP_REQUEST_TIMEOUT", "HTTP_FILE_TIMEOUT"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "-1s")
			if _, err := LoadConfig("test", ":8080"); err == nil {
				t.Fatal("invalid budget accepted")
			}
		})
	}
}

func TestChunkedOversizeBodyUsesPublicErrorEnvelope(t *testing.T) {
	cfg, err := LoadConfig("test", ":8080")
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxBodyBytes = 1024
	h := NewHandler(cfg, NewLogger("test"), func(mux *http.ServeMux) {
		mux.HandleFunc("POST /upload", func(w http.ResponseWriter, r *http.Request) {
			_, err := io.ReadAll(r.Body)
			if err != nil {
				WriteRequestError(w, r, err, "INVALID_REQUEST", "invalid request")
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	})
	r := httptest.NewRequest("POST", "/upload", strings.NewReader(strings.Repeat("a", 2048)))
	r.ContentLength = -1
	r.TransferEncoding = []string{"chunked"}
	r.Header.Set("X-Request-ID", "chunked-test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 413 || !strings.Contains(w.Body.String(), `"code":"BODY_TOO_LARGE"`) || !strings.Contains(w.Body.String(), `"request_id":"chunked-test"`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
