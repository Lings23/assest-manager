package taskcenter

import "context"

func (s *Store) WorkerTask(ctx context.Context, id string) (Task, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback(ctx)
	t, err := scanTask(tx.QueryRow(ctx, baseSelect+` WHERE id=$1`, id))
	if err != nil {
		return Task{}, err
	}
	err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&t.ObservedAt)
	if err != nil {
		return Task{}, err
	}
	return t, tx.Commit(ctx)
}
func (s *Store) ConfirmAndEnqueue(ctx context.Context, id, user string) (Task, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback(ctx)
	t, err := scanTask(tx.QueryRow(ctx, `UPDATE task_report.tasks SET state='pending',progress=0,
 result=result||'{"confirmed":true}'::jsonb,input=input||'{"phase":"execution"}'::jsonb,attempt=0,dispatch_version=dispatch_version+1,next_run_at=clock_timestamp(),updated_at=clock_timestamp()
 WHERE id=$1 AND requested_by=$2 AND kind='import' AND state='awaiting_confirmation'
 AND COALESCE((result->>'error_count')::int,0)=0
 AND COALESCE(jsonb_array_length(CASE WHEN jsonb_typeof(result->'errors')='array' THEN result->'errors' ELSE '[]'::jsonb END),0)=0
 RETURNING `+columns, id, user))
	if err != nil {
		return Task{}, err
	}
	if err = enqueueTask(ctx, tx, t); err != nil {
		return Task{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Task{}, err
	}
	return t, nil
}
func (s *Store) InboxSeen(ctx context.Context, consumer, eventID string) (bool, error) {
	var seen bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_report.inbox WHERE consumer=$1 AND event_id=$2)`, consumer, eventID).Scan(&seen)
	return seen, err
}

// FailPermanent is only for synchronous import preflight; async writes need a token.
func (s *Store) FailPermanent(ctx context.Context, id, message string) (Task, error) {
	message = quarantineText(message, 4096)
	return scanTask(s.pool.QueryRow(ctx, `UPDATE task_report.tasks SET state='failed',last_error=$2,updated_at=clock_timestamp()
 WHERE id=$1 AND kind='import' AND dispatch_version=0 AND state='running' RETURNING `+columns, id, message))
}
func (s *Store) UpsertCount(ctx context.Context, assetType, departmentID, scope string, total int64) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO task_report.permission_counts(asset_type,requester_id,scope_key,total) VALUES($1,$2,$3,$4)
 ON CONFLICT(asset_type,requester_id,scope_key) DO UPDATE SET total=EXCLUDED.total,refreshed_at=now()`, assetType, departmentID, scope, total)
	return err
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
