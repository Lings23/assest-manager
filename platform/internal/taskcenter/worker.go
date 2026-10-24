package taskcenter

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"asset-platform/internal/asset"
	"asset-platform/internal/assetschema"
	"asset-platform/internal/servicekit"
)

// workerStore separates durable decisions from delivery acknowledgements.
type workerStore interface {
	InboxSeen(context.Context, string, string) (bool, error)
	WorkerTask(context.Context, string) (Task, error)
	ClaimTask(context.Context, string, int64, string, time.Duration) (Execution, error)
	RenewLease(context.Context, Execution, time.Duration) error
	Complete(context.Context, string, string, Execution, map[string]any) error
	RetryOrDead(context.Context, string, string, Execution, string) (Task, error)
	FailExecution(context.Context, string, string, Execution, string) (Task, error)
	RecordImportRows(context.Context, Execution, []map[string]any) error
	MarkRowsCommitted(context.Context, Execution, int, int) error
	CommittedRows(context.Context, Execution) (int, error)
	RecoverExpired(context.Context, int) (int, error)
	RecoverExpiredTask(context.Context, string, int64) error
	TerminateExhausted(context.Context, string, string, string, int64) error
	LegacyMessageSafe(context.Context, string, string, string) (bool, error)
	Quarantine(context.Context, []byte, map[string]any, string) error
}

type WorkerConfig struct {
	Slots               int
	LeaseDuration       time.Duration
	RenewInterval       time.Duration
	TaskTimeout         time.Duration
	RecoveryInterval    time.Duration
	DrainTimeout        time.Duration
	AllowLegacyMessages bool
}

func DefaultWorkerConfig() WorkerConfig {
	return WorkerConfig{Slots: 2, LeaseDuration: 90 * time.Second, RenewInterval: 20 * time.Second, TaskTimeout: 20 * time.Minute, RecoveryInterval: 10 * time.Second, DrainTimeout: time.Minute}
}

type Worker struct {
	store              workerStore
	broker             ConsumerBroker
	assets             AssetClient
	objects            ObjectStore
	logger             *slog.Logger
	consumer, workerID string
	config             WorkerConfig
	stopping           atomic.Bool
	activeSlots        atomic.Int32
	claimConflicts     atomic.Uint64
	leaseExpirations   atomic.Uint64
	activeExecutions   atomic.Int64
	executionDurations [2]executionDurationMetrics
	sessionMu          sync.Mutex
	sessions           map[int]Subscription
}

