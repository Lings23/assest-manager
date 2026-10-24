package taskcenter

import (
	"asset-platform/internal/asset"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var workerTaskID = "11111111-1111-4111-8111-111111111111"
var workerEventID = "22222222-2222-4222-8222-222222222222"

type workerMemoryStore struct {
	mu                                                                              sync.Mutex
	task                                                                            Task
	seen, parallel, legacySafe                                                      bool
	inboxErr, claimErr, completeErr, retryErr, renewErr, progressErr, quarantineErr error
	claims, retries, completes, renewals, recovered, quarantined, terminated        atomic.Int32
	result                                                                          map[string]any
	committed                                                                       int
}

func newWorkerMemoryStore() *workerMemoryStore {
	now := time.Now().UTC()
	due := now.Add(-time.Second)
	return &workerMemoryStore{task: Task{ID: workerTaskID, Kind: "export", AssetType: "responsible-department", State: Pending, DispatchVersion: 1, NextRunAt: &due, ObservedAt: now, MaxAttempts: 3}}
}
func (s *workerMemoryStore) InboxSeen(context.Context, string, string) (bool, error) {
	return s.seen, s.inboxErr
}
func (s *workerMemoryStore) WorkerTask(context.Context, string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.task
	if s.parallel {
		task.State = Pending
	}
	return task, nil
}
func (s *workerMemoryStore) ClaimTask(ctx context.Context, id string, version int64, worker string, lease time.Duration) (Execution, error) {
	s.claims.Add(1)
	if s.claimErr != nil {
		return Execution{}, s.claimErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.task.State = Running
	s.task.Attempt++
	until := time.Now().Add(lease)
	return Execution{Task: s.task, WorkerID: worker, Token: uuid(), DispatchVersion: version, LeaseUntil: until}, nil
}
func (s *workerMemoryStore) RenewLease(context.Context, Execution, time.Duration) error {
	s.renewals.Add(1)
	return s.renewErr
}
func (s *workerMemoryStore) Complete(ctx context.Context, consumer, eventID string, e Execution, result map[string]any) error {
	s.completes.Add(1)
	if s.completeErr != nil {
		return s.completeErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.task.State = Succeeded
	s.result = result
	return nil
}
func (s *workerMemoryStore) RetryOrDead(context.Context, string, string, Execution, string) (Task, error) {
	s.retries.Add(1)
	if s.retryErr != nil {
		return Task{}, s.retryErr
	}
	return Task{State: Failed}, nil
}
func (s *workerMemoryStore) RecordImportRows(context.Context, Execution, []map[string]any) error {
	return s.progressErr
}
func (s *workerMemoryStore) MarkRowsCommitted(context.Context, Execution, int, int) error {
	return s.progressErr
}
func (s *workerMemoryStore) CommittedRows(context.Context, Execution) (int, error) {
	return s.committed, s.progressErr
}
func (s *workerMemoryStore) RecoverExpired(context.Context, int) (int, error) {
	s.recovered.Add(1)
	return 0, nil
}
func (s *workerMemoryStore) RecoverExpiredTask(context.Context, string, int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.task.DispatchVersion++
	s.task.State = Failed
	return nil
}
func (s *workerMemoryStore) TerminateExhausted(context.Context, string, string, string, int64) error {
	s.terminated.Add(1)
	return nil
}
func (s *workerMemoryStore) LegacyMessageSafe(context.Context, string, string, string) (bool, error) {
	return s.legacySafe, nil
}
func (s *workerMemoryStore) Quarantine(context.Context, []byte, map[string]any, string) error {
	s.quarantined.Add(1)
	return s.quarantineErr
}

type workerAssets struct {
	export func(context.Context) ([]asset.Record, error)
	batch  func(context.Context, []asset.ImportRow) (asset.BatchResult, error)
	limits func(context.Context) (asset.BatchLimits, error)
}

func (a workerAssets) BatchLimits(ctx context.Context) (asset.BatchLimits, error) {
	if a.limits != nil {
		return a.limits(ctx)
	}
	return asset.BatchLimits{MaxRows: asset.MaxImportBatchRows, MaxBodyBytes: 1 << 20}, nil
}

func (a workerAssets) Counts(context.Context, AssetQuery) ([]asset.Count, error) {
	return nil, nil
}

func (a workerAssets) Export(ctx context.Context, _ string, _ AssetQuery) ([]asset.Record, error) {
	if a.export != nil {
		return a.export(ctx)
	}
	return nil, nil
}
func (a workerAssets) Batch(ctx context.Context, _ string, _ string, _ string, _ string, rows []asset.ImportRow) (asset.BatchResult, error) {
	if a.batch != nil {
		return a.batch(ctx, rows)
	}
	return asset.BatchResult{Created: len(rows)}, nil
}

type workerObjects struct {
	mu   sync.Mutex
	keys []string
}

func (o *workerObjects) Put(ctx context.Context, key string, _ []byte, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.keys = append(o.keys, key)
	return nil
}
func (o *workerObjects) Get(context.Context, string) (io.ReadCloser, string, error) {
	return io.NopCloser(strings.NewReader("")), "", nil
}
func (o *workerObjects) Delete(context.Context, string) error { return nil }
func testWorker(s *workerMemoryStore, a workerAssets) *Worker {
	return NewWorker(s, nil, a, &workerObjects{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}
func workerDelivery(kind string, version int64) Delivery {
	body, _ := json.Marshal(Message{SchemaVersion: 2, EventID: workerEventID, TaskID: workerTaskID, Kind: kind, DispatchVersion: version})
	return Delivery{Body: body, MessageID: workerEventID, Headers: map[string]any{"event_id": workerEventID}, RoutingKey: "task.requested"}
}
func TestWorkerRejectsInvalidMessageIdentity(t *testing.T) {
	for _, test := range []struct {
		name   string
		modify func(*Delivery)
	}{
		{"unknown schema", func(d *Delivery) {
			d.Body = []byte(fmt.Sprintf(`{"schema_version":3,"event_id":%q,"task_id":%q,"kind":"export","dispatch_version":1}`, workerEventID, workerTaskID))
		}},
		{"invalid UUID", func(d *Delivery) {
			d.Body = []byte(`{"schema_version":2,"event_id":"x","task_id":"y","kind":"export","dispatch_version":1}`)
		}},
		{"missing AMQP id", func(d *Delivery) { d.MessageID = "" }},
		{"different AMQP id", func(d *Delivery) { d.MessageID = uuid() }},
		{"different header", func(d *Delivery) { d.Headers["event_id"] = uuid() }},
		{"header type", func(d *Delivery) { d.Headers["event_id"] = 42 }},
		{"bad kind", func(d *Delivery) {
			d.Body = []byte(fmt.Sprintf(`{"schema_version":2,"event_id":%q,"task_id":%q,"kind":"delete","dispatch_version":1}`, workerEventID, workerTaskID))
		}},
		{"missing dispatch", func(d *Delivery) {
			d.Body = []byte(fmt.Sprintf(`{"schema_version":2,"event_id":%q,"task_id":%q,"kind":"export"}`, workerEventID, workerTaskID))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newWorkerMemoryStore()
			w := testWorker(s, workerAssets{})
			d := workerDelivery("export", 1)
			test.modify(&d)
			action := w.process(context.Background(), d)
			if !action.ack || s.quarantined.Load() != 1 || s.claims.Load() != 0 {
				t.Fatalf("illegal message not durably quarantined: %+v", action)
			}
		})
	}
	s := newWorkerMemoryStore()
	s.quarantineErr = errors.New("database unavailable")
	action := testWorker(s, workerAssets{}).process(context.Background(), Delivery{Body: []byte(`bad`)})
	if action.ack {
		t.Fatal("quarantine DB failure was acknowledged")
	}
}
func TestWorkerOnlyAcknowledgesDurableOutcomes(t *testing.T) {
	failure := errors.New("database connection lost")
	for _, test := range []struct {
		name            string
		configure       func(*workerMemoryStore, *workerAssets)
		ack             bool
		retry, complete int32
	}{
		{name: "success", ack: true, complete: 1},
		{name: "unknown complete commit", configure: func(s *workerMemoryStore, a *workerAssets) { s.completeErr = failure }, complete: 1},
		{name: "business retry committed", configure: func(s *workerMemoryStore, a *workerAssets) {
			a.export = func(context.Context) ([]asset.Record, error) { return nil, errors.New("asset timeout") }
		}, ack: true, retry: 1},
		{name: "retry commit failed", configure: func(s *workerMemoryStore, a *workerAssets) {
			s.retryErr = failure
			a.export = func(context.Context) ([]asset.Record, error) { return nil, errors.New("asset timeout") }
		}, retry: 1},
		{name: "inbox DB error", configure: func(s *workerMemoryStore, a *workerAssets) { s.inboxErr = failure }},
		{name: "claim DB error", configure: func(s *workerMemoryStore, a *workerAssets) { s.claimErr = failure }},
		{name: "already committed", configure: func(s *workerMemoryStore, a *workerAssets) { s.seen = true }, ack: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newWorkerMemoryStore()
			a := workerAssets{}
			if test.configure != nil {
				test.configure(s, &a)
			}
			action := testWorker(s, a).process(context.Background(), workerDelivery("export", 1))
			if action.ack != test.ack || s.retries.Load() != test.retry || s.completes.Load() != test.complete {
				t.Fatalf("action=%+v retries=%d completes=%d", action, s.retries.Load(), s.completes.Load())
			}
		})
	}
}
func TestWorkerClassifiesUnclaimableTasks(t *testing.T) {
	for _, test := range []struct {
		name                  string
		configure             func(*Task)
		version               int64
		ack                   bool
		quarantine, terminate int32
		delayed               bool
	}{
		{name: "older dispatch", configure: func(task *Task) { task.DispatchVersion = 2 }, version: 1, ack: true},
		{name: "future dispatch", version: 2, ack: true, quarantine: 1},
		{name: "terminal", configure: func(task *Task) { task.State = Succeeded }, version: 1, ack: true},
		{name: "active holder", configure: func(task *Task) {
			task.State = Running
			until := task.ObservedAt.Add(time.Minute)
			task.LeaseUntil = &until
		}, version: 1, ack: true},
		{name: "expired holder", configure: func(task *Task) {
			task.State = Running
			until := task.ObservedAt.Add(-time.Minute)
			task.LeaseUntil = &until
		}, version: 1, ack: true},
		{name: "next run uses DB time", configure: func(task *Task) {
			task.ObservedAt = time.Now().Add(-time.Hour)
			due := task.ObservedAt.Add(time.Minute)
			task.NextRunAt = &due
		}, version: 1, delayed: true},
		{name: "exhausted", configure: func(task *Task) { task.Attempt = task.MaxAttempts }, version: 1, ack: true, terminate: 1},
		{name: "not confirmed", configure: func(task *Task) { task.State = AwaitingConfirmation }, version: 1, ack: true, quarantine: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newWorkerMemoryStore()
			if test.configure != nil {
				test.configure(&s.task)
			}
			action := testWorker(s, workerAssets{}).process(context.Background(), workerDelivery("export", test.version))
			if action.ack != test.ack || s.claims.Load() != 0 || s.quarantined.Load() != test.quarantine || s.terminated.Load() != test.terminate || (test.delayed && action.delay != time.Minute) {
				t.Fatalf("action=%+v claims=%d quarantined=%d terminated=%d", action, s.claims.Load(), s.quarantined.Load(), s.terminated.Load())
			}
		})
	}
}
func TestLegacyDeliveryRequiresDurableIdentityAndReplacement(t *testing.T) {
	d := Delivery{Body: []byte(fmt.Sprintf(`{"task_id":%q,"kind":"export"}`, workerTaskID)), Headers: map[string]any{"event_id": workerEventID}}
	for _, test := range []struct {
		name        string
		allow, safe bool
		quarantine  int32
	}{{"disabled", false, true, 1}, {"unverified", true, false, 1}, {"safe replacement", true, true, 0}} {
		t.Run(test.name, func(t *testing.T) {
			s := newWorkerMemoryStore()
			s.legacySafe = test.safe
			w := testWorker(s, workerAssets{})
			w.config.AllowLegacyMessages = test.allow
			action := w.process(context.Background(), d)
			if !action.ack || s.claims.Load() != 0 || s.quarantined.Load() != test.quarantine {
				t.Fatalf("action=%+v", action)
			}
		})
	}
}
func TestLeaseRenewalFailureCancelsExecutionWithoutRetry(t *testing.T) {
	for _, failure := range []error{ErrLeaseLost, errors.New("database unavailable")} {
		s := newWorkerMemoryStore()
		s.renewErr = failure
		a := workerAssets{export: func(ctx context.Context) ([]asset.Record, error) { <-ctx.Done(); return nil, ctx.Err() }}
		w := testWorker(s, a)
		w.config.RenewInterval = 5 * time.Millisecond
		action := w.process(context.Background(), workerDelivery("export", 1))
		if action.ack || s.retries.Load() != 0 || s.completes.Load() != 0 || s.renewals.Load() == 0 {
			t.Fatalf("lease failure caused mutation: %+v", action)
		}
	}
}
func TestTaskDeadlineRetriesWithLiveFinalizationContext(t *testing.T) {
	s := newWorkerMemoryStore()
	a := workerAssets{export: func(ctx context.Context) ([]asset.Record, error) { <-ctx.Done(); return nil, ctx.Err() }}
	w := testWorker(s, a)
	w.config.TaskTimeout = 5 * time.Millisecond
	action := w.process(context.Background(), workerDelivery("export", 1))
	if !action.ack || s.retries.Load() != 1 {
		t.Fatalf("deadline did not durably retry: %+v", action)
	}
}
func TestImportReplayPreservesCumulativeCommittedResult(t *testing.T) {
	s := newWorkerMemoryStore()
	s.committed = 2
	s.task.Kind = "import"
	s.task.Input = map[string]any{"csv": "department_name,department_code\nResearch,RD\nOperations,OP\n"}
	a := workerAssets{batch: func(ctx context.Context, rows []asset.ImportRow) (asset.BatchResult, error) {
		if len(rows) != 2 || rows[0].Key != workerTaskID+":2" || rows[1].Key != workerTaskID+":3" {
			t.Fatalf("unstable replay keys: %+v", rows)
		}
		return asset.BatchResult{Skipped: 2}, nil
	}}
	action := testWorker(s, a).process(context.Background(), workerDelivery("import", 1))
	if !action.ack || s.result["created"] != 0 || s.result["skipped"] != 2 || s.result["committed"] != 2 {
		t.Fatalf("result=%+v action=%+v", s.result, action)
	}
}
func TestImportProgressDatabaseFailureDoesNotRetry(t *testing.T) {
	s := newWorkerMemoryStore()
	s.progressErr = errors.New("unknown commit")
	s.task.Kind = "import"
	s.task.Input = map[string]any{"csv": "department_name,department_code\nResearch,RD\n"}
	action := testWorker(s, workerAssets{}).process(context.Background(), workerDelivery("import", 1))
	if action.ack || s.retries.Load() != 0 {
		t.Fatalf("database failure became retry: %+v", action)
	}
}
func TestExportObjectsAreIsolatedByExecutionToken(t *testing.T) {
	s := newWorkerMemoryStore()
	w := testWorker(s, workerAssets{})
	objects := w.objects.(*workerObjects)
	for _, token := range []string{"old-token", "new-token"} {
		result, err := w.runExport(context.Background(), Execution{Task: s.task, Token: token})
		if err != nil || result["object_key"] != "exports/"+workerTaskID+"/"+token+".csv" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
	if len(objects.keys) != 2 || objects.keys[0] == objects.keys[1] {
		t.Fatalf("keys not isolated: %v", objects.keys)
	}
}

type workerTestSubscription struct {
	ctx           context.Context
	cancel        context.CancelFunc
	deliveries    chan Delivery
	done          chan error
	stops, closes atomic.Int32
}

func newWorkerTestSubscription(ctx context.Context) *workerTestSubscription {
	child, cancel := context.WithCancel(ctx)
	return &workerTestSubscription{ctx: child, cancel: cancel, deliveries: make(chan Delivery, 1), done: make(chan error)}
}
func (s *workerTestSubscription) Context() context.Context    { return s.ctx }
func (s *workerTestSubscription) Done() <-chan error          { return s.done }
func (s *workerTestSubscription) Deliveries() <-chan Delivery { return s.deliveries }
func (s *workerTestSubscription) Stop() error                 { s.stops.Add(1); return nil }
func (s *workerTestSubscription) Close() error                { s.closes.Add(1); s.cancel(); return nil }

type workerTestBroker struct {
	mu       sync.Mutex
	sessions []*workerTestSubscription
	delivery Delivery
	acks     atomic.Int32
}

func (b *workerTestBroker) Subscribe(ctx context.Context, tag string) (Subscription, error) {
	s := newWorkerTestSubscription(ctx)
	d := b.delivery
	d.AckFunc = func() error { b.acks.Add(1); return nil }
	s.deliveries <- d
	b.mu.Lock()
	b.sessions = append(b.sessions, s)
	b.mu.Unlock()
	return s, nil
}
func waitForWorkerCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("worker condition timed out")
		}
		time.Sleep(time.Millisecond)
	}
}
func TestWorkerGracefulDrainKeepsContextsAndRecoveryIndependent(t *testing.T) {
	s := newWorkerMemoryStore()
	s.parallel = true
	entered := make(chan context.Context, 2)
	release := make(chan struct{})
	a := workerAssets{export: func(ctx context.Context) ([]asset.Record, error) {
		entered <- ctx
		select {
		case <-release:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	w := testWorker(s, a)
	w.config.RecoveryInterval = 5 * time.Millisecond
	w.config.RenewInterval = 5 * time.Millisecond
	w.config.DrainTimeout = time.Second
	broker := &workerTestBroker{delivery: workerDelivery("export", 1)}
	w.broker = broker
	intake, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); w.Run(intake) }()
	waitForWorkerCondition(t, func() bool { return len(entered) == 2 })
	contexts := []context.Context{<-entered, <-entered}
	waitForWorkerCondition(t, func() bool { return s.recovered.Load() >= 2 && s.renewals.Load() >= 2 })
	if s.claims.Load() != 2 || !w.Ready() {
		t.Fatal("worker slots did not execute concurrently")
	}
	cancel()
	waitForWorkerCondition(t, func() bool { return !w.Ready() })
	for _, ctx := range contexts {
		if ctx.Err() != nil {
			t.Fatal("SIGTERM prematurely cancelled active task")
		}
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not drain")
	}
	if broker.acks.Load() != 2 || s.completes.Load() != 2 || s.retries.Load() != 0 {
		t.Fatalf("acks=%d completes=%d retries=%d", broker.acks.Load(), s.completes.Load(), s.retries.Load())
	}
}
func TestWorkerDrainTimeoutCancelsWithoutAcknowledgementOrRetry(t *testing.T) {
	s := newWorkerMemoryStore()
	entered := make(chan struct{})
	a := workerAssets{export: func(ctx context.Context) ([]asset.Record, error) { close(entered); <-ctx.Done(); return nil, ctx.Err() }}
	w := testWorker(s, a)
	w.config.Slots = 1
	w.config.DrainTimeout = 10 * time.Millisecond
	broker := &workerTestBroker{delivery: workerDelivery("export", 1)}
	w.broker = broker
	intake, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); w.Run(intake) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not force drain")
	}
	if broker.acks.Load() != 0 || s.retries.Load() != 0 || s.completes.Load() != 0 {
		t.Fatalf("acks=%d retries=%d completes=%d", broker.acks.Load(), s.retries.Load(), s.completes.Load())
	}
}
func TestAckFailureNeverChangesCommittedSuccess(t *testing.T) {
	s := newWorkerMemoryStore()
	w := testWorker(s, workerAssets{})
	session := newWorkerTestSubscription(context.Background())
	defer session.Close()
	d := workerDelivery("export", 1)
	d.AckFunc = func() error { return errors.New("channel closed") }
	session.deliveries <- d
	_, _ = w.consumeSlot(context.Background(), session)
	if s.completes.Load() != 1 || s.retries.Load() != 0 || s.task.State != Succeeded {
		t.Fatal("ACK failure changed committed task")
	}
}
