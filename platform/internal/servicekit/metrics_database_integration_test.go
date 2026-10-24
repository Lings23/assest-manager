package servicekit

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresPoolMetricsKeepWaitAndQuerySemantics(t *testing.T) {
	dsn := os.Getenv("SERVICEKIT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SERVICEKIT_TEST_DATABASE_URL is not configured")
	}
	m := isolatedMetrics(t)
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 1 // Test-only contention. Production budgets are unchanged.
	tracer := NewDatabaseTracer(m, "primary")
	tracer.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	config.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	m.RegisterPool("primary", pool)
	held, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	_, err = pool.Acquire(ctx)
	cancel()
	if err == nil {
		held.Release()
		t.Fatal("blocked acquisition did not time out")
	}
	// A successful acquisition must actually wait for the held connection.
	acquired := make(chan error, 1)
	go func() {
		conn, err := pool.Acquire(t.Context())
		if err == nil {
			conn.Release()
		}
		acquired <- err
	}()
	time.Sleep(20 * time.Millisecond)
	held.Release()
	if err := <-acquired; err != nil {
		t.Fatal(err)
	}
	queryCtx := WithDatabaseOperation(t.Context(), "asset_list")
	if _, err := pool.Exec(queryCtx, "SELECT pg_sleep(0.02)"); err != nil {
		t.Fatal(err)
	}
	stat := pool.Stat()
	if stat.MaxConns() != 1 || stat.EmptyAcquireCount() < 1 || stat.EmptyAcquireWaitTime() <= 0 || stat.CanceledAcquireCount() != 1 {
		t.Fatalf("pool semantics=%+v", stat)
	}
	query := m.databaseMetric(dbKey{pool: "primary", route: "background", operation: "asset_list"})
	acquire := m.databaseMetric(dbKey{pool: "primary", route: "background", operation: "ACQUIRE"})
	if metricCount(query) != 1 || query.nanos.Load() < uint64(15*time.Millisecond) || metricCount(acquire) < 4 || acquire.errors.Load() != 1 {
		t.Fatal("pool acquisition and Conn query were not observed separately")
	}
	var out strings.Builder
	m.write(&out)
	if strings.Contains(out.String(), "pg_sleep") || !strings.Contains(out.String(), "service_db_pool_wait_seconds_total") {
		t.Fatal("query text leaked or wait metrics missing")
	}
}
