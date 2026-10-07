package watch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// hangingProbe never answers on its own. It stands for the subprocess the
// supervisor's loop was left waiting on at 17:19Z on 2026-09-25: serve kept
// its lock and answered HTTP, but its heartbeat stopped, the Stop hook could
// not take the lock, and supervision stayed off until a restart.
type hangingProbe struct{ released chan struct{} }

func (p hangingProbe) Inspect(ctx context.Context, _ state.TaskMeta) (monitor.EndpointSample, error) {
	select {
	case <-ctx.Done():
		return monitor.EndpointSample{}, ctx.Err()
	case <-p.released:
		return monitor.EndpointSample{}, errors.New("released at test cleanup")
	}
}

func hangingMonitor(t *testing.T, dir string) *monitor.Service {
	t.Helper()
	service := monitoringService(t, dir, "g1")
	released := make(chan struct{})
	t.Cleanup(func() { close(released) })
	service.Probe = hangingProbe{released: released}
	return service
}

func returnsWithin(t *testing.T, limit time.Duration, what string, call func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		call()
	}()
	select {
	case <-done:
	case <-time.After(limit):
		t.Fatalf("%s was still waiting on a probe that never answers after %s", what, limit)
	}
}

func TestReconcileGivesUpOnAProbeThatNeverAnswers(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{Home: baseConfig(dir).Home, Monitor: hangingMonitor(t, dir), ReconcileBudget: 200 * time.Millisecond}

	returnsWithin(t, 10*time.Second, "the supervisor's reconcile pass", func() {
		_ = Reconcile(context.Background(), cfg)
	})
}

func TestRunGivesUpOnAProbeThatNeverAnswers(t *testing.T) {
	dir := t.TempDir()
	cfg := baseConfig(dir)
	cfg.Monitor = hangingMonitor(t, dir)
	cfg.ReconcileBudget = 200 * time.Millisecond

	var reason string
	returnsWithin(t, 10*time.Second, "the watcher's cycle", func() {
		reason, _ = Run(cfg)
	})
	if reason == "" {
		t.Error("the watcher returned without a reason; want the unreadable endpoint the cut-off scan recorded")
	}
}

// A cut-off pass must leave the heartbeat as fresh as the pass's end, not its
// start: the scan stamps last_cycle when it begins, so a pass that spends its
// whole budget would otherwise leave serve's heartbeat a budget old, and the
// next pass can start before serve's own heartbeat tick, doubling that past
// the Stop hook's grace while serve is alive and holds the lock.
//
// The scan waits on a probe that never answers until the budget cuts it off,
// so a heartbeat touched after the scan is stamped at least a budget into the
// pass. That is judged from the pass's start, never as the heartbeat's age
// when the pass returns: that age is how long writing the heartbeat took,
// which reached a second on a loaded four-core runner (run 37631337103) and
// says nothing of when it was stamped. A third of the budget is left for
// Windows' wall clock, which the stamp is kept in, advancing in ticks.
func TestReconcileLeavesTheHeartbeatFreshAfterACutOffPass(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	budget := 300 * time.Millisecond
	cfg := Config{Home: baseConfig(dir).Home, Monitor: hangingMonitor(t, dir), ReconcileBudget: budget}
	passStart := time.Now()

	// Act
	_ = Reconcile(context.Background(), cfg)

	// Assert
	heartbeat, err := monitor.ReadHeartbeat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if stamped := heartbeat.LastCycle.Sub(passStart); stamped < budget-budget/3 {
		t.Errorf("heartbeat was stamped %s into the pass; want it touched after the scan the %s budget cut off", stamped, budget)
	}
}
