package taskcenter

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
)

func TestPostgresMetricsReflectPendingOutboxDeadLettersAndQuarantine(t *testing.T) {
	store := executionTestStore(t)
	before, err := store.readMetrics(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	events := insertTestOutbox(t, store, 4)
	// A delayed retry and a claimed publish both remain pending. Earlier
	// superseded/published events must not distort the oldest-pending age.
	if _, err = store.pool.Exec(t.Context(), `UPDATE task_report.outbox SET
	 created_at=CASE WHEN id=$1 THEN statement_timestamp()-interval '2 hours' ELSE '1900-01-01'::timestamptz END,
	 available_at=CASE WHEN id=$1 THEN statement_timestamp()+interval '1 hour' ELSE available_at END,
	 superseded_at=CASE WHEN id=$2 THEN statement_timestamp() END,
	 published_at=CASE WHEN id=$3 THEN statement_timestamp() END,
	 claimed_by=CASE WHEN id=$4 THEN 'metrics-test' END,
	 claim_token=CASE WHEN id=$4 THEN $5::uuid END,
	 claim_until=CASE WHEN id=$4 THEN statement_timestamp()+interval '1 minute' END
	 WHERE id=ANY($6::bigint[])`, events[0].ID, events[1].ID, events[2].ID, events[3].ID, uuid(), []int64{events[0].ID, events[1].ID, events[2].ID, events[3].ID}); err != nil {
		t.Fatal(err)
	}
	// Make the active claim newer than the scheduled event.
	if _, err = store.pool.Exec(t.Context(), `UPDATE task_report.outbox SET created_at=statement_timestamp()-interval '1 hour' WHERE id=$1`, events[3].ID); err != nil {
		t.Fatal(err)
	}
	expiredTask := executionTestTask(t, store, "export")
	execution, err := store.ClaimTask(t.Context(), expiredTask.ID, 1, "metrics-test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	expireExecution(t, store, execution)
	deadTask := executionTestTask(t, store, "import")
	if _, err = store.pool.Exec(t.Context(), `INSERT INTO task_report.dead_letters(task_id,payload,reason) VALUES($1,'{}','metrics-test')`, deadTask.ID); err != nil {
		t.Fatal(err)
	}
	quarantineBody := []byte("metrics-test-invalid-" + uuid())
	metadata := map[string]any{"message_id": uuid()}
	if err = store.Quarantine(t.Context(), quarantineBody, metadata, "metrics-test"); err != nil {
		t.Fatal(err)
	}
	var quarantineID string
	if err = store.pool.QueryRow(t.Context(), `SELECT id FROM task_report.message_quarantine WHERE body=$1`, string(quarantineBody)).Scan(&quarantineID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM task_report.outbox WHERE aggregate_id=$1`, quarantineID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM task_report.message_quarantine WHERE id=$1`, quarantineID)
	})
	// Redelivery has the same fingerprint and must not increment durable gauges.
	metadata["redelivered"] = true
	if err = store.Quarantine(t.Context(), quarantineBody, metadata, "metrics-test"); err != nil {
		t.Fatal(err)
	}
	handler := NewMetricsHandler(MetricsSources{Store: store})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("scrape status=%d, headers=%v, body=%s", response.Code, response.Header(), response.Body)
	}
	for name, want := range map[string]float64{
		"taskcenter_outbox_pending":       float64(before.pendingOutbox + 4),
		"taskcenter_dead_letters":         float64(before.deadLetters + 1),
		"taskcenter_quarantined_messages": float64(before.quarantinedMessages + 1),
		"taskcenter_expired_executions":   float64(before.expiredExecutions + 1),
	} {
		if got := metricSample(t, response.Body.String(), name); got != want {
			t.Errorf("%s=%g, want %g", name, got, want)
		}
	}
	wantAge := math.Max(before.oldestPendingAgeSeconds, 7200)
	if age := metricSample(t, response.Body.String(), "taskcenter_outbox_oldest_pending_age_seconds"); math.Abs(age-wantAge) > 5 {
		t.Errorf("oldest pending age=%g, want approximately %g; superseded/published events must be ignored", age, wantAge)
	}
	head := httptest.NewRecorder()
	handler.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/metrics", nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Fatal("HEAD metrics did not preserve GET status without a body")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	unavailable := httptest.NewRecorder()
	handler.ServeHTTP(unavailable, httptest.NewRequest(http.MethodGet, "/metrics", nil).WithContext(cancelled))
	if unavailable.Code != http.StatusServiceUnavailable || unavailable.Body.String() != "metrics database unavailable\n" {
		t.Fatal("cancelled database scrape exposed credentials or false zero gauges")
	}
}

