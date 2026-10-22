package cleanup

import (
	"context"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// readHook runs a function once a monitor sweep has read a task's record and
// before it reads the task's terminal, the first time it reads one, and keeps
// what each read after it found.
type readHook struct {
	monitor.Prober
	before func()
	after  []monitor.ProbeVerdict
}

func (h *readHook) Inspect(ctx context.Context, meta state.TaskMeta) (monitor.EndpointSample, error) {
	hook := h.before
	if hook != nil {
		h.before = nil
		hook()
	}
	sample, err := h.Prober.Inspect(ctx, meta)
	if hook != nil {
		h.after = append(h.after, sample.Verdict)
	}
	return sample, err
}

// sweep runs one cycle of the watcher's monitor: a scan, then the publish of
// the wake it raised, if any.
func sweep(t *testing.T, service monitor.Service) {
	t.Helper()
	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.Event != nil {
		if _, err := service.Publish(*result.Event); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
}

// A cleanup closes the goblin's terminal, returns its worktree and only then
// retires its record. On 2026-10-20 a monitor sweep that had read Fern's
// record before her cleanup retired it read her terminal after the cleanup
// closed it, and woke the CFO with endpoint_missing to bring back a goblin
// that was retired (issue #1225). A sweep in flight across a cleanup, whether
// the cleanup still holds the task or has retired it when the sweep reads the
// terminal, raises no wake for the task, and neither does the sweep after it.
func TestMonitorSweepAcrossACleanupRaisesNoWakeForTheRetiredTask(t *testing.T) {
	for _, test := range []struct {
		name string
		// start runs the cleanup up to where the sweep reads the terminal,
		// and returns what lets it end and says how it ended.
		start func(t *testing.T, fixture *cleanupFixture) (finish func() error)
	}{
		{name: "the cleanup holds the task with its terminal closed", start: func(t *testing.T, fixture *cleanupFixture) func() error {
			closed, proceed, ended := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			fixture.service.EndLeft = func(context.Context, state.TaskMeta) ([]string, error) {
				close(closed)
				<-proceed
				return nil, nil
			}
			go func() {
				_, err := fixture.service.Cleanup(context.Background(), "g1")
				ended <- err
			}()
			finish := func() error {
				close(proceed)
				return <-ended
			}
			select {
			case <-closed:
			case err := <-ended:
				t.Fatalf("the cleanup ended before it closed the terminal: %v", err)
			case <-time.After(time.Minute):
				t.Cleanup(func() { _ = finish() })
				t.Fatal("the cleanup did not close the terminal within a minute")
			}
			return finish
		}},
		{name: "the cleanup has retired the task", start: func(t *testing.T, fixture *cleanupFixture) func() error {
			_, err := fixture.service.Cleanup(context.Background(), "g1")
			return func() error { return err }
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			fixture := nativeCleanupFixture(t, 0)
			runFakeClaude(t, fixture, "idle")
			if err := state.AppendStatus(fixture.stateDir, "g1", "done: delivered"); err != nil {
				t.Fatal(err)
			}
			hook := &readHook{Prober: monitor.NativeProber{StateDir: fixture.stateDir}}
			service := monitor.Service{StateDir: fixture.stateDir, Probe: hook, LaunchGrace: time.Nanosecond}
			sweep(t, service)
			if seen, err := monitor.ReadObservation(fixture.stateDir, "g1"); err != nil || seen.EndpointVerdict != monitor.ProbePresent {
				t.Fatalf("the sweep before the cleanup saw %+v, %v; want the goblin's terminal present", seen, err)
			}
			if records, err := wake.Pending(fixture.stateDir); err != nil || len(records) != 0 {
				t.Fatalf("the sweep before the cleanup raised %+v, %v; want no wake", records, err)
			}
			var finish func() error
			hook.before = func() { finish = test.start(t, fixture) }

			// Act
			sweep(t, service)
			cleanupErr := finish()
			sweep(t, service)

			// Assert
			if cleanupErr != nil {
				t.Fatalf("Cleanup: %v", cleanupErr)
			}
			if _, err := state.ReadTaskMeta(fixture.stateDir, "g1"); err == nil {
				t.Fatal("the cleanup did not retire the task")
			}
			if len(hook.after) != 1 || hook.after[0] != monitor.ProbeMissing {
				t.Fatalf("the sweep in flight read the terminal as %v, want it once, gone", hook.after)
			}
			records, err := wake.Pending(fixture.stateDir)
			if err != nil {
				t.Fatal(err)
			}
			for _, record := range records {
				if record.Key == "g1" {
					t.Errorf("wake %d (%s): %s; want no wake for the task its cleanup retired", record.Seq, record.Kind, record.Detail)
				}
			}
		})
	}
}
