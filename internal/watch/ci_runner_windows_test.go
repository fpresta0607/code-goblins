package watch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/cirunner"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
)

// The watcher watches the desktop CI runner while CIRunnerEvery is set, from
// before its first cycle, and never when it is zero.
func TestRunStartsTheDesktopCIRunnerWatch(t *testing.T) {
	for name, every := range map[string]time.Duration{"set": time.Hour, "zero": 0} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			cfg := reapConfig(dir, orphanFleet())
			cfg.CIRunnerEvery = every
			cfg.CIRunnerPipe = `\\.\pipe\code-goblins-ci-runner-test-` + filepath.Base(dir)

			// Act
			_, err := Run(cfg)

			// Assert
			_, statErr := os.Stat(ciRunnerFolder(dir))
			if err != nil || (every > 0) != (statErr == nil) {
				t.Errorf("Run = %v, the runner's folder %v; want the watch started only when CIRunnerEvery is set", err, statErr)
			}
		})
	}
}

// A test binary never watches the machine's own runner, so no test starts a
// job's turn on the machine, and a test's own pipe is watched.
func TestATestWatchesOnlyARunnerOfItsOwn(t *testing.T) {
	// Act
	machine, machineErr := newCIRunnerWatch(t.TempDir(), cirunner.PipeName)
	own, ownErr := newCIRunnerWatch(t.TempDir(), `\\.\pipe\code-goblins-ci-runner-test-own`)

	// Assert
	if machineErr == nil || machine != nil {
		t.Errorf("watching the machine's runner from a test = %v, %v; want it refused", machine, machineErr)
	}
	if ownErr != nil || own == nil {
		t.Errorf("watching a test's own runner = %v, %v; want it watched", own, ownErr)
	}
}

// The defaults ask the machine's runner every 30 seconds.
func TestConfigFromEnvWatchesTheMachinesRunner(t *testing.T) {
	// Act
	cfg := ConfigFromEnv(home.Home{State: t.TempDir()})
	if cfg.Cleanup != nil {
		defer cfg.Cleanup()
	}

	// Assert
	if cfg.CIRunnerEvery != 30*time.Second || cfg.CIRunnerPipe != cirunner.PipeName {
		t.Errorf("CIRunnerEvery %v, CIRunnerPipe %q; want 30s and %q", cfg.CIRunnerEvery, cfg.CIRunnerPipe, cirunner.PipeName)
	}
}

// pendingOnly is GitHub as a service that is only asked what waits sees it,
// and says when it is first asked.
type pendingOnly struct {
	asked chan struct{}
	once  sync.Once
}

func (p *pendingOnly) QueuedJob(context.Context) (cirunner.Pending, error) {
	p.once.Do(func() { close(p.asked) })
	return cirunner.Pending{}, nil
}

func (p *pendingOnly) RegisterRunner(context.Context, string) (cirunner.Runner, error) {
	return cirunner.Runner{}, errors.New("not asked for")
}

func (p *pendingOnly) RunnerState(context.Context, int64) (bool, bool, error) {
	return false, false, errors.New("not asked for")
}

func (p *pendingOnly) DeleteRunner(context.Context, int64) error { return errors.New("not asked for") }

func (p *pendingOnly) DeleteOwnRunners(context.Context) error { return errors.New("not asked for") }

// While it runs, the watcher asks the runner whether a job waits.
func TestTheWatcherAsksTheRunnerWhileItRuns(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	t.Setenv("CFO_POLL", "300")
	cfg := ConfigFromEnv(home.Home{Root: filepath.Dir(dir), State: dir})
	cfg.Monitor, cfg.Reap, cfg.FileEvery = nil, nil, 0
	cfg.CIRunnerPipe = `\\.\pipe\code-goblins-ci-runner-test-` + filepath.Base(dir)
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	github := &pendingOnly{asked: make(chan struct{})}
	serving, stopServing := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() {
		served <- cirunner.Serve(serving, &cirunner.Service{Deps: cirunner.Deps{GitHub: github}, Log: func(string) {}}, cfg.CIRunnerPipe, user.User.Sid.String())
	}()
	defer func() {
		stopServing()
		<-served
	}()
	eventually(t, 5*time.Second, "the runner answering", func() bool {
		return cirunner.Ask(context.Background(), cfg.CIRunnerPipe, cirunner.Request{Kind: cirunner.AskStatus}, func(cirunner.Reply) {}) == nil
	})
	returned := make(chan error, 1)

	// Act
	go func() {
		_, err := Run(cfg)
		returned <- err
	}()

	// Assert
	select {
	case <-github.asked:
	case <-time.After(20 * time.Second):
		t.Error("the watcher never asked the runner whether a job waits")
	}
	eventually(t, 10*time.Second, "the watcher taking the lock", func() bool { return lock.HeldByNamed(dir, ".watch.lock", os.Getpid()) })
	askForTheLock(t, dir, startServeStandIn(t))
	select {
	case err := <-returned:
		if err != nil {
			t.Errorf("Run = %v; want it to yield quietly", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the watcher kept running")
	}
}