func NewWorker(store workerStore, broker ConsumerBroker, assets AssetClient, objects ObjectStore, logger *slog.Logger) *Worker {
	w, _ := NewWorkerWithConfig(store, broker, assets, objects, logger, DefaultWorkerConfig())
	return w
}
func NewWorkerWithConfig(store workerStore, broker ConsumerBroker, assets AssetClient, objects ObjectStore, logger *slog.Logger, cfg WorkerConfig) (*Worker, error) {
	if cfg.Slots < 1 || cfg.Slots > 64 || cfg.LeaseDuration <= 0 || cfg.RenewInterval <= 0 || cfg.RenewInterval >= cfg.LeaseDuration || cfg.TaskTimeout <= 0 || cfg.RecoveryInterval <= 0 || cfg.DrainTimeout <= 0 {
		return nil, fmt.Errorf("invalid worker configuration")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{store: store, broker: broker, assets: assets, objects: objects, logger: logger, consumer: "task-report-worker-v2", workerID: uuid(), config: cfg, sessions: make(map[int]Subscription)}, nil
}
func (w *Worker) Ready() bool {
	if w.stopping.Load() || int(w.activeSlots.Load()) != w.config.Slots {
		return false
	}
	w.sessionMu.Lock()
	defer w.sessionMu.Unlock()
	if len(w.sessions) != w.config.Slots {
		return false
	}
	for _, session := range w.sessions {
		if session.Context().Err() != nil {
			return false
		}
	}
	return true
}

// The supplied context stops intake only. Existing executions keep renewing until
// they finish or the bounded drain expires; a signal never revokes them early.
func (w *Worker) Run(intakeCtx context.Context) {
	runCtx, forceStop := context.WithCancel(context.Background())
	defer forceStop()
	recoveryCtx, stopRecovery := context.WithCancel(runCtx)
	recoveryDone := make(chan struct{})
	go func() { defer close(recoveryDone); w.runRecovery(recoveryCtx) }()
	var slots sync.WaitGroup
	for slot := 0; slot < w.config.Slots; slot++ {
		slots.Add(1)
		go func() { defer slots.Done(); w.runSlot(intakeCtx, runCtx, slot) }()
	}
	<-intakeCtx.Done()
	w.stopping.Store(true)
	stopRecovery()
	timer := time.NewTimer(w.config.DrainTimeout)
	defer timer.Stop()
	w.controlSessions(false)
	drained := make(chan struct{})
	go func() { slots.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-timer.C:
		w.logger.Warn("worker drain expired", "worker_id", w.workerID)
		forceStop()
		w.controlSessions(true)
		<-drained
	}
	forceStop()
	<-recoveryDone
}
func (w *Worker) controlSessions(closeChannel bool) {
	w.sessionMu.Lock()
	sessions := make([]Subscription, 0, len(w.sessions))
	for _, session := range w.sessions {
		sessions = append(sessions, session)
	}
	w.sessionMu.Unlock()
	var controls sync.WaitGroup
	for _, session := range sessions {
		controls.Add(1)
		go func() {
			defer controls.Done()
			var err error
			if closeChannel {
				err = session.Close()
			} else {
				err = session.Stop()
			}
			if err != nil {
				w.logger.Warn("stop AMQP subscription", "worker_id", w.workerID, "error", err)
			}
		}()
	}
	controls.Wait()
}
func (w *Worker) runSlot(intakeCtx, runCtx context.Context, slot int) {
	failures := 0
	for intakeCtx.Err() == nil && runCtx.Err() == nil {
		session, err := w.broker.Subscribe(runCtx, fmt.Sprintf("%s-%d", w.workerID, slot))
		if err != nil {
			w.logger.Error("subscribe AMQP", "worker_id", w.workerID, "slot", slot, "error", err)
			failures++
			if !waitWorker(intakeCtx, workerBackoff(failures)) {
				return
			}
			continue
		}
		w.sessionMu.Lock()
		w.sessions[slot] = session
		w.sessionMu.Unlock()
		w.activeSlots.Add(1)
		delay, healthy := w.consumeSlot(intakeCtx, session)
		w.activeSlots.Add(-1)
		_ = session.Close()
		w.sessionMu.Lock()
		delete(w.sessions, slot)
		w.sessionMu.Unlock()
		if intakeCtx.Err() != nil || runCtx.Err() != nil {
			return
		}
		if healthy {
			failures = 0
		}
		failures++
		backoff := workerBackoff(failures)
		if delay < backoff {
			delay = backoff
		}
		if !waitWorker(intakeCtx, delay) {
			return
		}
	}
}
func (w *Worker) consumeSlot(intakeCtx context.Context, session Subscription) (time.Duration, bool) {
	healthy := false
	for {
		select {
		case <-intakeCtx.Done():
			return 0, healthy
		case <-session.Context().Done():
			return 0, healthy
		case err := <-session.Done():
			if err != nil {
				w.logger.Error("AMQP subscription closed", "worker_id", w.workerID, "error", err)
			}
			return 0, healthy
		case delivery, ok := <-session.Deliveries():
			if !ok || intakeCtx.Err() != nil {
				return 0, healthy
			}
			action := w.process(session.Context(), delivery)
			if !action.ack {
				w.logger.Warn("delivery left unacknowledged", "worker_id", w.workerID, "message_id", delivery.MessageID, "error", action.err)
				return action.delay, healthy
			}
			if err := delivery.Ack(); err != nil {
				w.logger.Warn("ack AMQP delivery", "worker_id", w.workerID, "message_id", delivery.MessageID, "error", err)
				return 0, healthy
			}
			healthy = true
		}
	}
}

type deliveryAction struct {
	ack   bool
	delay time.Duration
	err   error
}

func (w *Worker) process(ctx context.Context, delivery Delivery) deliveryAction {
	decisionCtx, cancelDecision := context.WithTimeout(ctx, 10*time.Second)
	defer cancelDecision()
	message, legacy, err := decodeTaskDelivery(delivery, w.config.AllowLegacyMessages)
	if err != nil {
		return w.quarantine(decisionCtx, delivery, err.Error())
	}
	if legacy {
		safe, err := w.store.LegacyMessageSafe(decisionCtx, message.EventID, message.TaskID, message.Kind)
		if err != nil {
			return deliveryAction{err: err}
		}
		if !safe {
			return w.quarantine(decisionCtx, delivery, "legacy event identity or replacement could not be verified")
		}
		return deliveryAction{ack: true}
	}
	seen, err := w.store.InboxSeen(decisionCtx, w.consumer, message.EventID)
	if err != nil {
		return deliveryAction{err: err}
	}
	if seen {
		return deliveryAction{ack: true}
	}
	for observation := 0; observation < 4; observation++ {
		task, err := w.store.WorkerTask(decisionCtx, message.TaskID)
		if errors.Is(err, ErrNotFound) {
			return w.quarantine(decisionCtx, delivery, "task does not exist")
		}
		if err != nil {
			return deliveryAction{err: err}
		}
		if task.Kind != message.Kind {
			return w.quarantine(decisionCtx, delivery, "message kind differs from task")
		}
		if message.DispatchVersion > task.DispatchVersion {
			return w.quarantine(decisionCtx, delivery, "message dispatch version is in the future")
		}
		if message.DispatchVersion < task.DispatchVersion || terminalTask(task.State) {
			return deliveryAction{ack: true}
		}
		now := task.ObservedAt
		if now.IsZero() {
			return deliveryAction{err: fmt.Errorf("task observation has no database timestamp")}
		}
		if task.State == Running {
			if task.LeaseUntil != nil && task.LeaseUntil.After(now) {
				return deliveryAction{ack: true}
			}
			if err := w.store.RecoverExpiredTask(decisionCtx, task.ID, task.DispatchVersion); err == nil {
				w.leaseExpirations.Add(1)
			} else if !errors.Is(err, ErrConflict) {
				return deliveryAction{err: err}
			}
			continue
		}
		if task.State != Pending && task.State != Failed {
			return w.quarantine(decisionCtx, delivery, "task is not scheduled for execution")
		}
		if task.Attempt >= task.MaxAttempts {
			if err := w.store.TerminateExhausted(decisionCtx, w.consumer, message.EventID, task.ID, task.DispatchVersion); errors.Is(err, ErrConflict) {
				continue
			} else if err != nil {
				return deliveryAction{err: err}
			}
			return deliveryAction{ack: true}
		}
		if task.NextRunAt == nil {
			return deliveryAction{err: fmt.Errorf("scheduled task has no next_run_at")}
		}
		if task.NextRunAt.After(now) {
			return deliveryAction{delay: task.NextRunAt.Sub(now), err: fmt.Errorf("task is not due yet")}
		}
		if w.stopping.Load() {
			return deliveryAction{err: context.Canceled}
		}
		execution, err := w.store.ClaimTask(decisionCtx, task.ID, message.DispatchVersion, w.workerID, w.config.LeaseDuration)
		if errors.Is(err, ErrConflict) {
			w.claimConflicts.Add(1)
			continue
		}
		if err != nil {
			return deliveryAction{err: err}
		}
		if execution.NextRunAt != nil {
			servicekit.MetricsFor("task-worker").ObserveStage("dispatch_wait", max(time.Duration(0), execution.LeaseUntil.Add(-w.config.LeaseDuration).Sub(*execution.NextRunAt)), nil)
		}
		return w.execute(ctx, message, execution)
	}
	return deliveryAction{err: ErrConflict}
}
func terminalTask(state State) bool {
	return state == Succeeded || state == Cancelled || state == DeadLetter
}
func (w *Worker) quarantine(ctx context.Context, delivery Delivery, reason string) deliveryAction {
	metadata := map[string]any{"message_id": delivery.MessageID, "headers": delivery.Headers, "routing_key": delivery.RoutingKey, "redelivered": delivery.Redelivered}
	if err := w.store.Quarantine(ctx, delivery.Body, metadata, reason); err != nil {
		return deliveryAction{err: err}
	}
	w.logger.Warn("quarantined task message", "worker_id", w.workerID, "message_id", delivery.MessageID, "reason", reason)
	return deliveryAction{ack: true}
}
func decodeTaskDelivery(delivery Delivery, allowLegacy bool) (Message, bool, error) {
	var message Message
	if len(delivery.Body) > 64<<10 {
		return message, false, fmt.Errorf("task message exceeds size limit")
	}
	if err := json.Unmarshal(delivery.Body, &message); err != nil {
		return message, false, fmt.Errorf("invalid task JSON: %w", err)
	}
	legacy := message.SchemaVersion == 0 || message.SchemaVersion == 1
	if message.SchemaVersion != 2 && !(legacy && allowLegacy) {
		return message, false, fmt.Errorf("unsupported message schema version %d", message.SchemaVersion)
	}
	headerID, hasHeader := delivery.Headers["event_id"]
	headerString, stringHeader := headerID.(string)
	if hasHeader && !stringHeader {
		return message, false, fmt.Errorf("event_id header must be a string")
	}
	if legacy && message.EventID == "" {
		message.EventID = delivery.MessageID
		if message.EventID == "" {
			message.EventID = headerString
		}
	}
	if !validTaskUUID(message.EventID) || !validTaskUUID(message.TaskID) {
		return message, false, fmt.Errorf("event_id and task_id must be UUIDs")
	}
	if message.Kind != "import" && message.Kind != "export" {
		return message, false, fmt.Errorf("unsupported task kind")
	}
	if !legacy && delivery.MessageID == "" {
		return message, false, fmt.Errorf("AMQP message_id is required")
	}
	if delivery.MessageID != "" && !strings.EqualFold(delivery.MessageID, message.EventID) {
		return message, false, fmt.Errorf("AMQP message_id differs from event_id")
	}
	if hasHeader && !strings.EqualFold(headerString, message.EventID) {
		return message, false, fmt.Errorf("event_id header differs from message")
	}
	if delivery.RoutingKey != "" && delivery.RoutingKey != "task.requested" {
		return message, false, fmt.Errorf("unexpected routing key")
	}
	if (!legacy && message.DispatchVersion < 1) || (legacy && message.DispatchVersion != 0) {
		return message, false, fmt.Errorf("invalid dispatch version")
	}
	return message, legacy, nil
}
func validTaskUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	return err == nil
}

