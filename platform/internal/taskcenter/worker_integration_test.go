package taskcenter

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"asset-platform/internal/asset"
)

func workerIntegrationDependencies(t *testing.T) (*Store, *AMQPBroker) {
	t.Helper()
	if os.Getenv("TASK_TEST_DATABASE_URL") == "" || os.Getenv("TASKCENTER_AMQP_TEST_URL") == "" {
		t.Skip("TASK_TEST_DATABASE_URL and TASKCENTER_AMQP_TEST_URL are required")
	}
	store := executionTestStore(t)
	broker, _ := amqpTestBroker(t)
	return store, broker
}

func workerRequestedEvent(t *testing.T, store *Store, task Task) OutboxEvent {
	t.Helper()
	var event OutboxEvent
	err := store.pool.QueryRow(t.Context(), `SELECT id,event_id,topic,aggregate_id,payload FROM task_report.outbox WHERE aggregate_id=$1 AND topic='task.requested' AND dispatch_version=$2`, task.ID, task.DispatchVersion).Scan(&event.ID, &event.EventID, &event.Topic, &event.AggregateID, &event.Payload)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM task_report.inbox WHERE event_id=$1`, event.EventID)
	})
	return event
}

func waitWorkerIntegration(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("worker integration condition timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func newIntegrationWorker(t *testing.T, store *Store, broker ConsumerBroker, assets AssetClient, slots int, drain time.Duration) *Worker {
	t.Helper()
	cfg := DefaultWorkerConfig()
	cfg.Slots = slots
	cfg.LeaseDuration = 5 * time.Second
	cfg.RenewInterval = 100 * time.Millisecond
	cfg.TaskTimeout = 10 * time.Second
	cfg.RecoveryInterval = 50 * time.Millisecond
	cfg.DrainTimeout = drain
	worker, err := NewWorkerWithConfig(store, broker, assets, &workerObjects{}, slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func TestWorkerPostgresAMQPConcurrentDuplicateRecoveryAndDrain(t *testing.T) {
	store, broker := workerIntegrationDependencies(t)
	first := executionTestTask(t, store, "export")
	second := executionTestTask(t, store, "export")
	expired := executionTestTask(t, store, "export")
	execution, err := store.ClaimTask(t.Context(), expired.ID, 1, "crashed-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	expireExecution(t, store, execution)
	entered := make(chan context.Context, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	var calls atomic.Int32
	assets := workerAssets{export: func(ctx context.Context) ([]asset.Record, error) {
		calls.Add(1)
		entered <- ctx
		select {
		case <-release:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	worker := newIntegrationWorker(t, store, broker, assets, 2, 2*time.Second)
	intake, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); worker.Run(intake) }()
	t.Cleanup(func() {
		cancel()
		releaseOnce.Do(func() { close(release) })
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("worker cleanup did not stop")
		}
	})
	firstEvent := workerRequestedEvent(t, store, first)
	secondEvent := workerRequestedEvent(t, store, second)
	for _, event := range []OutboxEvent{firstEvent, firstEvent, secondEvent} {
		if err := broker.Publish(t.Context(), event); err != nil {
			t.Fatal(err)
		}
	}
	waitWorkerIntegration(t, 5*time.Second, func() bool { return len(entered) == 2 })
	contexts := []context.Context{<-entered, <-entered}
	if calls.Load() != 2 {
		t.Fatalf("duplicate performed extra work: %d", calls.Load())
	}
	waitWorkerIntegration(t, 5*time.Second, func() bool {
		task, err := store.WorkerTask(t.Context(), expired.ID)
		return err == nil && task.State == Failed && task.DispatchVersion == 2 && task.Attempt == 1
	})
	// Both slots are occupied; recovery has still advanced the crashed execution.
	cancel()
	waitWorkerIntegration(t, time.Second, func() bool { return !worker.Ready() })
	for _, ctx := range contexts {
		if ctx.Err() != nil {
			t.Fatal("SIGTERM cancelled execution before drain")
		}
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker drain timed out")
	}
	for _, pair := range []struct {
		task  Task
		event OutboxEvent
	}{{first, firstEvent}, {second, secondEvent}} {
		task, err := store.WorkerTask(t.Context(), pair.task.ID)
		if err != nil || task.State != Succeeded || task.Attempt != 1 || task.ExecutionToken != "" {
			t.Fatalf("task=%+v error=%v", task, err)
		}
		seen, err := store.InboxSeen(t.Context(), worker.consumer, pair.event.EventID)
		if err != nil || !seen {
			t.Fatalf("durable inbox=%v error=%v", seen, err)
		}
	}
}

type workerFaultBroker struct {
	broker                    ConsumerBroker
	breakAck                  atomic.Bool
	ackFailures, redeliveries, acknowledged atomic.Int32
}
type workerFaultSubscription struct {
	Subscription
	deliveries chan Delivery
}

func (s *workerFaultSubscription) Deliveries() <-chan Delivery { return s.deliveries }

func (b *workerFaultBroker) Subscribe(ctx context.Context, tag string) (Subscription, error) {
	subscription, err := b.broker.Subscribe(ctx, tag)
	if err != nil {
		return nil, err
	}
	wrapped := &workerFaultSubscription{Subscription: subscription, deliveries: make(chan Delivery)}
	go func() {
		defer close(wrapped.deliveries)
		for {
			select {
			case <-subscription.Context().Done():
				return
			case d, ok := <-subscription.Deliveries():
				if !ok {
					return
				}
				if d.Redelivered {
					b.redeliveries.Add(1)
				}
				originalAck := d.AckFunc
				d.AckFunc = func() error {
					if b.breakAck.CompareAndSwap(true, false) {
						b.ackFailures.Add(1)
						_ = subscription.Close()
						return errors.New("injected disconnect after durable commit before ACK")
					}
					err := originalAck()
					if err == nil { b.acknowledged.Add(1) }
					return err
				}
				select {
				case wrapped.deliveries <- d:
				case <-subscription.Context().Done():
					return
				}
			}
		}
	}()
	return wrapped, nil
}

func TestWorkerPostgresAMQPDisconnectAfterCommitDeduplicatesRedelivery(t *testing.T) {
	store, broker := workerIntegrationDependencies(t)
	task := executionTestTask(t, store, "export")
	event := workerRequestedEvent(t, store, task)
	faults := &workerFaultBroker{broker: broker}
	faults.breakAck.Store(true)
	var calls atomic.Int32
	worker := newIntegrationWorker(t, store, faults, workerAssets{export: func(context.Context) ([]asset.Record, error) { calls.Add(1); return nil, nil }}, 1, time.Second)
	intake, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); worker.Run(intake) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("worker did not stop")
		}
	})
	if err := broker.Publish(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	waitWorkerIntegration(t, 7*time.Second, func() bool { return faults.ackFailures.Load() == 1 && faults.redeliveries.Load() >= 1 && faults.acknowledged.Load() == 1 })
	task, err := store.WorkerTask(t.Context(), task.ID)
	if err != nil || task.State != Succeeded || task.Attempt != 1 || calls.Load() != 1 {
		t.Fatalf("redelivery mutated committed task: task=%+v calls=%d err=%v", task, calls.Load(), err)
	}
	seen, err := store.InboxSeen(t.Context(), worker.consumer, event.EventID)
	if err != nil || !seen {
		t.Fatalf("inbox missing: %v %v", seen, err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop")
	}
}

func TestWorkerPostgresAMQPForcedDrainLeavesExecutionRecoverable(t *testing.T) {
	store, broker := workerIntegrationDependencies(t)
	task := executionTestTask(t, store, "export")
	event := workerRequestedEvent(t, store, task)
	entered := make(chan struct{})
	worker := newIntegrationWorker(t, store, broker, workerAssets{export: func(ctx context.Context) ([]asset.Record, error) { close(entered); <-ctx.Done(); return nil, ctx.Err() }}, 1, 100*time.Millisecond)
	intake, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); worker.Run(intake) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("worker did not stop")
		}
	})
	if err := broker.Publish(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("task did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("forced drain did not stop")
	}
	running, err := store.WorkerTask(t.Context(), task.ID)
	if err != nil || running.State != Running || running.Attempt != 1 {
		t.Fatalf("forced drain wrote business failure: %+v %v", running, err)
	}
	seen, err := store.InboxSeen(t.Context(), worker.consumer, event.EventID)
	if err != nil || seen {
		t.Fatalf("uncommitted execution was acknowledged: inbox=%v err=%v", seen, err)
	}
	// Channel close returns the original notification, while the lease remains
	// recoverable and schedules the next generation durably after expiry.
	sub, err := broker.Subscribe(t.Context(), "drain-check-"+uuid())
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	redelivery := receiveDelivery(t, sub)
	if !redelivery.Redelivered {
		t.Fatal("forced drain message did not return to broker")
	}
	if err := redelivery.Ack(); err != nil {
		t.Fatal(err)
	}
	expireExecution(t, store, Execution{Task: running})
	if err := store.RecoverExpiredTask(t.Context(), task.ID, 1); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.WorkerTask(t.Context(), task.ID)
	if err != nil || recovered.State != Failed || recovered.DispatchVersion != 2 || recovered.Attempt != 1 {
		t.Fatalf("recovered=%+v err=%v", recovered, err)
	}
}
