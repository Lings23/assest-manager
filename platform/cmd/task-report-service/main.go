package main

import (
	"context"
	"flag"
	"log"
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
	initObjects := flag.Bool("init-objects", false, "initialize object storage with migrations")
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
		if *initObjects {
			days, err := strconv.Atoi(env("S3_RETENTION_DAYS", "7"))
			if err != nil {
				log.Fatal(err)
			}
			objects, err := taskcenter.NewS3Store(env("S3_ENDPOINT", "http://minio:9000"), required("MINIO_ROOT_USER"), required("MINIO_ROOT_PASSWORD"), env("S3_BUCKET", "asset-exports"), days)
			if err != nil {
				log.Fatal(err)
			}
			if err = objects.Ensure(ctx); err != nil {
				log.Fatal(err)
			}
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
	logger := servicekit.NewLogger("task-report-service")
	service := taskcenter.NewService(store, authorizer, assets, objects)
	api := taskcenter.NewHTTPHandler(service)
	cfg.Readiness = servicekit.NewReadiness(store.Ping)
	handler := servicekit.NewHandler(cfg, logger, api.Register)
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
