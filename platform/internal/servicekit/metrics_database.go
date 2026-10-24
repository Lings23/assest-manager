package servicekit

import (
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PoolSnapshot struct {
	MaxOpen, Open, InUse, Idle, Constructing      int32
	WaitCount, CanceledAcquireCount, AcquireCount int64
	WaitDuration, AcquireDuration                 time.Duration
}

func (s PoolSnapshot) write(w io.Writer, labels string) {
	for _, metric := range []struct {
		name  string
		value any
	}{
		{"max_open_connections", s.MaxOpen}, {"open_connections", s.Open}, {"in_use_connections", s.InUse}, {"idle_connections", s.Idle}, {"constructing_connections", s.Constructing},
		{"wait_total", s.WaitCount}, {"wait_seconds_total", s.WaitDuration.Seconds()}, {"canceled_acquires_total", s.CanceledAcquireCount}, {"acquires_total", s.AcquireCount}, {"acquire_seconds_total", s.AcquireDuration.Seconds()},
	} {
		fmt.Fprintf(w, "service_db_pool_%s{%s} %v\n", metric.name, labels, metric.value)
	}
}
func (m *Metrics) RegisterPool(name string, pool *pgxpool.Pool) {
	if m == nil {
		return
	}
	name = boundedComponent(name)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pools[name] = func() PoolSnapshot {
		s := pool.Stat()
		return PoolSnapshot{MaxOpen: s.MaxConns(), Open: s.TotalConns(), InUse: s.AcquiredConns(), Idle: s.IdleConns(), Constructing: s.ConstructingConns(), WaitCount: s.EmptyAcquireCount(), CanceledAcquireCount: s.CanceledAcquireCount(), AcquireCount: s.AcquireCount(), WaitDuration: s.EmptyAcquireWaitTime(), AcquireDuration: s.AcquireDuration()}
	}
}

type databaseTraceKey struct{}
type acquireTraceKey struct{}
type databaseTrace struct {
	started          time.Time
	operation, query string
}
type DatabaseTracer struct {
	metrics *Metrics
	pool    string
	logger  *slog.Logger
}

func NewDatabaseTracer(metrics *Metrics, pool string) *DatabaseTracer {
	return &DatabaseTracer{metrics: metrics, pool: boundedComponent(pool), logger: NewLogger(metrics.service)}
}
func sqlVerb(query string) string {
	fields := strings.Fields(query)
	if len(fields) == 0 {
		return "OTHER"
	}
	switch verb := strings.ToUpper(fields[0]); verb {
	case "SELECT", "INSERT", "UPDATE", "DELETE", "COPY", "BEGIN", "COMMIT", "ROLLBACK", "SAVEPOINT", "RELEASE", "WITH":
		return verb
	default:
		return "OTHER"
	}
}
func (t *DatabaseTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	operation, _ := ctx.Value(metricOperationContextKey{}).(string)
	if operation == "" {
		operation = sqlVerb(data.SQL)
	}
	return context.WithValue(ctx, databaseTraceKey{}, databaseTrace{started: time.Now(), operation: operation, query: data.SQL})
}
func (t *DatabaseTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	t.end(ctx, data.Err)
}
func (t *DatabaseTracer) end(ctx context.Context, err error) {
	trace, ok := ctx.Value(databaseTraceKey{}).(databaseTrace)
	if !ok {
		return
	}
	elapsed := time.Since(trace.started)
	t.metrics.databaseMetric(dbKey{pool: t.pool, route: metricRoute(ctx), operation: trace.operation}).observe(elapsed, err != nil, isTimeout(err))
	if elapsed >= 100*time.Millisecond {
		// Diagnostic checksum matches pg_stat_activity's md5(query). It is not
		// used for authentication, integrity or a high-cardinality metric label.
		fingerprint := md5.Sum([]byte(trace.query))
		t.logger.Warn("slow database operation", "operation", trace.operation, "query_fingerprint", fmt.Sprintf("%x", fingerprint), "duration_ms", float64(elapsed)/float64(time.Millisecond), "request_id", RequestID(ctx), "failed", err != nil)
	}
}
func (t *DatabaseTracer) TraceCopyFromStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceCopyFromStartData) context.Context {
	return context.WithValue(ctx, databaseTraceKey{}, databaseTrace{started: time.Now(), operation: "COPY"})
}
func (t *DatabaseTracer) TraceCopyFromEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceCopyFromEndData) {
	t.end(ctx, data.Err)
}
func (t *DatabaseTracer) TraceAcquireStart(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	return context.WithValue(ctx, acquireTraceKey{}, time.Now())
}
func (t *DatabaseTracer) TraceAcquireEnd(ctx context.Context, _ *pgxpool.Pool, data pgxpool.TraceAcquireEndData) {
	if started, ok := ctx.Value(acquireTraceKey{}).(time.Time); ok {
		t.metrics.databaseMetric(dbKey{pool: t.pool, route: metricRoute(ctx), operation: "ACQUIRE"}).observe(time.Since(started), data.Err != nil, isTimeout(data.Err))
	}
}
