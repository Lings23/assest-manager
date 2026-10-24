package taskcenter

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync/atomic"
	"time"
)

type PublisherStore interface {
	ClaimOutbox(context.Context, string, int, time.Duration) ([]OutboxEvent, error)
	MarkPublished(context.Context, OutboxEvent) error
	MarkPublishFailed(context.Context, OutboxEvent, string) error
}

type Publisher struct {
	store     PublisherStore
	broker    PublisherBroker
	ID        string
	Interval  time.Duration
	Lease     time.Duration
	OnError   func(error)
	published atomic.Uint64
	failures  atomic.Uint64
}

func NewPublisher(store PublisherStore, broker PublisherBroker) *Publisher {
	return &Publisher{store: store, broker: broker, ID: "publisher-" + uuid(), Interval: 500 * time.Millisecond, Lease: 30 * time.Second}
}

func (p *Publisher) Ready(ctx context.Context) error {
	if check, ok := p.store.(interface{ Ping(context.Context) error }); ok {
		if err := check.Ping(ctx); err != nil {
			return err
		}
	}
	if check, ok := p.broker.(interface{ Ready(context.Context) error }); ok {
		return check.Ready(ctx)
	}
	return nil
}

func (p *Publisher) Run(ctx context.Context) error {
	if p.store == nil || p.broker == nil || p.ID == "" || p.Interval <= 0 || p.Lease <= 10*time.Second {
		return ErrInvalid
	}
	failures := 0
	for ctx.Err() == nil {
		if ensure, ok := p.broker.(interface{ Ensure(context.Context) error }); ok {
			if err := ensure.Ensure(ctx); err != nil {
				if ctx.Err() != nil {
					break
				}
				p.failures.Add(1)
				p.report(err)
				failures++
				if !waitPublisher(ctx, publisherBackoff(failures)) {
					break
				}
				continue
			}
		}
		published, err := p.PublishNext(ctx)
		if ctx.Err() != nil {
			break
		}
		if err != nil {
			p.report(err)
			failures++
			if !waitPublisher(ctx, publisherBackoff(failures)) {
				break
			}
			continue
		}
		failures = 0
		if !published && !waitPublisher(ctx, p.Interval) {
			break
		}
	}
	return nil
}

// One slot claims one event. It never holds database locks across network I/O
// or claims a backlog whose later events could expire before being sent.
func (p *Publisher) PublishNext(ctx context.Context) (published bool, err error) {
	defer func() {
		if err != nil {
			p.failures.Add(1)
		} else if published {
			p.published.Add(1)
		}
	}()
	events, err := p.store.ClaimOutbox(ctx, p.ID, 1, p.Lease)
	if err != nil || len(events) == 0 {
		return false, err
	}
	event := events[0]
	publishCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = p.broker.Publish(publishCtx, event)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if markErr := p.store.MarkPublishFailed(ctx, event, err.Error()); markErr != nil {
			return false, errors.Join(err, markErr)
		}
		return false, fmt.Errorf("publish event %s: %w", event.EventID, err)
	}
	if err = p.store.MarkPublished(ctx, event); err != nil {
		return false, fmt.Errorf("mark event %s published: %w", event.EventID, err)
	}
	return true, nil
}

func (p *Publisher) report(err error) {
	if p.OnError != nil {
		p.OnError(err)
	}
}
func publisherBackoff(failures int) time.Duration {
	base := time.Duration(1<<min(failures-1, 5)) * time.Second
	return base + time.Duration(rand.Int64N(int64(base/4)+1))
}
func waitPublisher(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
