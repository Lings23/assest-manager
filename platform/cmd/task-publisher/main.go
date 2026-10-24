package main

import (
	"asset-platform/internal/servicekit"
	"asset-platform/internal/taskcenter"
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	cfg, err := servicekit.LoadConfig("task-publisher", ":8085")
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	store, err := taskcenter.OpenStore(ctx, required("DATABASE_URL"), servicekit.MetricsFor(cfg.ServiceName))
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	broker, err := taskcenter.NewAMQPBroker(required("RABBITMQ_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer broker.Close()
	logger := servicekit.NewLogger("task-publisher")
	publisher := taskcenter.NewPublisher(store, broker)
	publisher.OnError = func(err error) { logger.Error("publish outbox", "publisher_id", publisher.ID, "error", err) }
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		err := publisher.Run(runCtx)
		done <- err
		if err != nil {
			cancel()
		}
	}()
	base := servicekit.NewHandler(cfg, logger, func(mux *http.ServeMux) {
		mux.Handle("GET /metrics", taskcenter.NewMetricsHandler(taskcenter.MetricsSources{Store: store, Broker: broker, Publisher: publisher}))
	})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/ready" {
			checkCtx, stopCheck := context.WithTimeout(r.Context(), 3*time.Second)
			defer stopCheck()
			if runCtx.Err() != nil || publisher.Ready(checkCtx) != nil {
				http.Error(w, "publisher unavailable", http.StatusServiceUnavailable)
				return
			}
		}
		base.ServeHTTP(w, r)
	})
	err = servicekit.Run(runCtx, cfg, logger, handler)
	cancel()
	runErr := <-done
	if err != nil || runErr != nil {
		logger.Error("task-publisher stopped", "http_error", err, "publisher_error", runErr)
		os.Exit(1)
	}
}
func required(key string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		log.Fatalf("%s is required", key)
	}
	return value
}