func TestAMQPMetricsCountReturnsAndUnackedRedelivery(t *testing.T) {
	broker, channel := amqpTestBroker(t)
	store := executionTestStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	handler := NewMetricsHandler(MetricsSources{Store: store, Broker: broker})
	scrape := func(name string) float64 {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("metrics scrape: %d %s", response.Code, response.Body)
		}
		return metricSample(t, response.Body.String(), name)
	}
	if err := channel.QueueUnbind(broker.queue, "task.requested", broker.exchange, nil); err != nil {
		t.Fatal(err)
	}
	if err := broker.Publish(ctx, testBrokerEvent()); !errors.Is(err, ErrPublishReturned) {
		t.Fatal(err)
	}
	if got := scrape("taskcenter_amqp_return_failures_total"); got != 1 {
		t.Fatalf("mandatory return counter=%g", got)
	}
	if got := scrape("taskcenter_amqp_published_total"); got != 0 {
		t.Fatal("unroutable publish counted as confirmed routed delivery")
	}
	if err := channel.QueueBind(broker.queue, "task.requested", broker.exchange, false, nil); err != nil {
		t.Fatal(err)
	}
	first, err := broker.Subscribe(ctx, "metrics-first-"+uuid())
	if err != nil {
		t.Fatal(err)
	}
	event := testBrokerEvent()
	if err := broker.Publish(ctx, event); err != nil {
		t.Fatal(err)
	}
	delivery := receiveDelivery(t, first)
	if delivery.MessageID != event.EventID || scrape("taskcenter_amqp_unacked_deliveries") != 1 {
		t.Fatal("received unacked delivery was not counted")
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	if got := scrape("taskcenter_amqp_unacked_deliveries"); got != 0 {
		t.Fatalf("closed channel retained local unacked gauge=%g", got)
	}
	second, err := broker.Subscribe(ctx, "metrics-second-"+uuid())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	delivery = receiveDelivery(t, second)
	if !delivery.Redelivered || delivery.MessageID != event.EventID || scrape("taskcenter_amqp_unacked_deliveries") != 1 {
		t.Fatal("redelivered event was not counted on its new channel")
	}
	if err := delivery.Ack(); err != nil {
		t.Fatal(err)
	}
	if got := scrape("taskcenter_amqp_unacked_deliveries"); got != 0 {
		t.Fatalf("successful ACK retained local unacked gauge=%g", got)
	}
	if got := scrape("taskcenter_amqp_published_total"); got != 1 {
		t.Fatalf("confirmed routed publish counter=%g", got)
	}
}

func TestPostgresMetricsHandlerBoundsBusyPoolAndHonorsRequestDeadline(t *testing.T) {
	store := executionTestStore(t)
	config := store.pool.Config()
	config.MaxConns, config.MinConns = 1, 0
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	connection, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	handler := NewMetricsHandler(MetricsSources{Store: &Store{pool: pool}})
	for _, test := range []struct {
		name    string
		timeout time.Duration
		minimum time.Duration
		maximum time.Duration
	}{
		{"handler deadline", 10 * time.Second, 2500 * time.Millisecond, 5 * time.Second},
		{"request deadline", 50 * time.Millisecond, 0, time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			requestCtx, cancel := context.WithTimeout(t.Context(), test.timeout)
			defer cancel()
			response := httptest.NewRecorder()
			started := time.Now()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil).WithContext(requestCtx))
			elapsed := time.Since(started)
			if response.Code != http.StatusServiceUnavailable || elapsed < test.minimum || elapsed > test.maximum {
				t.Fatalf("busy pool scrape status=%d, elapsed=%s", response.Code, elapsed)
			}
		})
	}
	connection.Release()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("pool recovery scrape=%d %s", response.Code, response.Body)
	}
}

func TestAMQPMetricsCountRealBrokerNack(t *testing.T) {
	broker, channel := amqpTestBroker(t)
	store := executionTestStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	queue := broker.queue + ".metrics-reject-overflow"
	if _, err := channel.QueueDeclare(queue, true, false, false, false, amqp.Table{"x-max-length": int64(1), "x-overflow": "reject-publish"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = channel.QueueDelete(queue, false, false, false) })
	if err := channel.QueueUnbind(broker.queue, "task.requested", broker.exchange, nil); err != nil {
		t.Fatal(err)
	}
	if err := channel.QueueBind(queue, "task.requested", broker.exchange, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := broker.Publish(ctx, testBrokerEvent()); err != nil {
		t.Fatal(err)
	}
	if err := broker.Publish(ctx, testBrokerEvent()); !errors.Is(err, ErrPublishNack) {
		t.Fatalf("overflow publish=%v", err)
	}
	response := httptest.NewRecorder()
	NewMetricsHandler(MetricsSources{Store: store, Broker: broker}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("metrics scrape=%d %s", response.Code, response.Body)
	}
	if got := metricSample(t, response.Body.String(), "taskcenter_amqp_nack_failures_total"); got != 1 {
		t.Fatalf("real broker NACK counter=%g", got)
	}
	if got := metricSample(t, response.Body.String(), "taskcenter_amqp_published_total"); got != 1 {
		t.Fatalf("rejected publish affected confirmed counter=%g", got)
	}
}