// Store failures have an unknown commit outcome and must never become another
// business retry. Recovery owns any execution whose durable outcome is unknown.
type executionStoreError struct{ error }

func (e executionStoreError) Unwrap() error { return e.error }

func (w *Worker) execute(ctx context.Context, message Message, execution Execution) deliveryAction {
	started := time.Now()
	w.activeExecutions.Add(1)
	defer func() {
		kind := 1
		if execution.Kind == "import" {
			kind = 0
		}
		w.executionDurations[kind].observe(time.Since(started))
		w.activeExecutions.Add(-1)
	}()
	logger := w.logger.With("task_id", execution.ID, "event_id", message.EventID, "dispatch_version", execution.DispatchVersion, "worker_id", w.workerID, "execution_token", execution.Token)
	taskCtx, timeoutCancel := context.WithTimeout(ctx, w.config.TaskTimeout)
	defer timeoutCancel()
	execCtx, revoke := context.WithCancelCause(taskCtx)
	defer revoke(nil)
	renewCtx, stopRenew := context.WithCancel(execCtx)
	renewDone := make(chan struct{})
	go func() {
		defer close(renewDone)
		ticker := time.NewTicker(w.config.RenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-renewCtx.Done():
				return
			case <-ticker.C:
				checkCtx, cancel := context.WithTimeout(renewCtx, 10*time.Second)
				err := w.store.RenewLease(checkCtx, execution, w.config.LeaseDuration)
				cancel()
				if err != nil {
					if renewCtx.Err() == nil {
						logger.Error("execution lease could not be renewed", "error", err)
						revoke(executionStoreError{err})
					}
					return
				}
			}
		}
	}()
	var result map[string]any
	var err error
	if isPreflight(execution.Task) {
		result, err = w.runPreflight(execCtx, execution)
	} else if execution.Kind == "import" {
		result, err = w.runImport(execCtx, execution)
	} else {
		result, err = w.runExport(execCtx, execution)
	}
	stopRenew()
	<-renewDone
	if ctx.Err() != nil {
		return deliveryAction{err: ctx.Err()}
	}
	var storeErr executionStoreError
	if errors.As(err, &storeErr) || errors.Is(err, ErrLeaseLost) {
		return deliveryAction{err: err}
	}
	if cause := context.Cause(execCtx); cause != nil {
		if !errors.Is(cause, context.DeadlineExceeded) {
			return deliveryAction{err: cause}
		}
		err = cause
	}
	commitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err == nil {
		// Complete may have committed despite an error; never call RetryOrDead.
		if err := w.store.Complete(commitCtx, w.consumer, message.EventID, execution, result); err != nil {
			return deliveryAction{err: err}
		}
		logger.Info("task succeeded", "duration_ms", time.Since(started).Milliseconds())
		return deliveryAction{ack: true}
	}
	var failed Task
	var failErr error
	if IsPermanentTaskError(err) {
		failed, failErr = w.store.FailExecution(commitCtx, w.consumer, message.EventID, execution, err.Error())
	} else {
		failed, failErr = w.store.RetryOrDead(commitCtx, w.consumer, message.EventID, execution, err.Error())
	}
	if failErr != nil {
		return deliveryAction{err: failErr}
	}
	logger.Warn("task execution failed", "state", failed.State, "error", err, "duration_ms", time.Since(started).Milliseconds())
	return deliveryAction{ack: true}
}
func (w *Worker) runImport(ctx context.Context, execution Execution) (map[string]any, error) {
	task := execution.Task
	var source io.Reader
	var body io.ReadCloser
	if key := stringInput(task.Input, "source_object_key"); key != "" {
		var err error
		body, _, err = w.objects.Get(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("read import source: %w", err)
		}
		defer body.Close()
		source = body
	} else {
		raw, ok := task.Input["csv"].(string)
		if !ok {
			return nil, PermanentFailure(fmt.Errorf("import payload is missing"))
		}
		source = strings.NewReader(raw)
	}
	metrics := servicekit.MetricsFor("task-worker")
	stageStarted := time.Now()
	check, err := PreflightCSV(task.AssetType, source, true)
	metrics.ObserveStage("import_parse", time.Since(stageStarted), err)
	if err != nil {
		return nil, err
	}
	if len(check.Errors) > 0 {
		return nil, PermanentFailure(fmt.Errorf("import contains %d invalid rows", len(check.Errors)))
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	limits, err := w.assets.BatchLimits(ctx)
	if err != nil {
		return nil, fmt.Errorf("read asset batch limits: %w", err)
	}
	batcher, err := newImportBatcher(task, limits)
	if err != nil {
		return nil, err
	}
	stageStarted = time.Now()
	err = w.store.RecordImportRows(servicekit.WithDatabaseOperation(ctx, "task_record_rows"), execution, check.Rows)
	metrics.ObserveStage("import_record_rows", time.Since(stageStarted), err)
	if err != nil {
		return nil, executionStoreError{err}
	}
	created, skipped := 0, 0
	for start := 0; start < len(check.Rows); {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rows, end, requestBytes, err := batcher.next(check.Rows, start)
		if err != nil {
			return nil, err
		}
		w.logger.Debug("import batch encoded", "task_id", task.ID, "first_csv_row", start+2, "rows", len(rows), "request_bytes", requestBytes)
		stageStarted = time.Now()
		batch, batchErr := w.assets.Batch(ctx, task.ID, task.AssetType, task.RequestedBy, task.DepartmentID, rows)
		metrics.ObserveStage("import_asset_batch", time.Since(stageStarted), batchErr)
		if batchErr != nil {
			return nil, batchErr
		}
		created += batch.Created
		skipped += batch.Skipped
		stageStarted = time.Now()
		err = w.store.MarkRowsCommitted(servicekit.WithDatabaseOperation(ctx, "task_mark_progress"), execution, start+2, end+1)
		metrics.ObserveStage("import_mark_progress", time.Since(stageStarted), err)
		if err != nil {
			return nil, executionStoreError{err}
		}
		start = end
	}
	stageStarted = time.Now()
	committed, err := w.store.CommittedRows(servicekit.WithDatabaseOperation(ctx, "task_final_audit"), execution)
	metrics.ObserveStage("import_final_audit", time.Since(stageStarted), err)
	if err != nil {
		return nil, executionStoreError{err}
	}
	return map[string]any{"total": check.Total, "created": created, "skipped": skipped, "committed": committed}, nil
}
func (w *Worker) runExport(ctx context.Context, execution Execution) (map[string]any, error) {
	task := execution.Task
	query := AssetQuery{OwnerID: stringInput(task.Input, "owner_id"), DepartmentIDs: stringSliceInput(task.Input, "department_ids"), Global: boolInput(task.Input, "global"), ViewSensitive: boolInput(task.Input, "view_sensitive")}
	metrics := servicekit.MetricsFor("task-worker")
	stageStarted := time.Now()
	records, err := w.assets.Export(ctx, task.AssetType, query)
	metrics.ObserveStage("export_read", time.Since(stageStarted), err)
	if err != nil {
		return nil, err
	}
	stageStarted = time.Now()
	data, err := exportCSV(task.AssetType, records)
	metrics.ObserveStage("export_encode", time.Since(stageStarted), err)
	if err != nil {
		return nil, PermanentFailure(err)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	key := "exports/" + task.ID + "/" + execution.Token + ".csv"
	stageStarted = time.Now()
	err = w.objects.Put(ctx, key, data, "text/csv; charset=utf-8")
	metrics.ObserveStage("export_put", time.Since(stageStarted), err)
	if err != nil {
		return nil, err
	}
	return map[string]any{"rows": len(records), "object_key": key, "download_url": "/api/v1/export-jobs/" + task.ID + "/download"}, nil
}
func workerBackoff(failures int) time.Duration {
	base := time.Duration(1<<min(failures-1, 5)) * time.Second
	return base + time.Duration(rand.Int64N(int64(base/4)+1))
}
func waitWorker(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func exportCSV(assetType string, records []asset.Record) ([]byte, error) {
	schema, ok := assetschema.Types[assetType]
	if !ok {
		return nil, asset.ErrUnknownType
	}
	fields := make([]assetschema.Field, 0)
	var buffer bytes.Buffer
	buffer.Write([]byte{0xEF, 0xBB, 0xBF})
	writer := csv.NewWriter(&buffer)
	header := []string{"id", "owner_id", "department_id"}
	for _, field := range schema.Fields {
		if field.Export {
			fields = append(fields, field)
			header = append(header, field.Name)
		}
	}
	if err := writer.Write(header); err != nil {
		return nil, err
	}
	for _, record := range records {
		row := []string{record.ID, record.OwnerID, record.DepartmentID}
		for _, field := range fields {
			row = append(row, csvString(record.Fields[field.Name]))
		}
		if err := writer.Write(row); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	return buffer.Bytes(), writer.Error()
}
func csvString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	default:
		return fmt.Sprint(typed)
	}
}
func stringInput(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}
func boolInput(values map[string]any, key string) bool { value, _ := values[key].(bool); return value }
func stringSliceInput(values map[string]any, key string) []string {
	raw, ok := values[key].([]any)
	if !ok {
		if typed, yes := values[key].([]string); yes {
			return typed
		}
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if value, yes := item.(string); yes {
			out = append(out, value)
		}
	}
	return out
}
