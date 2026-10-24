package servicekit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"
)

type contextKey string

const requestIDKey contextKey = "request_id"

// ErrorResponse is the public error envelope used by all new APIs.
type ErrorResponse struct {
	Error APIError `json:"error"`
}

type APIError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

type statusResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
}

func NewLogger(service string) *slog.Logger {
	return slog.New(slog.NewJSONHandler(logWriter{}, &slog.HandlerOptions{})).With("service", service, "instance", os.Getenv("HOSTNAME"), "version", envOrDefault("APP_VERSION", "dev"))
}

// logWriter keeps slog output on stderr without sharing mutable global logger state.
type logWriter struct{}

func (logWriter) Write(p []byte) (int, error) { return writeStderr(p) }

// NewHandler builds the common health endpoints and HTTP middleware chain.
func NewHandler(cfg Config, logger *slog.Logger, register func(*http.ServeMux)) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, statusResponse{Status: "ok", Service: cfg.ServiceName})
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		if !cfg.Readiness.Ready(r.Context()) {
			WriteError(w, r, http.StatusServiceUnavailable, "NOT_READY", "service is not ready")
			return
		}
		writeJSON(w, http.StatusOK, statusResponse{Status: "ready", Service: cfg.ServiceName})
	})
	mux.Handle("GET /metrics/service", MetricsFor(cfg.ServiceName).Handler(cfg.MetricsToken))
	register(mux)

	instrumented := MetricsFor(cfg.ServiceName).InstrumentHTTP(mux, requestIDMiddleware(
		corsMiddleware(cfg.AllowedOrigins,
			securityHeadersMiddleware(
				requestBudgetMiddleware(cfg, bodyLimitMiddleware(cfg.MaxBodyBytes,
					loggingMiddleware(logger,
						recoveryMiddleware(logger, routingHandler{mux: mux}))))))))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Instance-ID", cfg.InstanceID)
		instrumented.ServeHTTP(w, r)
	})
}

type routingHandler struct{ mux *http.ServeMux }

func (h routingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, pattern := h.mux.Handler(r)
	if pattern == "" {
		WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
		return
	}
	// ServeMux.ServeHTTP performs the final match and populates PathValue for
	// wildcard routes. Calling the Handler result directly loses {type}/{id}.
	h.mux.ServeHTTP(w, r)
}

func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, status, ErrorResponse{Error: APIError{
		Code: code, Message: message, RequestID: RequestID(r.Context()),
	}})
}

func WriteJSON(w http.ResponseWriter, status int, value any) { writeJSON(w, status, value) }

func WriteRequestError(w http.ResponseWriter, r *http.Request, err error, code, message string) {
	var oversized *http.MaxBytesError
	if errors.As(err, &oversized) {
		WriteError(w, r, http.StatusRequestEntityTooLarge, "BODY_TOO_LARGE", "request body too large")
		return
	}
	WriteError(w, r, http.StatusBadRequest, code, message)
}

func requestBudgetMiddleware(cfg Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		budget := cfg.RequestTimeout
		if (r.Method == http.MethodPost && r.URL.Path == "/api/v1/import-jobs") || strings.HasSuffix(r.URL.Path, "/download") || strings.HasSuffix(r.URL.Path, "/errors") {
			budget = cfg.FileTimeout
		}
		if budget <= 0 {
			next.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), budget)
		defer cancel()
		deadline := time.Now().Add(budget)
		controller := http.NewResponseController(w)
		_ = controller.SetReadDeadline(deadline)
		_ = controller.SetWriteDeadline(deadline)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RequestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if requestID == "" || len(requestID) > 128 {
			requestID = newRequestID()
		}
		r.Header.Set("X-Request-ID", requestID)
		w.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, requestID)))
	})
}

func corsMiddleware(allowedOrigins []string, next http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[origin] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		if _, ok := allowed[origin]; !ok {
			WriteError(w, r, http.StatusForbidden, "ORIGIN_NOT_ALLOWED", "request origin is not allowed")
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Add("Vary", "Origin")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		next.ServeHTTP(w, r)
	})
}

func bodyLimitMiddleware(limit int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > limit {
			WriteError(w, r, http.StatusRequestEntityTooLarge, "BODY_TOO_LARGE", "request body too large")
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}

func recoveryMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("panic recovered", "request_id", RequestID(r.Context()), "panic", recovered, "stack", string(debug.Stack()))
				WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

// Unwrap lets net/http.ResponseController retain streaming and proxy features.
func (w *responseRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *responseRecorder) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func loggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		logger.Info("http request", "request_id", RequestID(r.Context()), "method", r.Method,
			"path", r.URL.Path, "status", recorder.status, "duration_ms", time.Since(started).Milliseconds())
	})
}

func newRequestID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return time.Now().UTC().Format("20060102T150405.000000000")
	}
	return hex.EncodeToString(value)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
