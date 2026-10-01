package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/watch"
)

// lockHolderVariable names the state directory this test binary, started as
// a stand-in, holds the watcher lock in, and lockHolderModeVariable how:
// "watcher" runs a real watcher, which answers a serve's handover; "legacy"
// holds the lock as a watcher from before the handover did, never reading the
// request; "acknowledging" answers the request as a watcher winding a slow
// cycle down does and lets the lock go acknowledgedHold later; "slow-watcher"
// runs a real watcher whose monitor scan cannot be cut short, each inspection
// taking unheededInspection whatever its context says; "serve" takes the lock
// as cfo serve does, over a watcher holding it, with a two-second handover
// wait; "idle" holds nothing, a bystander such as a terminal's host.
const (
	lockHolderVariable     = "CFO_TEST_LOCK_HOLDER_STATE"
	lockHolderModeVariable = "CFO_TEST_LOCK_HOLDER_MODE"
)

// holdWatcherLock is the stand-in's whole run, and its exit code.
func holdWatcherLock(stateDir, mode string) int {
	switch mode {
	case "watcher":
		cfg := watch.ConfigFromEnv(home.Home{Root: filepath.Dir(stateDir), State: stateDir})
		cfg.Monitor, cfg.Reap, cfg.FileEvery = nil, nil, 0
		if _, err := watch.Run(cfg); err != nil {
			return 1
		}
		return 0
	case "slow-watcher":
		cfg := watch.ConfigFromEnv(home.Home{Root: filepath.Dir(stateDir), State: stateDir})
		cfg.Reap, cfg.FileEvery = nil, 0
		cfg.Monitor = &monitor.Service{StateDir: stateDir, Probe: unheedingProbe{}, Now: time.Now, StaleEscalateAfter: time.Minute, BusyTurnMax: time.Hour, PauseResurfaceAfter: time.Hour, Heartbeat: time.Minute, HeartbeatMax: time.Hour}
		if _, err := watch.Run(cfg); err != nil {
			return 1
		}
		return 0
	case "legacy":
		if _, err := lock.AcquireNamedOwner(stateDir, ".watch.lock", os.Getpid(), watch.WatcherSession); err != nil {
			return 1
		}
	case "serve":
		watch.HandoverWait = 2 * time.Second
		if err := AcquireWatchLock(stateDir); err != nil {
			return 1
		}
	case "acknowledging":
		if _, err := lock.AcquireNamedOwner(stateDir, ".watch.lock", os.Getpid(), watch.WatcherSession); err != nil {
			return 1
		}
		if err := acknowledgeFirstRequest(stateDir); err != nil {
			return 1
		}
		time.Sleep(acknowledgedHold)
		if err := lock.ReleaseNamed(stateDir, ".watch.lock"); err != nil {
			return 1
		}
		return 0
	}
	time.Sleep(2 * time.Minute)
	return 0
}

// acknowledgedHold is how long an acknowledging stand-in keeps the lock after
// answering, far past the handover wait its test sets.
const acknowledgedHold = 8 * time.Second

// acknowledgeFirstRequest waits for a serve's request and answers it as a
// watcher does, naming itself and the serve.
func acknowledgeFirstRequest(stateDir string) error {
	var request struct {
		PID   int       `json:"pid"`
		Start time.Time `json:"start"`
	}
	for {
		data, err := os.ReadFile(filepath.Join(stateDir, watch.HandoverName))
		if err == nil && json.Unmarshal(data, &request) == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	start, _ := proc.StartTime(os.Getpid())
	hostname, err := os.Hostname()
	if err != nil {
		return err
	}
	data, err := json.Marshal(map[string]any{"watcher_pid": os.Getpid(), "watcher_start": start, "watcher_hostname": hostname, "serve_pid": request.PID, "serve_start": request.Start})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stateDir, watch.HandoverAckName), data, 0o600)
}

// unheededInspection is how long each of a slow watcher's inspections takes.
const unheededInspection = 10 * time.Second

// unheedingProbe stands for an inspection that cannot be interrupted, such as
// a screen read already under way: it ends when it ends, then reports the
// context's state.
type unheedingProbe struct{}

func (unheedingProbe) Inspect(ctx context.Context, _ state.TaskMeta) (monitor.EndpointSample, error) {
	time.Sleep(unheededInspection)
	return monitor.EndpointSample{}, ctx.Err()
}
