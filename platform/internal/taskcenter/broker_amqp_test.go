package taskcenter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestPublishReturnCannotBeMistakenForConfirm(t *testing.T) {
	// Both buffers are ready: select may pick the confirm first. v1.11.0
	// guarantees the return was enqueued first, so draining must detect it.
	for i := 0; i < 1000; i++ {
		confirms := make(chan amqp.Confirmation, 1)
		returns := make(chan amqp.Return, 1)
		returns <- amqp.Return{MessageId: "event", ReplyCode: 312, ReplyText: "NO_ROUTE"}
		confirms <- amqp.Confirmation{DeliveryTag: 1, Ack: true}
		err := waitPublishConfirmation(context.Background(), "event", 1, confirms, returns, nil, nil)
		if !errors.Is(err, ErrPublishReturned) {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
}

func TestPublishConfirmationFailures(t *testing.T) {
	t.Run("nack", func(t *testing.T) {
		confirms := make(chan amqp.Confirmation, 1)
		confirms <- amqp.Confirmation{DeliveryTag: 1, Ack: false}
		if err := waitPublishConfirmation(context.Background(), "event", 1, confirms, nil, nil, nil); !errors.Is(err, ErrPublishNack) {
			t.Fatal(err)
		}
	})
	t.Run("unexpected sequence", func(t *testing.T) {
		confirms := make(chan amqp.Confirmation, 1)
		confirms <- amqp.Confirmation{DeliveryTag: 2, Ack: true}
		if err := waitPublishConfirmation(context.Background(), "event", 1, confirms, nil, nil, nil); err == nil {
			t.Fatal("accepted another event's confirm")
		}
	})
	t.Run("connection closed", func(t *testing.T) {
		closed := make(chan *amqp.Error)
		close(closed)
		if err := waitPublishConfirmation(context.Background(), "event", 1, nil, nil, nil, closed); !errors.Is(err, ErrBrokerClosed) {
			t.Fatal(err)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := waitPublishConfirmation(ctx, "event", 1, nil, nil, nil, nil); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

func amqpTestBroker(t *testing.T) (*AMQPBroker, *amqp.Channel) {
	t.Helper()
	address := os.Getenv("TASKCENTER_AMQP_TEST_URL")
	if address == "" {
		t.Skip("TASKCENTER_AMQP_TEST_URL is not set; requires a real RabbitMQ broker")
	}
	broker, err := NewAMQPBroker(address)
	if err != nil {
		t.Fatal(err)
	}
	suffix := uuid()
	broker.exchange = "test.tasks." + suffix
	broker.queue = "test.worker." + suffix
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err = broker.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	admin, err := amqp.Dial(address)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := admin.Channel()
	if err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = broker.Close()
		_, _ = channel.QueueDelete(broker.queue, false, false, false)
		_, _ = channel.QueueDelete(broker.queue+".dead", false, false, false)
		_ = channel.ExchangeDelete(broker.exchange, false, false)
		_ = channel.ExchangeDelete(broker.exchange+".dead", false, false)
		_ = admin.Close()
	})
	return broker, channel
}

func testBrokerEvent() OutboxEvent {
	eventID, taskID := uuid(), uuid()
	return OutboxEvent{EventID: eventID, Topic: "task.requested", AggregateID: taskID,
		Payload: []byte(fmt.Sprintf(`{"schema_version":2,"event_id":%q,"task_id":%q,"kind":"export","dispatch_version":1}`, eventID, taskID))}
}

func receiveDelivery(t *testing.T, subscription Subscription) Delivery {
	t.Helper()
	select {
	case delivery, open := <-subscription.Deliveries():
		if !open {
			t.Fatal("subscription closed before delivery")
		}
		return delivery
	case err := <-subscription.Done():
		t.Fatalf("subscription lost: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for delivery")
	}
	return Delivery{}
}

func TestAMQPManualAckPrefetchRedeliveryAndDrain(t *testing.T) {
	broker, _ := amqpTestBroker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	first, err := broker.Subscribe(ctx, "first-"+uuid())
	if err != nil {
		t.Fatal(err)
	}
	event1, event2 := testBrokerEvent(), testBrokerEvent()
	if err = broker.Publish(ctx, event1); err != nil {
		t.Fatal(err)
	}
	if err = broker.Publish(ctx, event2); err != nil {
		t.Fatal(err)
	}
	delivery1 := receiveDelivery(t, first)
	if delivery1.MessageID != event1.EventID || delivery1.Headers["event_id"] != event1.EventID {
		t.Fatalf("lost event metadata: %#v", delivery1)
	}
	select {
	case <-first.Deliveries():
		t.Fatal("prefetch=1 allowed a second unacknowledged delivery")
	case <-time.After(150 * time.Millisecond):
	}
	_ = first.Close()
	select {
	case <-first.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("closed session did not signal execution cancellation")
	}
	if err = delivery1.Ack(); err == nil {
		t.Fatal("old delivery acknowledged on a closed original channel")
	}
	second, err := broker.Subscribe(ctx, "second-"+uuid())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		delivery := receiveDelivery(t, second)
		seen[delivery.MessageID] = true
		if delivery.MessageID == event1.EventID && !delivery.Redelivered {
			t.Fatal("unacknowledged delivery was not marked redelivered")
		}
		if i == 1 {
			if err = second.Stop(); err != nil {
				t.Fatal(err)
			}
			if second.Context().Err() != nil {
				t.Fatal("Stop cancelled in-flight execution before drain")
			}
		}
		if err = delivery.Ack(); err != nil {
			t.Fatal(err)
		}
	}
	if !seen[event1.EventID] || !seen[event2.EventID] {
		t.Fatalf("missing durable delivery: %v", seen)
	}
}

func TestAMQPRealReturnAndPublisherReconnect(t *testing.T) {
	broker, admin := amqpTestBroker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for i := 0; i < 10; i++ {
		if err := broker.Ensure(ctx); err != nil {
			t.Fatal(err)
		}
		if err := admin.QueueUnbind(broker.queue, "task.requested", broker.exchange, nil); err != nil {
			t.Fatal(err)
		}
		if err := broker.Publish(ctx, testBrokerEvent()); !errors.Is(err, ErrPublishReturned) {
			t.Fatalf("unroutable attempt %d falsely confirmed: %v", i, err)
		}
	}
	// Each failed publish closes the transport; the next attempt recreates the
	// connection, topology and confirm channel before sending the same event ID.
	event := testBrokerEvent()
	if err := broker.Publish(ctx, event); err != nil {
		t.Fatal(err)
	}
	subscription, err := broker.Subscribe(ctx, "after-reconnect-"+uuid())
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	delivery := receiveDelivery(t, subscription)
	if delivery.MessageID != event.EventID {
		t.Fatal("unexpected event after reconnect")
	}
	if err = delivery.Ack(); err != nil {
		t.Fatal(err)
	}
}

func TestAMQPDeadOutboxAndBrokerDLXBinding(t *testing.T) {
	broker, admin := amqpTestBroker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	event := testBrokerEvent()
	event.Topic = "task.dead"
	if err := broker.Publish(ctx, event); err != nil {
		t.Fatal(err)
	}
	dead, ok, err := admin.Get(broker.queue+".dead", true)
	if err != nil || !ok || dead.MessageId != event.EventID {
		t.Fatalf("dead outbox routing: %v %v %#v", err, ok, dead)
	}
	subscription, err := broker.Subscribe(ctx, "dlx-"+uuid())
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	event = testBrokerEvent()
	if err = broker.Publish(ctx, event); err != nil {
		t.Fatal(err)
	}
	delivery := receiveDelivery(t, subscription)
	if err = delivery.Nack(false); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		dead, ok, err = admin.Get(broker.queue+".dead", true)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			if dead.MessageId != event.EventID || dead.RoutingKey != "task.requested" {
				t.Fatalf("missing legacy DLX binding: %#v", dead)
			}
			return
		}
		if !waitPublisher(ctx, 20*time.Millisecond) {
			break
		}
	}
	t.Fatal("broker dead lettering was not routed to the dead queue")
}

func TestAMQPBrokerCancelsSubscriptionOnDeletedQueue(t *testing.T) {
	broker, admin := amqpTestBroker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	subscription, err := broker.Subscribe(ctx, "deleted-queue-"+uuid())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.QueueDelete(broker.queue, false, false, false); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-subscription.Done():
		if err == nil || subscription.Context().Err() == nil {
			t.Fatal("broker cancellation did not revoke execution context")
		}
	case <-ctx.Done():
		t.Fatal("broker cancel did not close consumer session")
	}
	// The application supervisor owns resubscription, including topology/Qos.
	newSubscription, err := broker.Subscribe(ctx, "recreated-queue-"+uuid())
	if err != nil {
		t.Fatal(err)
	}
	defer newSubscription.Close()
	event := testBrokerEvent()
	if err = broker.Publish(ctx, event); err != nil {
		t.Fatal(err)
	}
	delivery := receiveDelivery(t, newSubscription)
	if delivery.MessageID != event.EventID {
		t.Fatal("resubscription lost routing")
	}
	if err = delivery.Ack(); err != nil {
		t.Fatal(err)
	}
}

func TestAMQPRealBrokerNackIsNotPublished(t *testing.T) {
	broker, admin := amqpTestBroker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// A separate temporary queue rejects publishes on overflow. Production
	// queues retain their existing declaration arguments and queue type.
	limitedQueue := broker.queue + ".reject-overflow"
	if _, err := admin.QueueDeclare(limitedQueue, true, false, false, false, amqp.Table{"x-max-length": int64(1), "x-overflow": "reject-publish"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.QueueDelete(limitedQueue, false, false, false) })
	if err := admin.QueueUnbind(broker.queue, "task.requested", broker.exchange, nil); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueueBind(limitedQueue, "task.requested", broker.exchange, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := broker.Publish(ctx, testBrokerEvent()); err != nil {
		t.Fatal(err)
	}
	if err := broker.Publish(ctx, testBrokerEvent()); !errors.Is(err, ErrPublishNack) {
		t.Fatalf("overflowing queue failed to reject publish: %v", err)
	}
}
