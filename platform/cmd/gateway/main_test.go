package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"asset-platform/internal/servicekit"
)

func TestGatewayRoutesAndPropagatesRequestID(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/me" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			t.Fatalf("external origin propagated upstream: %q", origin)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	cfg := servicekit.Config{
		ServiceName: "gateway", HTTPAddr: ":8080", ShutdownTimeout: time.Second, MaxBodyBytes: 1024,
		AllowedOrigins: []string{"http://127.0.0.1:8088"},
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler, err := newGatewayHandler(cfg, upstreamConfig{
		IAM: upstream.URL, Asset: upstream.URL, Governance: upstream.URL, TaskReport: upstream.URL,
	}, logger)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("X-Request-ID", "gateway-test-id")
	req.Header.Set("Origin", "http://127.0.0.1:8088")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Header().Get("X-Request-ID") != "gateway-test-id" {
		t.Fatalf("unexpected response: status=%d request_id=%q", res.Code, res.Header().Get("X-Request-ID"))
	}
	if !strings.Contains(res.Body.String(), `"ok":true`) {
		t.Fatalf("unexpected body: %s", res.Body.String())
	}
	if origins := res.Header().Values("Access-Control-Allow-Origin"); len(origins) != 1 || origins[0] != "http://127.0.0.1:8088" {
		t.Fatalf("unexpected CORS origins: %v", origins)
	}
}

func TestGatewayRejectsInvalidUpstream(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	if _, err := proxyFor("iam", "file:///tmp/socket", logger); err == nil {
		t.Fatal("invalid upstream URL accepted")
	}
}
