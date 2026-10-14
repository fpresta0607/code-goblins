package cirunner

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrBusy is a run asked for while a job runs. The service never queues a
// run, so the machine's heavy line stays the only queue.
var ErrBusy = errors.New("cirunner: a job already runs")

// Service runs one job at a time, as the fleet asks for each.
type Service struct {
	Deps   Deps
	Limits Limits

	mu      sync.Mutex
	started time.Time
	last    string
}

// Start puts the VM back to clean and deletes every runner the service
// registered, as a job's end would have, before the service takes any run:
// a service that stopped mid-job, as in a crash, left both behind.
func (s *Service) Start(ctx context.Context) error {
	if err := putBack(ctx, s.Deps.VM); err != nil {
		return err
	}
	return s.Deps.GitHub.DeleteOwnRunners(ctx)
}

// Pending is the desktop job that has waited longest, when one waits.
func (s *Service) Pending(ctx context.Context) (Pending, error) {
	return s.Deps.GitHub.QueuedJob(ctx)
}

// Run runs one job, or refuses with ErrBusy while one runs.
func (s *Service) Run(ctx context.Context, progress func(string)) (Outcome, error) {
	s.mu.Lock()
	if !s.started.IsZero() {
		s.mu.Unlock()
		return Failed, ErrBusy
	}
	started := s.Deps.Now()
	s.started = started
	s.mu.Unlock()
	outcome, err := RunOneJob(ctx, s.Deps, s.Limits, progress)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started = time.Time{}
	s.last = fmt.Sprintf("the last job, started at %s, %s", started.UTC().Format(time.RFC3339), describe(outcome, err))
	return outcome, err
}

// Status says whether a job runs, and how the last one ended.
func (s *Service) Status() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case !s.started.IsZero():
		return "a job runs, started at " + s.started.UTC().Format(time.RFC3339)
	case s.last != "":
		return "no job runs, and " + s.last
	default:
		return "no job has run since the service started"
	}
}

func describe(outcome Outcome, err error) string {
	said := map[Outcome]string{
		Ran:       "ran to its end",
		NoJob:     "found no job to run",
		TimedOut:  "ran past its limit",
		Abandoned: "ended with its turn",
		Failed:    "failed",
	}[outcome]
	if err != nil {
		said += ", and " + err.Error()
	}
	return said
}
