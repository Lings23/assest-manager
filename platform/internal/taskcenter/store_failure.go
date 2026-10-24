package taskcenter

import (
	"context"
	"errors"
)

// FailExecution durably terminates a permanent business failure, regardless of
// the remaining attempt budget. The task's lease and token protect all writes
// in the same transaction, including the dead notification and consumed event.
func (s *Store) FailExecution(ctx context.Context, consumer, eventID string, e Execution, reason string) (Task, error) {
	reason = quarantineText(reason, 4096)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = lockExecution(ctx, tx, e); err != nil {
		return Task{}, err
	}
	task, err := scanTask(tx.QueryRow(ctx, `UPDATE task_report.tasks
 SET state='dead_letter',next_run_at=NULL,last_error=$5,
 worker_id=NULL,execution_token=NULL,lease_until=NULL,updated_at=clock_timestamp()
 WHERE `+executionPredicate+` RETURNING `+columns, append(execArgs(e), reason)...))
	if errors.Is(err, ErrNotFound) {
		return Task{}, ErrLeaseLost
	}
	if err != nil {
		return Task{}, err
	}
	if err = deadTask(ctx, tx, task, reason); err != nil {
		return Task{}, err
	}
	if err = inbox(ctx, tx, consumer, eventID, string(DeadLetter)); err != nil {
		return Task{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Task{}, err
	}
	return task, nil
}
