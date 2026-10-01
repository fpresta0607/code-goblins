package supervisor

import (
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/watch"
)

// lockHolderVariable names the state directory this test binary, started as
// a stand-in, holds the watcher lock in, and lockHolderModeVariable how:
// "watcher" runs a real watcher, which answers a serve's handover; "legacy"
// holds the lock as a watcher from before the handover did, never reading the
// request; "idle" holds nothing, a bystander such as a terminal's host.
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
	case "legacy":
		if _, err := lock.AcquireNamedOwner(stateDir, ".watch.lock", os.Getpid(), watch.WatcherSession); err != nil {
			return 1
		}
	}
	time.Sleep(2 * time.Minute)
	return 0
}
