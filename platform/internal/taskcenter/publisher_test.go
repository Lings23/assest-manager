package taskcenter

import (
	"context"
	"errors"
	"testing"
	"time"
)

type publisherTestStore struct {
	event                      OutboxEvent
	limit                      int
	claimed, published, failed int
	markError                  error
}

func (s *publisherTestStore) ClaimOutbox(_ context.Context, _ string, limit int, _ time.Duration) ([]OutboxEvent, error) {
	s.limit = limit
	s.claimed++
	if s.claimed > 1 {
		return nil, nil
	}
	return []OutboxEvent{s.event}, nil
}
func (s *publisherTestStore) MarkPublished(_ context.Context, event OutboxEvent) error {
	if event.ClaimToken != s.event.ClaimToken {
		return ErrOutboxClaimLost
	}
	s.published++
	return s.markError
}
func (s *publisherTestStore) MarkPublishFailed(_ context.Context, event OutboxEvent, _ string) error {
	if event.ClaimToken != s.event.ClaimToken {
		return ErrOutboxClaimLost
	}
	s.failed++
	return s.markError
}

type publisherTestBroker struct {
	publishError error
	cancel       context.CancelFunc
}

func (b publisherTestBroker) Publish(context.Context, OutboxEvent) error {
	if b.cancel != nil {
		b.cancel()
	}
	return b.publishError
}

func TestPublisherOnlyMarksConfirmedEvent(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		err                       error
		wantPublished, wantFailed int
	}{
		{"confirmed", nil, 1, 0}, {"returned", ErrPublishReturned, 0, 1}, {"nack", ErrPublishNack, 0, 1}, {"connection closed", ErrBrokerClosed, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &publisherTestStore{event: OutboxEvent{EventID: uuid(), ClaimToken: uuid()}}
			publisher := NewPublisher(store, publisherTestBroker{publishError: tc.err})
			published, err := publisher.PublishNext(context.Background())
			if !errors.Is(err, tc.err) || published != (tc.err == nil) {
				t.Fatalf("published=%v err=%v", published, err)
			}
			if store.limit != 1 || store.published != tc.wantPublished || store.failed != tc.wantFailed {
				t.Fatalf("unexpected outbox calls: %#v", store)
			}
		})
	}
}

func TestPublisherAmbiguousDatabaseCommitDoesNotScheduleAnotherSend(t *testing.T) {
	store := &publisherTestStore{event: OutboxEvent{EventID: uuid(), ClaimToken: uuid()}, markError: errors.New("connection lost while commit result unknown")}
	publisher := NewPublisher(store, publisherTestBroker{})
	if _, err := publisher.PublishNext(context.Background()); err == nil {
		t.Fatal("ignored database failure")
	}
	if store.failed != 0 || store.published != 1 {
		t.Fatal("uncertain published update converted to an immediate failure update")
	}
}

func TestPublisherCancellationLeavesClaimForRecovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &publisherTestStore{event: OutboxEvent{EventID: uuid(), ClaimToken: uuid()}}
	publisher := NewPublisher(store, publisherTestBroker{publishError: context.Canceled, cancel: cancel})
	if _, err := publisher.PublishNext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if store.failed != 0 || store.published != 0 {
		t.Fatal("cancelled publisher mutated unresolved claim")
	}
}
