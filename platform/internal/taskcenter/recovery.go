package taskcenter

import (
	"context"
	"time"
)

// Recovery is independent of slot availability and never performs external work
// or publishes messages. Each store call atomically revokes expired executions
// and creates their next dispatch or dead-letter Outbox event.
func (w *Worker) runRecovery(ctx context.Context) {
	ticker := time.NewTicker(w.config.RecoveryInterval)
	defer ticker.Stop()
	for {
		checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		count, err := w.store.RecoverExpired(checkCtx, 100)
		cancel()
		if err != nil && ctx.Err() == nil {
			w.logger.Error("recover expired task leases", "worker_id", w.workerID, "error", err)
		}
		if err == nil && count > 0 {
			w.leaseExpirations.Add(uint64(count))
			w.logger.Info("recovered expired task leases", "worker_id", w.workerID, "count", count)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
