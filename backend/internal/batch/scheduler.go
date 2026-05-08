package batch

import (
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// Scheduler is a thin wrapper around robfig/cron/v3 pinned to a single
// timezone (KST in production). The wrapper is small on purpose: cron/v3
// already supports pre-configured timezones and graceful stop, but the
// composition root in cmd/server should not import the third-party type
// directly so the cron implementation can be swapped (or removed when we
// move to an external scheduler — ADR-016) without touching every caller.
//
// Lifecycle:
//
//	sched := batch.NewScheduler(util.KST)
//	sched.Register("1 0 * * *", func() { batch.RunExpiry(...) })
//	sched.Start()
//	...
//	sched.Stop() // graceful — waits for running jobs
//
// Stop() returns once any job that was already running at the time of the
// call has returned. Subsequent Start/Register calls on the same Scheduler
// after Stop are no-ops; allocate a fresh Scheduler for restarts.
type Scheduler struct {
	cron *cron.Cron

	mu      sync.Mutex
	started bool
	stopped bool
}

// NewScheduler returns a Scheduler whose entries are evaluated against
// loc. Pass util.KST in production. loc must not be nil — pass time.UTC
// explicitly if that is intended.
func NewScheduler(loc *time.Location) *Scheduler {
	if loc == nil {
		loc = time.UTC
	}
	return &Scheduler{
		cron: cron.New(cron.WithLocation(loc)),
	}
}

// Register adds a job. spec must be a 5-field cron expression
// ("min hour dom mon dow"); robfig/cron/v3 supports the standard form
// without seconds when constructed via cron.New (no WithSeconds option).
// Returns an error if Start has already been called or if the spec is
// invalid — fail fast at composition time so a typo doesn't silently
// drop the daily expiry job.
func (s *Scheduler) Register(spec string, fn func()) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return fmt.Errorf("batch.Scheduler.Register: cannot add jobs after Start")
	}
	if s.stopped {
		return fmt.Errorf("batch.Scheduler.Register: scheduler already stopped")
	}
	if _, err := s.cron.AddFunc(spec, fn); err != nil {
		return fmt.Errorf("batch.Scheduler.Register: invalid spec %q: %w", spec, err)
	}
	return nil
}

// Start begins evaluating registered entries. Idempotent — calling twice
// is a no-op (a logged warning would mask a wiring bug; we silently
// succeed because cmd/server's run() is single-shot anyway).
func (s *Scheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.stopped {
		return
	}
	s.started = true
	s.cron.Start()
}

// Stop stops new triggers and waits for currently-running jobs to finish.
// It is safe to call Stop without a prior Start — used by cmd/server's
// graceful shutdown path which always tears down everything regardless of
// whether startup completed.
//
// The wait is bounded only by the running job's own behavior; callers
// that need a hard timeout should wrap the call with context.AfterFunc
// or run Stop in a goroutine and select on a timer.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if !s.started || s.stopped {
		s.stopped = true
		s.mu.Unlock()
		return
	}
	s.stopped = true
	s.mu.Unlock()

	ctx := s.cron.Stop()
	<-ctx.Done()
}

// EntryCount returns the number of registered jobs. Useful for tests and
// for the cmd/server log line that confirms cron registration succeeded.
func (s *Scheduler) EntryCount() int {
	return len(s.cron.Entries())
}

// ParseSpec is a thin re-export of cron.ParseStandard so callers that
// want to validate a spec ahead of time (e.g. config loaders) can do so
// without depending on robfig/cron/v3 directly.
func ParseSpec(spec string) error {
	_, err := cron.ParseStandard(spec)
	return err
}
