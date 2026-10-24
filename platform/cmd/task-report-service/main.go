package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"asset-platform/internal/asset"
	"asset-platform/internal/servicekit"
	"asset-platform/internal/taskcenter"
)

func main() {
	migrateOnly := flag.Bool("migrate-only", false, "apply task schema migrations and exit")
	cutover := flag.Bool("cutover-old-tasks", false, "normalize old tasks after stopping every old worker")
	flag.Parse()
	if *cutover && !*migrateOnly {
		log.Fatal("--cutover-old-tasks requires --migrate-only")
	}
	if *migrateOnly {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := taskcenter.Migrate(ctx, required("DATABASE_URL"), *cutover); err != nil {
			log.Fatal(err)
		}
		log.Print("task migrations applied")
		return
	}
	cfg, err := servicekit.LoadConfig("task-report-service", ":8084")
	if err != nil {
		log.Fatal(err)
	}
	databaseURL := required("DATABASE_URL")
	internalToken := required("INTERNAL_SERVICE_TOKEN")
	authorizer, err := asset.NewIAMAuthorizer(env("IAM_SERVICE_URL", "http://iam-service:8081"), servicekit.MetricsFor(cfg.ServiceName))
	if err != nil {
		log.Fatal(err)
	}
	assets, err := taskcenter.NewHTTPAssetClient(env("ASSET_SERVICE_URL", "http://asset-service:8082"), internalToken, servicekit.MetricsFor(cfg.ServiceName))
	if err != nil {
		log.Fatal(err)
	}
	retentionDays, err := strconv.Atoi(env("S3_RETENTION_DAYS", "7"))
	if err != nil {
		log.Fatal("S3_RETENTION_DAYS must be an integer")
	}
	objects, err := taskcenter.NewS3Store(env("S3_ENDPOINT", "http://minio:9000"), required("MINIO_ROOT_USER"), required("MINIO_ROOT_PASSWORD"), env("S3_BUCKET", "asset-exports"), retentionDays, servicekit.MetricsFor(cfg.ServiceName))
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	store, err := taskcenter.OpenStore(ctx, databaseURL, servicekit.MetricsFor(cfg.ServiceName))
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	if err = objects.Ensure(ctx); err != nil {
		log.Fatal(err)
	}
	logger := servicekit.NewLogger("task-report-service")
	service := taskcenter.NewService(store, authorizer, assets, objects)
	api := taskcenter.NewHTTPHandler(service)
	handler := servicekit.NewHandler(cfg, logger, api.Register)
	baseHandler := handler
	client := &http.Client{Timeout: 2 * time.Second}
	handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/ready" {
			checkCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			ready := ctx.Err() == nil && store.Ping(checkCtx) == nil
			for _, endpoint := range []string{env("IAM_SERVICE_URL", "http://iam-service:8081") + "/health/ready", env("ASSET_SERVICE_URL", "http://asset-service:8082") + "/health/ready", env("S3_ENDPOINT", "http://minio:9000") + "/minio/health/ready"} {
				request, err := http.NewRequestWithContext(checkCtx, http.MethodGet, endpoint, nil)
				if err != nil {
					ready = false
					break
				}
				response, err := client.Do(request)
				if err != nil {
					ready = false
					break
				}
				response.Body.Close()
				if response.StatusCode != http.StatusOK {
					ready = false
					break
				}
			}
			if !ready {
				http.Error(w, "dependencies unavailable", http.StatusServiceUnavailable)
				return
			}
		}
		baseHandler.ServeHTTP(w, r)
	})
	if err := servicekit.Run(ctx, cfg, logger, handler); err != nil {
		logger.Error("task-report-service stopped", "error", err)
		os.Exit(1)
	}
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
func required(key string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		log.Fatalf("%s is required", key)
	}
	return value
}
