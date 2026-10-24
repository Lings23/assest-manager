package taskcenter

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrOutboxClaimLost = errors.New("outbox claim lost")

type OutboxEvent struct {
	ID              int64
	EventID         string
	Topic           string
	AggregateID     string
	Payload         []byte
	Attempts        int
	DispatchVersion *int64
	ClaimedBy       string
	ClaimToken      string
	ClaimUntil      time.Time
}

// ClaimOutbox acquires only the currently idle publisher slots. The statement
// commits before broker I/O; the claim token fences writes after takeover.
func (s *Store) ClaimOutbox(ctx context.Context, publisherID string, limit int, lease time.Duration) ([]OutboxEvent, error) {
	if publisherID == "" || limit < 1 || limit > 100 || lease.Milliseconds() < 1 {
		return nil, ErrInvalid
	}
	rows, err := s.pool.Query(ctx, `WITH candidates AS (
	 SELECT id FROM task_report.outbox
	 WHERE published_at IS NULL AND superseded_at IS NULL
	   AND available_at<=clock_timestamp()
	   AND (claim_until IS NULL OR claim_until<=clock_timestamp())
	 ORDER BY available_at,id LIMIT $1 FOR UPDATE SKIP LOCKED
	)
	UPDATE task_report.outbox o SET claimed_by=$2,claim_token=$3::uuid,
	 claim_until=clock_timestamp()+$4::bigint*interval '1 millisecond'
	FROM candidates c WHERE o.id=c.id
	RETURNING o.id,o.event_id::text,o.topic,o.aggregate_id::text,o.payload,o.attempts,
	 o.dispatch_version,o.claimed_by,o.claim_token::text,o.claim_until`, limit, publisherID, uuid(), lease.Milliseconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []OutboxEvent
	for rows.Next() {
		var event OutboxEvent
		if err = rows.Scan(&event.ID, &event.EventID, &event.Topic, &event.AggregateID, &event.Payload, &event.Attempts, &event.DispatchVersion, &event.ClaimedBy, &event.ClaimToken, &event.ClaimUntil); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) MarkPublished(ctx context.Context, event OutboxEvent) error {
	return s.writeOutboxClaim(ctx, event, `UPDATE task_report.outbox SET published_at=clock_timestamp(),
	 last_error='',claimed_by=NULL,claim_token=NULL,claim_until=NULL
	WHERE id=$1 AND claim_token=$2::uuid AND claimed_by=$3 AND claim_until>clock_timestamp()
	 AND published_at IS NULL AND superseded_at IS NULL`)
}

func (s *Store) MarkPublishFailed(ctx context.Context, event OutboxEvent, reason string) error {
	return s.writeOutboxClaim(ctx, event, `UPDATE task_report.outbox SET attempts=attempts+1,last_error=$4,
	 available_at=clock_timestamp()+LEAST(interval '5 minutes',interval '1 second'*power(2,LEAST(attempts,8))),
	 claimed_by=NULL,claim_token=NULL,claim_until=NULL
	WHERE id=$1 AND claim_token=$2::uuid AND claimed_by=$3 AND claim_until>clock_timestamp()
	 AND published_at IS NULL AND superseded_at IS NULL`, limitOutboxReason(reason))
}

// Lock only by ID, then evaluate the lease using fresh database time. UPDATE's
// pre-lock WHERE predicate alone can be stale if another transaction held an
// unchanged row lock until after the claim expired.
func (s *Store) writeOutboxClaim(ctx context.Context, event OutboxEvent, statement string, extra ...any) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var id int64
	err = tx.QueryRow(ctx, `SELECT id FROM task_report.outbox WHERE id=$1 FOR UPDATE`, event.ID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrOutboxClaimLost
	}
	if err != nil {
		return err
	}
	args := append([]any{event.ID, event.ClaimToken, event.ClaimedBy}, extra...)
	tag, err := tx.Exec(ctx, statement, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrOutboxClaimLost
	}
	return tx.Commit(ctx)
}

func limitOutboxReason(reason string) string {
	chars := []rune(reason)
	if len(chars) > 4096 {
		chars = chars[:4096]
	}
	return string(chars)
}
