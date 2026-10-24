package taskcenter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"asset-platform/internal/asset"
)

func metricSample(t *testing.T, body, name string) float64 {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, name+" ") {
			value, err := strconv.ParseFloat(strings.TrimPrefix(line, name+" "), 64)
			if err != nil {
				t.Fatalf("invalid metric %s: %v", name, err)
			}
			return value
		}
	}
	t.Fatalf("metric %s is missing from:\n%s", name, body)
	return 0
}

func TestMetricsHistogramUsesCumulativeBuckets(t *testing.T) {
	var worker Worker
	worker.executionDurations[0].observe(500 * time.Millisecond)
	worker.executionDurations[0].observe(1500 * time.Millisecond)
	var body bytes.Buffer
	writeMetrics(&body, databaseMetrics{}, MetricsSources{Worker: &worker})
	for name, want := range map[string]float64{
		`taskcenter_worker_execution_duration_seconds_bucket{kind="import",le="0.1"}`:  0,
		`taskcenter_worker_execution_duration_seconds_bucket{kind="import",le="0.5"}`:  1,
		`taskcenter_worker_execution_duration_seconds_bucket{kind="import",le="1"}`:    1,
		`taskcenter_worker_execution_duration_seconds_bucket{kind="import",le="5"}`:    2,
		`taskcenter_worker_execution_duration_seconds_bucket{kind="import",le="+Inf"}`: 2,
		`taskcenter_worker_execution_duration_seconds_count{kind="import"}`:            2,
		`taskcenter_worker_execution_duration_seconds_sum{kind="import"}`:              2,
		`taskcenter_worker_execution_duration_seconds_count{kind="export"}`:            0,
	} {
		if got := metricSample(t, body.String(), name); got != want {
			t.Errorf("%s=%g, want %g", name, got, want)
		}
	}
	if !strings.Contains(body.String(), "# TYPE taskcenter_worker_execution_duration_seconds histogram\n") {
		t.Fatal("missing Prometheus histogram type")
	}
}

func TestMetricsUnackedRequiresSuccessfulSettlement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subscription := &amqpSubscription{ctx: ctx}
	broker := &AMQPBroker{subscriptions: map[*amqpSubscription]struct{}{subscription: {}}}
	ackFailure := errors.New("channel closed before ack")
	ack, nack, tracked := subscription.trackDelivery(func() error { return ackFailure }, func(bool) error { return nil })
	if !tracked || broker.unackedDeliveries() != 1 {
		t.Fatal("delivery was not tracked before worker receives it")
	}
	if err := ack(); !errors.Is(err, ackFailure) || broker.unackedDeliveries() != 1 {
		t.Fatal("failed ACK removed the unacked delivery")
	}
	if err := nack(true); err != nil || broker.unackedDeliveries() != 0 {
		t.Fatal("successful NACK did not settle the delivery")
	}
	_, _, _ = subscription.trackDelivery(func() error { return nil }, func(bool) error { return nil })
	if err := nack(false); err != nil || broker.unackedDeliveries() != 1 {
		t.Fatal("duplicate settlement removed another delivery")
	}
	cancel()
	if _, _, tracked := subscription.trackDelivery(func() error { return nil }, func(bool) error { return nil }); tracked {
		t.Fatal("cancelled subscription continued tracking deliveries")
	}
}

