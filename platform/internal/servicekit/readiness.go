package servicekit

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Readiness bounds and coalesces dependency probes. Liveness never probes them.
type Readiness struct {
	draining atomic.Bool
	mu       sync.Mutex
	checked  time.Time
	err      error
	check    func(context.Context) error
}

func NewReadiness(check func(context.Context) error) *Readiness { return &Readiness{check: check} }
func (r *Readiness) Drain()                                     { r.draining.Store(true) }
func (r *Readiness) Ready(ctx context.Context) bool {
	if r == nil {
		return true
	}
	if r.draining.Load() {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Since(r.checked) > time.Second {
		if r.check != nil {
			checkCtx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
			r.err = r.check(checkCtx)
			cancel()
		}
		r.checked = time.Now()
	}
	return !r.draining.Load() && r.err == nil
}
