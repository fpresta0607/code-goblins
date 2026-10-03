package watch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func TestAWatcherThatAnsweredSkipsThePollFloorAfterAWaitFailure(t *testing.T) {
	dir := t.TempDir()
	first := startServeStandIn(t)
	second := startServeStandIn(t)
	cfg := baseConfig(dir)
	cfg.Poll = 20 * time.Second
	cfg.Sleep = sleep
	var answered time.Time
	cfg.WaitEvent = func(context.Context, time.Duration) bool {
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

func TestAWatcherYieldsDuringItsTimerAndGraceSleeps(t *testing.T) {
	for _, hasSignal := range []bool{false, true} {
		name := "poll"
		if hasSignal {
			name = "signal grace"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			serve := startServeStandIn(t)
			if hasSignal {
				if err := os.WriteFile(filepath.Join(dir, "task.status"), []byte("needs-decision: waiting"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cfg := baseConfig(dir)
			cfg.Poll, cfg.SignalGrace = 10*time.Second, 10*time.Second
			sleeping := make(chan struct{})
			cfg.Sleep = func(ctx context.Context, duration time.Duration) {
				close(sleeping)
				sleep(ctx, duration)
			}
			type result struct {
				reason string
				err    error
			}
			returned := make(chan result, 1)
			go func() {
				reason, err := Run(cfg)
				returned <- result{reason, err}
			}()
			select {
			case <-sleeping:
			case got := <-returned:
				t.Fatalf("Run returned before sleeping: %q, %v", got.reason, got.err)
			case <-time.After(10 * time.Second):
				t.Fatal("the watcher never started its sleep")
			}

			askForTheLock(t, dir, serve)

			var got result
			select {
			case got = <-returned:
			case <-time.After(5 * time.Second):
				t.Error("the handover did not interrupt the watcher's sleep")
				got = <-returned
			}
			if got.err != nil || got.reason != "" {
				t.Fatalf("Run = %q, %v; want it to yield quietly", got.reason, got.err)
			}
			if records, err := wake.Pending(dir); err != nil || len(records) != 0 {
				t.Errorf("a yielded watcher queued wakes: %+v, %v", records, err)
			}
		})
	}
}

func TestAWaitFailureStillKeepsThePollFloor(t *testing.T) {
	dir := t.TempDir()
	serve := startServeStandIn(t)
	cfg := baseConfig(dir)
	cfg.Poll = 250 * time.Millisecond
	cfg.Sleep = sleep
	var failedAt time.Time
	var retriedAfter time.Duration
	cfg.WaitEvent = func(ctx context.Context, _ time.Duration) bool {
		if failedAt.IsZero() {
			failedAt = time.Now()
			return false
		}
		retriedAfter = time.Since(failedAt)
		askForTheLock(t, dir, serve)
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("the watcher never canceled after the handover request")
		}
		return false
	}

	reason, err := Run(cfg)

	if reason != "" || err != nil {
		t.Fatalf("Run = %q, %v; want it to yield quietly", reason, err)
	}
	if retriedAfter < cfg.Poll-time.Millisecond {
		t.Errorf("the failed wait retried after %s, before the %s polling floor", retriedAfter, cfg.Poll)
	}
}
