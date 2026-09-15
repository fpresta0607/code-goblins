// Package supervisor owns the continuous watcher process, independently of
// Claude, Codex, Pi and Kimi turn boundaries. It never restarts a worker.
package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/wake"
	"github.com/fpresta0607/code-goblins/internal/watch"
)

const LockName = ".supervisor.lock"
const stopFile = ".supervisor-stop"
const FreshFor = 2 * time.Minute

type Health struct {
	Healthy         bool      `json:"healthy"`
	PID             int       `json:"pid,omitempty"`
	Reason          string    `json:"reason"`
	LastObservation time.Time `json:"last_observation"`
	Action          string    `json:"action"`
	Observing       bool      `json:"observing"`
	Delivery        Delivery  `json:"delivery"`
}

func Status(dir string) Health {
	health := Health{Reason: "supervisor_down", Action: "cfo supervisor start; then cfo drain"}
	beat, err := monitor.ReadHeartbeat(dir)
	if err == nil {
		health.LastObservation = beat.LastCycle
	}
	owner, err := lock.ReadNamed(dir, LockName)
	if err != nil || !owner.Alive() {
		return health
	}
	health.PID = owner.PID
	age := time.Since(health.LastObservation)
	if age < 0 || age >= FreshFor || health.LastObservation.Before(owner.Acquired) {
		health.Reason = "stale_evidence"
		health.Action = "cfo supervisor status; inspect supervisor process and backend; do not restart workers"
		return health
	}
	health.Observing = true
	if data, e := os.ReadFile(filepath.Join(dir, "delivery-error")); e == nil {
		health.Reason = "notification_failed"
		health.Action = string(data)
		return health
	}
	data, e := os.ReadFile(filepath.Join(dir, "delivery.json"))
	if e != nil || json.Unmarshal(data, &health.Delivery) != nil || time.Since(health.Delivery.CheckedAt) >= FreshFor || health.Delivery.CheckedAt.After(time.Now()) {
		health.Reason = "notification_failed"
		health.Action = "cfo supervisor register <session:pane>; inspect delivery health"
		return health
	}
	if health.Delivery.Unconfirmed || len(health.Delivery.Uncertain) > 0 {
		health.Reason = "submission_unknown"
		health.Action = "inspect uncertain receipts with cfo supervisor status and cfo peek; confirm verified submission without acknowledging unresolved decisions"
		return health
	}
	if health.Delivery.State != "ready" {
		health.Reason = health.Delivery.State
		health.Action = health.Delivery.Detail
		return health
	}
	health.Healthy = true
	health.Reason = "running"
	health.Action = "cfo drain"
	return health
}

// Stop requests a cooperative stop of this exact process generation. A stale
// request cannot stop its successor, even if Windows recycles the PID.
func Stop(dir string) error {
	owner, err := lock.ReadNamed(dir, LockName)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !owner.Alive() {
		return nil
	}
	return fsx.AtomicWriteFile(filepath.Join(dir, stopFile), []byte(owner.Acquired.Format(time.RFC3339Nano)))
}

type Service struct {
	Home    home.Home
	Config  func() watch.Config
	OnEvent func(string)
	Deliver func(context.Context) error
}

func (s Service) Run(ctx context.Context) (err error) {
	if err := os.MkdirAll(s.Home.State, 0700); err != nil {
		return err
	}
	owner, err := lock.AcquireExclusiveNamed(s.Home.State, LockName)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(s.Home.State, LockName)) }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				data, e := os.ReadFile(filepath.Join(s.Home.State, stopFile))
				if e == nil && strings.TrimSpace(string(data)) == owner.Acquired.Format(time.RFC3339Nano) {
					cancel()
					return
				}
			}
		}
	}()
	// Retry observation failures three times per process with exponential delay.
	// The external Windows task supplies a separate bounded process-restart cap.
	for attempt := 0; attempt < 3; attempt++ {
		cfg := watch.ConfigFromEnv(s.Home)
		if s.Config != nil {
			if cfg.Cleanup != nil {
				cfg.Cleanup()
			}
			cfg = s.Config()
		}
		cfg.Continuous = true
		cfg.OnEvent = s.OnEvent
		cfg.OnCycle = func(ctx context.Context) {
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if s.Deliver != nil {
				if e := s.Deliver(ctx); e != nil {
					_ = fsx.AtomicWriteFile(filepath.Join(s.Home.State, "delivery-error"), []byte(e.Error()))
				} else {
					_ = os.Remove(filepath.Join(s.Home.State, "delivery-error"))
				}
			}
		}
		cfg.Sleep = func(d time.Duration) {
			select {
			case <-ctx.Done():
			case <-time.After(d):
			}
		}
		if cfg.WaitEvent != nil { // Timer mode permits prompt cooperative shutdown.
			if cfg.Cleanup != nil {
				cfg.Cleanup()
			}
			cfg.WaitEvent = nil
			cfg.Cleanup = nil
		}
		_, err = watch.RunContext(ctx, cfg)
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			err = errors.New("watcher lost ownership")
		}
		if errors.Is(err, lock.ErrHeld) {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second * time.Duration(1<<attempt)):
		}
	}
	detail := fmt.Sprintf("supervisor observation failed after 3 attempts: %v; inspect cfo supervisor status; retained work is unchanged", err)
	if _, e := wake.Append(s.Home.State, "check", "supervisor", detail); e != nil {
		return errors.Join(err, e)
	}
	_, e := wake.PublishEpisode(s.Home.State)
	return errors.Join(err, e)
}
