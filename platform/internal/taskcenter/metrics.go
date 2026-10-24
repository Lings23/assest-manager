package taskcenter

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// MetricsSources describes the components in one process. Database gauges are
// shared across replicas; process counters and unacked deliveries are local.
type MetricsSources struct {
	Store     *Store
	Broker    *AMQPBroker
	Publisher *Publisher
	Worker    *Worker
}

type databaseMetrics struct {
	pendingOutbox, deadLetters, quarantinedMessages, expiredExecutions int64
	oldestPendingAgeSeconds                                            float64
}

// Read all persistent gauges in one statement, using database time. Scheduled
// retries and claimed events remain pending until published; cutover-superseded
// events are excluded because they will never be published.
func (s *Store) readMetrics(ctx context.Context) (databaseMetrics, error) {
	var result databaseMetrics
	err := s.pool.QueryRow(ctx, `SELECT
	 (SELECT count(*) FROM task_report.outbox WHERE published_at IS NULL AND superseded_at IS NULL),
	 COALESCE((SELECT GREATEST(0, EXTRACT(EPOCH FROM (statement_timestamp()-MIN(created_at))))::double precision
	   FROM task_report.outbox WHERE published_at IS NULL AND superseded_at IS NULL),0),
	 (SELECT count(*) FROM task_report.dead_letters),
	 (SELECT count(*) FROM task_report.message_quarantine),
	 (SELECT count(*) FROM task_report.tasks WHERE dispatch_version>0 AND state='running' AND lease_until<=statement_timestamp())
	`).Scan(&result.pendingOutbox, &result.oldestPendingAgeSeconds, &result.deadLetters, &result.quarantinedMessages, &result.expiredExecutions)
	return result, err
}

// NewMetricsHandler exports Prometheus text format without contacting RabbitMQ's
// Management API. A failed database scrape returns 503 rather than zero gauges.
// Its request-scoped timeout also bounds acquisition of a busy pool connection.
func NewMetricsHandler(sources MetricsSources) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if sources.Store == nil || sources.Store.pool == nil {
			http.Error(w, "metrics database unavailable", http.StatusServiceUnavailable)
			return
		}
		database, err := sources.Store.readMetrics(ctx)
		if err != nil {
			http.Error(w, "metrics database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodHead {
			return
		}
		writeMetrics(w, database, sources)
	})
}

func writeMetrics(out io.Writer, database databaseMetrics, sources MetricsSources) {
	metric(out, "taskcenter_outbox_pending", "Unpublished Outbox events, including scheduled retries and active claims, excluding superseded events.", "gauge", database.pendingOutbox)
	metric(out, "taskcenter_outbox_oldest_pending_age_seconds", "Age since creation of the oldest unpublished, non-superseded Outbox event; zero when empty.", "gauge", database.oldestPendingAgeSeconds)
	metric(out, "taskcenter_dead_letters", "Durable task dead-letter records in the database.", "gauge", database.deadLetters)
	metric(out, "taskcenter_quarantined_messages", "Durable, fingerprint-deduplicated invalid message records in the database.", "gauge", database.quarantinedMessages)
	metric(out, "taskcenter_expired_executions", "Running dispatched tasks whose database lease has expired and awaits recovery.", "gauge", database.expiredExecutions)
	if b := sources.Broker; b != nil {
		metric(out, "taskcenter_amqp_published_total", "Routed messages successfully confirmed by RabbitMQ in this process.", "counter", b.publishConfirmed.Load())
		metric(out, "taskcenter_amqp_return_failures_total", "Mandatory publishes returned as unroutable in this process.", "counter", b.publishReturns.Load())
		metric(out, "taskcenter_amqp_nack_failures_total", "Publishes negatively acknowledged by RabbitMQ in this process.", "counter", b.publishNacks.Load())
		metric(out, "taskcenter_amqp_confirm_failures_total", "Publishes whose confirm is missing, invalid, interrupted, or timed out in this process.", "counter", b.confirmFailures.Load())
		metric(out, "taskcenter_amqp_transport_failures_total", "Publisher connection, channel initialization, or socket-write failures before waiting for a confirm in this process.", "counter", b.transportFailures.Load())
		metric(out, "taskcenter_amqp_unacked_deliveries", "Deliveries observed by this process awaiting successful ACK or NACK on live subscriptions.", "gauge", b.unackedDeliveries())
	}
	if p := sources.Publisher; p != nil {
		metric(out, "taskcenter_publisher_published_total", "Outbox events marked published after RabbitMQ confirmation by this publisher process.", "counter", p.published.Load())
		metric(out, "taskcenter_publisher_failures_total", "Publisher connection initialization, Outbox claim, publish, or finalization failures in this process.", "counter", p.failures.Load())
	}
	if w := sources.Worker; w != nil {
		metric(out, "taskcenter_worker_claim_conflicts_total", "Atomic execution claims rejected by a concurrent state or ownership change in this worker process.", "counter", w.claimConflicts.Load())
		metric(out, "taskcenter_worker_lease_expirations_total", "Expired executions successfully recovered by this worker process, including delivery-triggered recovery.", "counter", w.leaseExpirations.Load())
		metric(out, "taskcenter_worker_active_executions", "Claimed executions currently running or finalizing in this worker process.", "gauge", w.activeExecutions.Load())
		metricHeader(out, "taskcenter_worker_execution_duration_seconds", "Elapsed execution and finalization duration in this worker process, including abandoned executions.", "histogram")
		for index, kind := range []string{"import", "export"} {
			w.executionDurations[index].write(out, kind)
		}
	}
}