func TestMetricsPublisherAndWorkerCountActualOutcomes(t *testing.T) {
	failedStore := &publisherTestStore{event: OutboxEvent{EventID: uuid(), ClaimToken: uuid()}}
	failedPublisher := NewPublisher(failedStore, publisherTestBroker{publishError: ErrPublishReturned})
	if _, err := failedPublisher.PublishNext(t.Context()); !errors.Is(err, ErrPublishReturned) {
		t.Fatal(err)
	}
	if failedPublisher.failures.Load() != 1 || failedPublisher.published.Load() != 0 {
		t.Fatal("unroutable event counted as published")
	}
	confirmedStore := &publisherTestStore{event: OutboxEvent{EventID: uuid(), ClaimToken: uuid()}}
	confirmedPublisher := NewPublisher(confirmedStore, publisherTestBroker{})
	if _, err := confirmedPublisher.PublishNext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if confirmedPublisher.published.Load() != 1 || confirmedPublisher.failures.Load() != 0 {
		t.Fatal("confirmed and durably marked event was not counted")
	}
	store := newWorkerMemoryStore()
	store.claimErr = ErrConflict
	worker := testWorker(store, workerAssets{})
	if action := worker.process(t.Context(), workerDelivery("export", 1)); action.ack || !errors.Is(action.err, ErrConflict) {
		t.Fatalf("unexpected conflict action: %#v", action)
	}
	if worker.claimConflicts.Load() != uint64(store.claims.Load()) || worker.claimConflicts.Load() == 0 {
		t.Fatal("conditional claim failures were not counted")
	}
	store = newWorkerMemoryStore()
	started, release := make(chan struct{}), make(chan struct{})
	worker = testWorker(store, workerAssets{export: func(ctx context.Context) ([]asset.Record, error) {
		close(started)
		select {
		case <-release:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}})
	finished := make(chan deliveryAction, 1)
	go func() { finished <- worker.process(t.Context(), workerDelivery("export", 1)) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("execution did not start")
	}
	if worker.activeExecutions.Load() != 1 {
		t.Error("active execution gauge did not count the running task")
	}
	close(release)
	if action := <-finished; !action.ack {
		t.Fatal(action.err)
	}
	if worker.activeExecutions.Load() != 0 {
		t.Fatal("active execution gauge did not clear after finalization")
	}
	var body bytes.Buffer
	writeMetrics(&body, databaseMetrics{}, MetricsSources{Worker: worker})
	if count := metricSample(t, body.String(), `taskcenter_worker_execution_duration_seconds_count{kind="export"}`); count != 1 {
		t.Fatalf("executed export histogram count=%g", count)
	}
}

type metricsRecoveryStore struct {
	workerMemoryStore
	count  int
	err    error
	cancel context.CancelFunc
}

func (s *metricsRecoveryStore) RecoverExpired(context.Context, int) (int, error) {
	s.cancel()
	return s.count, s.err
}

func TestMetricsLeaseRecoveryCountsOnlyCommittedResults(t *testing.T) {
	for _, recoveryError := range []error{nil, errors.New("commit outcome unknown")} {
		ctx, cancel := context.WithCancel(context.Background())
		store := &metricsRecoveryStore{count: 2, err: recoveryError, cancel: cancel}
		worker := NewWorker(store, nil, nil, nil, nil)
		worker.runRecovery(ctx)
		want := uint64(2)
		if recoveryError != nil {
			want = 0
		}
		if got := worker.leaseExpirations.Load(); got != want {
			t.Fatalf("error=%v: recovered count=%d, want=%d", recoveryError, got, want)
		}
	}
	store := newWorkerMemoryStore()
	store.task.State = Running
	expired := store.task.ObservedAt.Add(-time.Second)
	store.task.LeaseUntil = &expired
	worker := testWorker(store, workerAssets{})
	if action := worker.process(t.Context(), workerDelivery("export", 1)); !action.ack {
		t.Fatal(action.err)
	}
	if worker.leaseExpirations.Load() != 1 {
		t.Fatal("delivery-triggered recovery was not counted")
	}
}

func TestMetricsConcurrentConfirmationCounters(t *testing.T) {
	var broker AMQPBroker
	var group sync.WaitGroup
	for range 20 {
		group.Add(1)
		go func() {
			defer group.Done()
			broker.recordConfirmationFailure(fmt.Errorf("event: %w", ErrPublishReturned))
			broker.recordConfirmationFailure(ErrPublishNack)
			broker.recordConfirmationFailure(context.DeadlineExceeded)
		}()
	}
	group.Wait()
	if broker.publishReturns.Load() != 20 || broker.publishNacks.Load() != 20 || broker.confirmFailures.Load() != 20 {
		t.Fatal("confirmation failure categories lost concurrent observations")
	}
}

func TestMetricsHandlerMissingDatabaseAndMethods(t *testing.T) {
	handler := NewMetricsHandler(MetricsSources{})
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, "/metrics", nil))
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s accepted an unavailable database: %d", method, response.Code)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/metrics", nil))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET, HEAD" {
		t.Fatal("metrics handler accepted a mutating request")
	}
}
