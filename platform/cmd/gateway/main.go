package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"asset-platform/internal/servicekit"
)

type upstreamConfig struct {
	IAM        string
	Asset      string
	Governance string
	TaskReport string
}

func main() {
	cfg, err := servicekit.LoadConfig("gateway", ":8080")
	if err != nil {
		log.Fatal(err)
	}
	upstreams := upstreamConfig{
		IAM:        env("IAM_SERVICE_URL", "http://iam-service:8081"),
		Asset:      env("ASSET_SERVICE_URL", "http://asset-service:8082"),
		Governance: env("GOVERNANCE_SERVICE_URL", "http://governance-service:8083"),
		TaskReport: env("TASK_REPORT_SERVICE_URL", "http://task-report-service:8084"),
	}
	logger := servicekit.NewLogger("gateway")
	handler, err := newGatewayHandler(cfg, upstreams, logger)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := servicekit.Run(ctx, cfg, logger, handler); err != nil {
		logger.Error("gateway stopped", "error", err)
		os.Exit(1)
	}
}

func newGatewayHandler(cfg servicekit.Config, upstreams upstreamConfig, logger *slog.Logger) (http.Handler, error) {
	iam, err := proxyFor("iam-service", upstreams.IAM, logger)
	if err != nil {
		return nil, err
	}
	asset, err := proxyFor("asset-service", upstreams.Asset, logger)
	if err != nil {
		return nil, err
	}
	governance, err := proxyFor("governance-service", upstreams.Governance, logger)
	if err != nil {
		return nil, err
	}
	taskReport, err := proxyFor("task-report-service", upstreams.TaskReport, logger)
	if err != nil {
		return nil, err
	}

	return servicekit.NewHandler(cfg, logger, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
			servicekit.WriteJSON(w, http.StatusOK, map[string]string{"service": "gateway", "status": "baseline-ready"})
		})
		registerProxy(mux, iam, "/api/v1/auth")
		registerProxy(mux, iam, "/api/v1/users")
		registerProxy(mux, iam, "/api/v1/departments")
		registerProxy(mux, asset, "/api/v1/asset-types")
		registerProxy(mux, asset, "/api/v1/assets")
		registerProxy(mux, governance, "/api/v1/workflows")
		registerProxy(mux, governance, "/api/v1/risks")
		registerProxy(mux, taskReport, "/api/v1/import-jobs")
		registerProxy(mux, taskReport, "/api/v1/export-jobs")
		registerProxy(mux, taskReport, "/api/v1/reporting")
	}), nil
}

func registerProxy(mux *http.ServeMux, handler http.Handler, prefix string) {
	mux.Handle(prefix, handler)
	mux.Handle(prefix+"/", handler)
}

func proxyFor(name, rawURL string, logger *slog.Logger) (*httputil.ReverseProxy, error) {
	target, err := url.Parse(rawURL)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		return nil, fmt.Errorf("invalid %s upstream URL %q", name, rawURL)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		// The gateway owns browser CORS. Internal services must not re-evaluate
		// or duplicate CORS headers for the already-approved external origin.
		r.Header.Del("Origin")
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, proxyErr error) {
		logger.Error("upstream request failed", "upstream", name, "request_id", servicekit.RequestID(r.Context()), "error", proxyErr)
		servicekit.WriteError(w, r, http.StatusBadGateway, "UPSTREAM_UNAVAILABLE", "upstream service unavailable")
	}
	return proxy, nil
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
