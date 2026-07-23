package servicekit

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestConfigValidation(t *testing.T) {
	valid := Config{ServiceName: "test", HTTPAddr: ":8080", ShutdownTimeout: time.Second, MaxBodyBytes: 1024}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	invalid := valid
	invalid.HTTPAddr = "8080"
	if err := invalid.Validate(); err == nil {
		t.Fatal("invalid address accepted")
	}
	invalid = valid
	invalid.AllowedOrigins = []string{"javascript:alert(1)"}
	if err := invalid.Validate(); err == nil {
		t.Fatal("invalid CORS origin accepted")
	}
}

func TestHealthAndRequestID(t *testing.T) {
	cfg := Config{ServiceName: "test", HTTPAddr: ":8080", ShutdownTimeout: time.Second, MaxBodyBytes: 1024}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewHandler(cfg, logger, func(*http.ServeMux) {})
	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	req.Header.Set("X-Request-ID", "known-request-id")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Header().Get("X-Request-ID") != "known-request-id" {
		t.Fatalf("unexpected health response: status=%d request_id=%q", res.Code, res.Header().Get("X-Request-ID"))
	}
	if !strings.Contains(res.Body.String(), `"service":"test"`) {
		t.Fatalf("unexpected body: %s", res.Body.String())
	}
}

func TestErrorEnvelopeIncludesRequestID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), requestIDKey, "error-request-id"))
	res := httptest.NewRecorder()
	WriteError(res, req, http.StatusBadRequest, "BAD_REQUEST", "bad request")
	if !strings.Contains(res.Body.String(), `"request_id":"error-request-id"`) {
		t.Fatalf("request id missing from response: %s", res.Body.String())
	}
}

func TestNotFoundUsesStandardError(t *testing.T) {
	cfg := Config{ServiceName: "test", HTTPAddr: ":8080", ShutdownTimeout: time.Second, MaxBodyBytes: 1024}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewHandler(cfg, logger, func(*http.ServeMux) {})
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if res.Code != http.StatusNotFound || !strings.Contains(res.Body.String(), `"code":"NOT_FOUND"`) {
		t.Fatalf("unexpected response: status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestCORSUsesExactAllowlist(t *testing.T) {
	cfg := Config{ServiceName: "test", HTTPAddr: ":8080", ShutdownTimeout: time.Second, MaxBodyBytes: 1024, AllowedOrigins: []string{"https://console.example"}}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewHandler(cfg, logger, func(*http.ServeMux) {})

	allowed := httptest.NewRequest(http.MethodOptions, "/health/ready", nil)
	allowed.Header.Set("Origin", "https://console.example")
	allowedRes := httptest.NewRecorder()
	handler.ServeHTTP(allowedRes, allowed)
	if allowedRes.Code != http.StatusNoContent || allowedRes.Header().Get("Access-Control-Allow-Origin") != "https://console.example" {
		t.Fatalf("allowed origin rejected: status=%d", allowedRes.Code)
	}

	denied := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	denied.Header.Set("Origin", "https://evil.example")
	deniedRes := httptest.NewRecorder()
	handler.ServeHTTP(deniedRes, denied)
	if deniedRes.Code != http.StatusForbidden || !strings.Contains(deniedRes.Body.String(), "ORIGIN_NOT_ALLOWED") {
		t.Fatalf("denied origin accepted: status=%d body=%s", deniedRes.Code, deniedRes.Body.String())
	}
}
