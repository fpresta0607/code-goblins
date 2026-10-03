package watch

import (
	"os"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
)

func TestAWatcherThatAnsweredSkipsThePollFloorAfterAWaitFailure(t *testing.T) {
	dir := t.TempDir()
	first := startServeStandIn(t)
	second := startServeStandIn(t)
	cfg := baseConfig(dir)
	cfg.Poll = 20 * time.Second
	cfg.Sleep = time.Sleep
	var answered time.Time
	cfg.WaitEvent = func(time.Duration) bool {
		askForTheLock(t, dir, first)
		eventually(t, 5*time.Second, "the first handover answer", func() bool {
			ack, ok := readHandoverAck(dir)
			return ok && ack.ServePID == first
		})
		askForTheLock(t, dir, second)
		// Answering again proves the first request already canceled the cycle.
		eventually(t, 5*time.Second, "the second handover answer", func() bool {
			ack, ok := readHandoverAck(dir)
			return ok && ack.ServePID == second
		})
		answered = time.Now()
		return false
	}

	reason, err := Run(cfg)

	if err != nil || reason != "" {
		t.Fatalf("Run = %q, %v; want the watcher to yield quietly", reason, err)
	}
	if answered.IsZero() {
		t.Fatal("the watcher never answered while its directory wait was running")
	}
	if waited := time.Since(answered); waited > 5*time.Second {
		t.Errorf("the watcher stayed alive %s after answering; want cancellation to interrupt its %s poll", waited, cfg.Poll)
	}
	if lock.HeldByNamed(dir, ".watch.lock", os.Getpid()) {
		t.Error("the watcher still holds the lock after yielding")
	}
}