func metricHeader(out io.Writer, name, help, kind string) {
	_, _ = fmt.Fprintf(out, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
}

func metric(out io.Writer, name, help, kind string, value any) {
	metricHeader(out, name, help, kind)
	_, _ = fmt.Fprintf(out, "%s %v\n", name, value)
}

var executionDurationBuckets = [...]time.Duration{
	100 * time.Millisecond, 500 * time.Millisecond, time.Second, 5 * time.Second,
	10 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute,
	10 * time.Minute, 20 * time.Minute,
}

type executionDurationMetrics struct {
	buckets     [len(executionDurationBuckets) + 1]atomic.Uint64
	nanoseconds atomic.Uint64
}

func (m *executionDurationMetrics) observe(duration time.Duration) {
	index := len(executionDurationBuckets)
	for candidate, bound := range executionDurationBuckets {
		if duration <= bound {
			index = candidate
			break
		}
	}
	m.buckets[index].Add(1)
	m.nanoseconds.Add(uint64(max(duration, 0)))
}

func (m *executionDurationMetrics) write(out io.Writer, kind string) {
	var count uint64
	for index := range m.buckets {
		count += m.buckets[index].Load()
		bound := "+Inf"
		if index < len(executionDurationBuckets) {
			bound = strconv.FormatFloat(executionDurationBuckets[index].Seconds(), 'g', -1, 64)
		}
		_, _ = fmt.Fprintf(out, "taskcenter_worker_execution_duration_seconds_bucket{kind=%q,le=%q} %d\n", kind, bound, count)
	}
	_, _ = fmt.Fprintf(out, "taskcenter_worker_execution_duration_seconds_sum{kind=%q} %g\n", kind, float64(m.nanoseconds.Load())/float64(time.Second))
	_, _ = fmt.Fprintf(out, "taskcenter_worker_execution_duration_seconds_count{kind=%q} %d\n", kind, count)
}

func (b *AMQPBroker) unackedDeliveries() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	var count int64
	for subscription := range b.subscriptions {
		subscription.deliveryMu.Lock()
		count += subscription.unacked
		subscription.deliveryMu.Unlock()
	}
	return count
}

// A delivery counts until settlement succeeds or its channel is closed, when
// RabbitMQ requeues it. Both settlement functions share the same guard.
func (s *amqpSubscription) trackDelivery(ack func() error, nack func(bool) error) (func() error, func(bool) error, bool) {
	s.deliveryMu.Lock()
	if s.ctx.Err() != nil {
		s.deliveryMu.Unlock()
		return nil, nil, false
	}
	s.unacked++
	s.deliveryMu.Unlock()
	var settled sync.Once
	finish := func(err error) error {
		if err == nil {
			settled.Do(func() {
				s.deliveryMu.Lock()
				if s.unacked > 0 {
					s.unacked--
				}
				s.deliveryMu.Unlock()
			})
		}
		return err
	}
	return func() error { return finish(ack()) }, func(requeue bool) error { return finish(nack(requeue)) }, true
}
