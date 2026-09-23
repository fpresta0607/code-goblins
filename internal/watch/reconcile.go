package watch

import (
	"context"
	"time"

	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// Reconcile is one recovery pass for the persistent supervisor. Its caller
// owns .watch.lock. It shares the legacy signal signatures, monitor episodes,
// routing policy, and orphan sweep; it never starts a competing watch loop.
func Reconcile(ctx context.Context, cfg Config) error {
	changes, err := ScanSignals(cfg.Home.State)
	if err != nil {
		return err
	}
	decision := false
	for _, c := range changes {
		if signalIsDecision(cfg.Home.State, c.Name) {
			if _, err := wake.Append(cfg.Home.State, "signal", c.Name, "signal:"+c.Name); err != nil {
				return err
			}
			decision = true
		}
	}
	if err := CommitSignatures(cfg.Home.State, changes); err != nil {
		return err
	}
	if decision {
		if _, err := wake.PublishEpisode(cfg.Home.State); err != nil {
			return err
		}
	}
	if cfg.Monitor != nil {
		result, err := cfg.Monitor.Scan(ctx)
		if err != nil {
			return err
		}
		if result.Event != nil {
			e := *result.Event
			e.Detail = routeHarnessError(cfg, e)
			if _, err := cfg.Monitor.Publish(e); err != nil {
				return err
			}
		}
	} else if err := monitor.TouchHeartbeat(cfg.Home.State, time.Now()); err != nil {
		return err
	}
	sweepOrphans(cfg)
	return nil
}
