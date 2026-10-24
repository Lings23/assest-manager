package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"asset-platform/internal/servicekit"
	"asset-platform/internal/taskcenter"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	cfg, err := servicekit.LoadConfig("task-worker", ":8086")
	if err != nil {
		return err
	}
	workerCfg := taskcenter.DefaultWorkerConfig()
	if raw := strings.TrimSpace(os.Getenv("WORKER_SLOTS")); raw != "" {
		workerCfg.Slots, err = strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("WORKER_SLOTS must be an integer")
		}
	}
	for key, target := range map[string]*time.Duration{"WORKER_LEASE_DURATION": &workerCfg.LeaseDuration, "WORKER_RENEW_INTERVAL": &workerCfg.RenewInterval, "WORKER_TASK_TIMEOUT": &workerCfg.TaskTimeout, "WORKER_RECOVERY_INTERVAL": &workerCfg.RecoveryInterval, "WORKER_DRAIN_TIMEOUT": &workerCfg.DrainTimeout} {
		if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
			*target, err = time.ParseDuration(raw)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
		}
	}
	if raw := strings.TrimSpace(os.Getenv("WORKER_ALLOW_LEGACY_MESSAGES")); raw != "" {
		workerCfg.AllowLegacyMessages, err = strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("WORKER_ALLOW_LEGACY_MESSAGES must be a boolean")
		}
	}
	consumerTimeout, err := time.ParseDuration(env("RABBITMQ_CONSUMER_TIMEOUT", "30m"))
	if err != nil {
		return fmt.Errorf("RABBITMQ_CONSUMER_TIMEOUT: %w", err)
	}
	if consumerTimeout < time.Minute || workerCfg.TaskTimeout > consumerTimeout-time.Minute {
		return fmt.Errorf("RABBITMQ_CONSUMER_TIMEOUT must exceed WORKER_TASK_TIMEOUT by at least 1m and match the broker configuration")
	}
	databaseURL, err := required("DATABASE_URL")
	if err != nil {
		return err
	}
	rabbitURL, err := required("RABBITMQ_URL")
	if err != nil {
		return err
	}
	token, err := required("INTERNAL_SERVICE_TOKEN")
	if err != nil {
		return err
	}
	access, err := required("MINIO_ROOT_USER")
	if err != nil {
		return err
	}
	secret, err := required("MINIO_ROOT_PASSWORD")
	if err != nil {
		return err
	}
	retention, err := strconv.Atoi(env("S3_RETENTION_DAYS", "7"))
	if err != nil {
		return fmt.Errorf("S3_RETENTION_DAYS must be an integer")
	}
	assetsURL := env("ASSET_SERVICE_URL", "http://asset-service:8082")
	objectsURL := env("S3_ENDPOINT", "http://minio:9000")
	assets, err := taskcenter.NewHTTPAssetClient(assetsURL, token, servicekit.MetricsFor(cfg.ServiceName))
	if err != nil {
		return err
	}
	objects, err := taskcenter.NewS3Store(objectsURL, access, secret, env("S3_BUCKET", "asset-exports"), retention, servicekit.MetricsFor(cfg.ServiceName))
	if err != nil {
		return err
	}
	broker, err := taskcenter.NewAMQPBroker(rabbitURL)
	if err != nil {
		return err
	}
	defer broker.Close()
	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	store, err := taskcenter.OpenStore(signalCtx, databaseURL, servicekit.MetricsFor(cfg.ServiceName))
	if err != nil {
		return err
	}
	defer store.Close()
	if err = objects.Ensure(signalCtx); err != nil {
		return err
	}
	logger := servicekit.NewLogger("task-worker")
	worker, err := taskcenter.NewWorkerWithConfig(store, broker, assets, objects, logger, workerCfg)
	if err != nil {
		return err
	}
	healthClient := &http.Client{Timeout: 3 * time.Second}
	stopDiagnostics, err := servicekit.StartDiagnostics(signalCtx, cfg, logger)
	if err != nil {
		return err
	}
	defer stopDiagnostics()
	mux := http.NewServeMux()
	mux.Handle("GET /metrics/service", servicekit.MetricsFor(cfg.ServiceName).Handler(cfg.MetricsToken))
	mux.Handle("GET /metrics", taskcenter.NewMetricsHandler(taskcenter.MetricsSources{Store: store, Broker: broker, Worker: worker}))
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		servicekit.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "task-worker"})
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		checkCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if signalCtx.Err() != nil || !worker.Ready() || store.Ping(checkCtx) != nil || checkHealth(checkCtx, healthClient, assetsURL+"/health/ready") != nil || checkHealth(checkCtx, healthClient, objectsURL+"/minio/health/ready") != nil {
			servicekit.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "service": "task-worker"})
			return
		}
		servicekit.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready", "service": "task-worker"})
	})
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: servicekit.MetricsFor(cfg.ServiceName).InstrumentHTTP(mux, mux), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.ListenAndServe() }()
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); worker.Run(signalCtx) }()
	logger.Info("task worker listening", "addr", cfg.HTTPAddr, "slots", workerCfg.Slots)
	select {
	case <-signalCtx.Done():
	case err = <-serverDone:
		stop()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	}
	// Keep readiness reachable (and false) throughout drain; close database only
	// after all execution, lease renewal, and recovery goroutines have returned.
	<-workerDone
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	if err != nil {
		return err
	}
	return shutdownErr
}
func checkHealth(ctx context.Context, client *http.Client, address string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(address, "/"), nil)
	if err != nil {
		return err
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("dependency not ready")
	}
	return nil
}
func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
func required(key string) (string, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}
