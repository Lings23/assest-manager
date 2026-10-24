package taskcenter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"asset-platform/internal/asset"
)

const publisherCrashChildEnv = "TASKCENTER_PUBLISHER_CRASH_CHILD"

// The synchronization is test-only: the actual AMQP adapter must finish its
// Confirm/Return decision before the parent lets Publisher enter MarkPublished.
type publisherCrashBarrier struct{ broker *AMQPBroker }

func (b publisherCrashBarrier) Publish(ctx context.Context, event OutboxEvent) error {
	if err := b.broker.Publish(ctx, event); err != nil {
		return err
	}
	if b.broker.publishConfirmed.Load() != 1 {
		return errors.New("publisher child did not receive exactly one real AMQP confirm")
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintf(os.Stdout, "CONFIRMED %s\n", encoded); err != nil {
		return err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return err
	}
	if line != "continue\n" {
		return errors.New("unexpected publisher child continuation")
	}
	return nil
}

// This helper runs only in a separate OS process, never in the parent suite.
func TestPublisherCrashChild(t *testing.T) {
	if os.Getenv(publisherCrashChildEnv) != "1" {
		t.Skip("helper for the publisher process kill integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	store, err := OpenStore(ctx, os.Getenv("TASKCENTER_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	broker, err := NewAMQPBroker(os.Getenv("TASKCENTER_AMQP_TEST_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	broker.exchange = os.Getenv("TASKCENTER_PUBLISHER_CRASH_EXCHANGE")
	broker.queue = os.Getenv("TASKCENTER_PUBLISHER_CRASH_QUEUE")
	publisher := NewPublisher(store, publisherCrashBarrier{broker})
	publisher.ID = os.Getenv("TASKCENTER_PUBLISHER_CRASH_ID")
	// Exceeds the production 10-second confirm timeout. The parent waits for
	// this lease to expire using database time, without editing claim_until.
	publisher.Lease = 11 * time.Second
	if published, err := publisher.PublishNext(ctx); err != nil || !published {
		t.Fatalf("publisher child: published=%v err=%v", published, err)
	}
	t.Fatal("publisher child survived the required process kill")
}

func TestPublisherProcessKillAfterConfirmBeforeDatabaseMark(t *testing.T) {
	address := os.Getenv("TASKCENTER_TEST_DATABASE_URL")
	if address == "" {
		address = os.Getenv("TASK_TEST_DATABASE_URL")
	}
	if address == "" || os.Getenv("TASKCENTER_AMQP_TEST_URL") == "" {
		t.Skip("real PostgreSQL and RabbitMQ test URLs are required")
	}
	store := executionTestStore(t)
	broker, admin := amqpTestBroker(t)
	task := executionTestTask(t, store, "export")
	wanted := workerRequestedEvent(t, store, task)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	// Make only this fixture first in the global outbox ordering. No other
	// event is marked, rescheduled, or removed to prepare the crash window.
	if _, err := store.pool.Exec(ctx, `UPDATE task_report.outbox SET available_at='1900-01-01' WHERE id=$1`, wanted.ID); err != nil {
		t.Fatal(err)
	}
	childID := "publisher-kill-" + uuid()
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("application_name", childID)
	parsed.RawQuery = query.Encode()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(executable, "-test.run=^TestPublisherCrashChild$", "-test.timeout=30s", "-test.v")
	child.Env = append(os.Environ(), publisherCrashChildEnv+"=1",
		"TASKCENTER_TEST_DATABASE_URL="+parsed.String(),
		"TASKCENTER_PUBLISHER_CRASH_EXCHANGE="+broker.exchange,
		"TASKCENTER_PUBLISHER_CRASH_QUEUE="+broker.queue,
		"TASKCENTER_PUBLISHER_CRASH_ID="+childID)
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = child.Stdout
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	childDone := make(chan struct{})
	var childErr error
	go func() {
		childErr = child.Wait()
		close(childDone)
	}()
	defer func() {
		_ = child.Process.Kill()
		<-childDone
	}()
	confirmed := make(chan OutboxEvent, 1)
	output := make(chan string, 1)
	go func() {
		var lines strings.Builder
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			lines.WriteString(line + "\n")
			if strings.HasPrefix(line, "CONFIRMED ") {
				var event OutboxEvent
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "CONFIRMED ")), &event); err == nil {
					confirmed <- event
				}
			}
		}
		output <- lines.String()
	}()
	var first OutboxEvent
	select {
	case first = <-confirmed:
	case <-childDone:
		t.Fatalf("child ended before AMQP confirm: %v\n%s", childErr, <-output)
	case <-ctx.Done():
		t.Fatal("child did not reach the confirmed publish barrier")
	}
	if first.ID != wanted.ID || first.EventID != wanted.EventID || first.ClaimedBy != childID || first.ClaimToken == "" {
		t.Fatalf("child confirmed another event or lacked ownership: %+v", first)
	}
	blocker, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	var lockedID int64
	if err = blocker.QueryRow(ctx, `SELECT id FROM task_report.outbox WHERE id=$1 FOR UPDATE`, wanted.ID).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}
	var blockerPID int
	if err = blocker.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
		t.Fatal(err)
	}
	if _, err = fmt.Fprintln(stdin, "continue"); err != nil {
		t.Fatal(err)
	}
	lockCtx, stopLockWait := context.WithTimeout(ctx, 5*time.Second)
	defer stopLockWait()
	for {
		var waiting bool
		if err = store.pool.QueryRow(lockCtx, `SELECT EXISTS (
		 SELECT 1 FROM pg_stat_activity
		 WHERE application_name=$1 AND wait_event_type='Lock'
		 AND $2::int=ANY(pg_blocking_pids(pid))
		 AND query LIKE '%task_report.outbox%FOR UPDATE%'
		)`, childID, blockerPID).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if !waitPublisher(lockCtx, 20*time.Millisecond) {
			t.Fatal("database did not observe the child blocked in MarkPublished")
		}
	}
	var unresolved bool
	if err = store.pool.QueryRow(ctx, `SELECT published_at IS NULL AND attempts=0
	 AND claimed_by=$2 AND claim_token=$3::uuid AND claim_until>clock_timestamp()
	 FROM task_report.outbox WHERE id=$1`, wanted.ID, childID, first.ClaimToken).Scan(&unresolved); err != nil || !unresolved {
		t.Fatalf("event was not unpublished with a live child claim at kill: %v %v", unresolved, err)
	}
	// Kill is SIGKILL on Unix and TerminateProcess on Windows. It cannot run
	// defer, gracefully drain, release the claim, or mark the confirmed event.
	if err = child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-childDone:
		var exitErr *exec.ExitError
		if !errors.As(childErr, &exitErr) || child.ProcessState.Success() {
			t.Fatalf("publisher was not abruptly killed: %v", childErr)
		}
	case <-ctx.Done():
		t.Fatal("killed publisher process did not exit")
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for {
		var expired, unchanged bool
		if err = store.pool.QueryRow(ctx, `SELECT claim_until<=clock_timestamp(),
		 published_at IS NULL AND attempts=0 AND claimed_by=$2 AND claim_token=$3::uuid AND claim_until=$4
		 FROM task_report.outbox WHERE id=$1`, wanted.ID, childID, first.ClaimToken, first.ClaimUntil).Scan(&expired, &unchanged); err != nil {
			t.Fatal(err)
		}
		if !unchanged {
			t.Fatal("killed publisher changed the durable outbox claim")
		}
		if expired {
			break
		}
		if !waitPublisher(ctx, 25*time.Millisecond) {
			t.Fatal("killed publisher's claim did not expire naturally")
		}
	}
	publisher := NewPublisher(store, broker)
	if sent, err := publisher.PublishNext(ctx); err != nil || !sent {
		t.Fatalf("publisher takeover: published=%v err=%v", sent, err)
	}
	var calls atomic.Int32
	assets := workerAssets{export: func(context.Context) ([]asset.Record, error) {
		calls.Add(1)
		return nil, nil
	}}
	worker := newIntegrationWorker(t, store, broker, assets, 1, time.Second)
	for i := 0; i < 2; i++ {
		message, ok, err := admin.Get(broker.queue, false)
		if err != nil || !ok || message.MessageId != wanted.EventID || !bytes.Equal(message.Body, wanted.Payload) {
			t.Fatalf("real delivery %d lost the original event: ok=%v err=%v message=%+v", i, ok, err, message)
		}
		action := worker.process(ctx, Delivery{Body: message.Body, MessageID: message.MessageId,
			Headers: map[string]any(message.Headers), RoutingKey: message.RoutingKey})
		if !action.ack || action.err != nil {
			t.Fatalf("worker delivery %d did not reach a durable result: %+v", i, action)
		}
		if err := message.Ack(false); err != nil {
			t.Fatal(err)
		}
	}
	current, err := store.WorkerTask(ctx, task.ID)
	if err != nil || current.State != Succeeded || current.Attempt != 1 || calls.Load() != 1 {
		t.Fatalf("duplicate business execution after publisher kill: task=%+v calls=%d err=%v", current, calls.Load(), err)
	}
	seen, err := store.InboxSeen(ctx, worker.consumer, wanted.EventID)
	if err != nil || !seen {
		t.Fatalf("durable inbox deduplication missing: seen=%v err=%v", seen, err)
	}
	var published, released bool
	var attempts int
	if err := store.pool.QueryRow(ctx, `SELECT published_at IS NOT NULL,
	 claim_token IS NULL AND claimed_by IS NULL AND claim_until IS NULL,attempts
	 FROM task_report.outbox WHERE id=$1`, wanted.ID).Scan(&published, &released, &attempts); err != nil || !published || !released || attempts != 0 {
		t.Fatalf("confirmed recovery did not complete the conditional outbox write: published=%v released=%v attempts=%d err=%v", published, released, attempts, err)
	}
	t.Log("OS-killed publisher after real Confirm and observed MarkPublished lock wait; unchanged 11s lease expired naturally; two same-ID deliveries produced one business execution")
}
