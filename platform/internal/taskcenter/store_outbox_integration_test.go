package taskcenter

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

func outboxTestStore(t *testing.T) *Store {
	t.Helper()
	address := os.Getenv("TASKCENTER_TEST_DATABASE_URL")
	if address == "" {
		t.Skip("TASKCENTER_TEST_DATABASE_URL is not set; requires real PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := Migrate(ctx, address, false); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	return store
}

func insertTestOutbox(t *testing.T, store *Store, count int) []OutboxEvent {
	t.Helper()
	ctx := context.Background()
	aggregateID := uuid()
	events := make([]OutboxEvent, 0, count)
	for i := 0; i < count; i++ {
		event := testBrokerEvent()
		event.AggregateID = aggregateID
		if err := store.pool.QueryRow(ctx, `INSERT INTO task_report.outbox(event_id,topic,aggregate_id,payload,available_at)
		 VALUES($1,$2,$3,$4,'1900-01-01'::timestamptz) RETURNING id`, event.EventID, event.Topic, event.AggregateID, event.Payload).Scan(&event.ID); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	t.Cleanup(func() {
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM task_report.outbox WHERE aggregate_id=$1`, aggregateID)
	})
	return events
}

func TestOutboxConcurrentPublishersClaimDisjointEvents(t *testing.T) {
	store := outboxTestStore(t)
	events := insertTestOutbox(t, store, 32)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	results := make(chan []OutboxEvent, len(events))
	errorsCh := make(chan error, len(events))
	start := make(chan struct{})
	var group sync.WaitGroup
	for i := 0; i < len(events); i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			claimed, err := store.ClaimOutbox(ctx, "publisher-"+uuid(), 1, time.Minute)
			if err != nil {
				errorsCh <- err
				return
			}
			results <- claimed
		}()
	}
	close(start)
	group.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		t.Error(err)
	}
	expected := map[int64]bool{}
	for _, event := range events {
		expected[event.ID] = true
	}
	seen := map[int64]bool{}
	for claimed := range results {
		if len(claimed) != 1 {
			t.Errorf("expected exactly one claim: %#v", claimed)
			continue
		}
		event := claimed[0]
		if !expected[event.ID] {
			t.Errorf("claimed an unrelated event %d", event.ID)
		}
		if seen[event.ID] {
			t.Errorf("event %d claimed by multiple publishers", event.ID)
		}
		if event.ClaimToken == "" || event.ClaimedBy == "" || event.ClaimUntil.IsZero() {
			t.Errorf("missing claim ownership: %#v", event)
		}
		seen[event.ID] = true
	}
	if len(seen) != len(events) {
		t.Fatalf("claimed %d of %d events", len(seen), len(events))
	}
}

func TestOutboxClaimTakeoverFencesLatePublisher(t *testing.T) {
	store := outboxTestStore(t)
	wanted := insertTestOutbox(t, store, 1)[0]
	ctx := context.Background()
	first, err := store.ClaimOutbox(ctx, "old-publisher", 1, time.Minute)
	if err != nil || len(first) != 1 || first[0].ID != wanted.ID {
		t.Fatalf("first claim: %v %#v", err, first)
	}
	old := first[0]
	if _, err = store.pool.Exec(ctx, `UPDATE task_report.outbox SET claim_until=clock_timestamp()-interval '1 second' WHERE id=$1`, old.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkPublished(ctx, old); !errors.Is(err, ErrOutboxClaimLost) {
		t.Fatalf("expired publisher marked success: %v", err)
	}
	second, err := store.ClaimOutbox(ctx, "new-publisher", 1, time.Minute)
	if err != nil || len(second) != 1 || second[0].ID != old.ID {
		t.Fatalf("takeover: %v %#v", err, second)
	}
	current := second[0]
	if current.EventID != old.EventID || current.ClaimToken == old.ClaimToken {
		t.Fatalf("event identity or fencing token incorrect: %#v", current)
	}
	if err = store.MarkPublished(ctx, old); !errors.Is(err, ErrOutboxClaimLost) {
		t.Fatalf("old publisher stole success: %v", err)
	}
	if err = store.MarkPublishFailed(ctx, old, "late old failure"); !errors.Is(err, ErrOutboxClaimLost) {
		t.Fatalf("old publisher stole failure: %v", err)
	}
	var attempts int
	var token string
	if err = store.pool.QueryRow(ctx, `SELECT attempts,claim_token::text FROM task_report.outbox WHERE id=$1`, old.ID).Scan(&attempts, &token); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 || token != current.ClaimToken {
		t.Fatal("stale publisher changed the new owner's event")
	}
	if err = store.MarkPublishFailed(ctx, current, "retry me"); err != nil {
		t.Fatal(err)
	}
	var cleared, scheduled bool
	if err = store.pool.QueryRow(ctx, `SELECT attempts,claim_token IS NULL AND claimed_by IS NULL AND claim_until IS NULL,available_at>clock_timestamp() FROM task_report.outbox WHERE id=$1`, old.ID).Scan(&attempts, &cleared, &scheduled); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || !cleared || !scheduled {
		t.Fatalf("failure did not release with backoff: %d %v %v", attempts, cleared, scheduled)
	}
	if _, err = store.pool.Exec(ctx, `UPDATE task_report.outbox SET available_at='1900-01-01' WHERE id=$1`, old.ID); err != nil {
		t.Fatal(err)
	}
	third, err := store.ClaimOutbox(ctx, "retry-publisher", 1, time.Minute)
	if err != nil || len(third) != 1 || third[0].EventID != old.EventID {
		t.Fatalf("retry identity lost: %v %#v", err, third)
	}
	if err = store.MarkPublished(ctx, third[0]); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkPublishFailed(ctx, third[0], "late failure after success"); !errors.Is(err, ErrOutboxClaimLost) {
		t.Fatalf("published event reverted: %v", err)
	}
}

func TestPublisherRealReturnRetainsOutboxThenReconnects(t *testing.T) {
	store := outboxTestStore(t)
	broker, admin := amqpTestBroker(t)
	wanted := insertTestOutbox(t, store, 1)[0]
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := admin.QueueUnbind(broker.queue, "task.requested", broker.exchange, nil); err != nil {
		t.Fatal(err)
	}
	publisher := NewPublisher(store, broker)
	if sent, err := publisher.PublishNext(ctx); sent || !errors.Is(err, ErrPublishReturned) {
		t.Fatalf("unroutable outbox falsely published: %v %v", sent, err)
	}
	var retained, released bool
	var attempts int
	if err := store.pool.QueryRow(ctx, `SELECT published_at IS NULL,claim_token IS NULL,attempts FROM task_report.outbox WHERE id=$1`, wanted.ID).Scan(&retained, &released, &attempts); err != nil {
		t.Fatal(err)
	}
	if !retained || !released || attempts != 1 {
		t.Fatalf("returned event not retained: %v %v %d", retained, released, attempts)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE task_report.outbox SET available_at='1900-01-01' WHERE id=$1`, wanted.ID); err != nil {
		t.Fatal(err)
	}
	if sent, err := publisher.PublishNext(ctx); !sent || err != nil {
		t.Fatalf("reconnect/retry: %v %v", sent, err)
	}
	message, ok, err := admin.Get(broker.queue, true)
	if err != nil || !ok || message.MessageId != wanted.EventID {
		t.Fatalf("retry did not preserve event identity: %v %v %#v", err, ok, message)
	}
}

func TestOutboxWriteRejectsClaimExpiredWhileWaitingForUnchangedLock(t *testing.T) {
	for _, operation := range []string{"success", "failure"} {
		t.Run(operation, func(t *testing.T) {
			store := outboxTestStore(t)
			wanted := insertTestOutbox(t, store, 1)[0]
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			claimed, err := store.ClaimOutbox(ctx, "blocked-publisher", 1, 400*time.Millisecond)
			if err != nil || len(claimed) != 1 || claimed[0].ID != wanted.ID {
				t.Fatalf("claim: %v %#v", err, claimed)
			}
			event := claimed[0]
			blocker, err := store.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(context.Background())
			var id int64
			if err = blocker.QueryRow(ctx, `SELECT id FROM task_report.outbox WHERE id=$1 FOR UPDATE`, event.ID).Scan(&id); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() {
				if operation == "success" {
					result <- store.MarkPublished(ctx, event)
				} else {
					result <- store.MarkPublishFailed(ctx, event, "late failure")
				}
			}()
			select {
			case err = <-result:
				t.Fatalf("write returned while row is locked: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			for {
				var expired bool
				if err = store.pool.QueryRow(ctx, `SELECT claim_until<=clock_timestamp() FROM task_report.outbox WHERE id=$1`, event.ID).Scan(&expired); err != nil {
					t.Fatal(err)
				}
				if expired {
					break
				}
				if !waitPublisher(ctx, 20*time.Millisecond) {
					t.Fatal("claim never expired")
				}
			}
			if err = blocker.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-result:
				if !errors.Is(err, ErrOutboxClaimLost) {
					t.Fatalf("expired claim wrote after waiting on unchanged row: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("write remained blocked after row unlocked")
			}
			var attempts int
			var unpublished bool
			if err = store.pool.QueryRow(ctx, `SELECT attempts,published_at IS NULL FROM task_report.outbox WHERE id=$1`, event.ID).Scan(&attempts, &unpublished); err != nil {
				t.Fatal(err)
			}
			if attempts != 0 || !unpublished {
				t.Fatal("expired sender changed outbox state")
			}
		})
	}
}

func TestOutboxConfirmBeforePublisherCrashResendsSameEventID(t *testing.T) {
	store := outboxTestStore(t)
	broker, admin := amqpTestBroker(t)
	wanted := insertTestOutbox(t, store, 1)[0]
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	claimed, err := store.ClaimOutbox(ctx, "crashed-publisher", 1, time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].ID != wanted.ID {
		t.Fatalf("claim: %v %#v", err, claimed)
	}
	if err = broker.Publish(ctx, claimed[0]); err != nil {
		t.Fatal(err)
	}
	// Model death after confirm but before the DB write: the row retains its
	// claim until database time expires it; another instance then republishes.
	if _, err = store.pool.Exec(ctx, `UPDATE task_report.outbox SET claim_until=clock_timestamp()-interval '1 second' WHERE id=$1`, wanted.ID); err != nil {
		t.Fatal(err)
	}
	publisher := NewPublisher(store, broker)
	if sent, err := publisher.PublishNext(ctx); !sent || err != nil {
		t.Fatalf("resume after crash: %v %v", sent, err)
	}
	for i := 0; i < 2; i++ {
		message, ok, err := admin.Get(broker.queue, true)
		if err != nil || !ok || message.MessageId != wanted.EventID {
			t.Fatalf("delivery %d changed event ID: %v %v %#v", i, err, ok, message)
		}
	}
	var published bool
	var attempts int
	if err = store.pool.QueryRow(ctx, `SELECT published_at IS NOT NULL,attempts FROM task_report.outbox WHERE id=$1`, wanted.ID).Scan(&published, &attempts); err != nil {
		t.Fatal(err)
	}
	if !published || attempts != 0 {
		t.Fatalf("recovery counted a send retry as a failure: %v %d", published, attempts)
	}
}
