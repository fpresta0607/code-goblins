package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestTheComebackRechecksWaitingGoblinsBeforeLaunching(t *testing.T) {
	for _, testCase := range []struct {
		name, evaluation, lifecycle, action string
		reports                             []string
		isStale, isRetired, isRunning       bool
		isResumeOperation, shouldLaunch     bool
		isPromptedAfter                     bool
	}{
		{name: "done", evaluation: "done"},
		{name: "merged", evaluation: "merged"},
		{name: "reported its pull request done", reports: []string{"done: PR https://github.com/acme/api/pull/7"}},
		{name: "took up work after reporting done", reports: []string{"done: PR https://github.com/acme/api/pull/7", "working: the review's fix"}, shouldLaunch: true},
		{name: "steered back to work after reporting done", reports: []string{"done: PR https://github.com/acme/api/pull/7"}, isPromptedAfter: true, shouldLaunch: true},
		{name: "paused", lifecycle: "paused"},
		{name: "stopped", lifecycle: "stopped"},
		{name: "pausing", lifecycle: "pausing"},
		{name: "resume cut short before it published", lifecycle: "resuming", shouldLaunch: true},
		{name: "stopping", lifecycle: "stopping"},
		{name: "failed", lifecycle: "failed"},
		{name: "resume cut short", lifecycle: "resuming", isResumeOperation: true, shouldLaunch: true},
		{name: "resume failed", lifecycle: "failed", isResumeOperation: true},
		{name: "pause failed", lifecycle: "failed", action: "pause"},
		{name: "stop failed", lifecycle: "failed", action: "stop"},
		{name: "retired", isRetired: true},
		{name: "terminal already answers", isRunning: true},
		{name: "old completion", evaluation: "done", isStale: true, shouldLaunch: true},
		{name: "old pause", lifecycle: "paused", isStale: true, shouldLaunch: true},
		{name: "still working", lifecycle: "running", shouldLaunch: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := &comebackRecorder{}
			service, h, _ := comebackBoard(t, recorder, [2]float64{8, 8})
			meta := workingGoblin(t, h, "alpha", lastSignIn.Add(time.Hour))
			reading(t, service, 0)
			generation := meta.SpawnGen
			if testCase.isStale {
				generation = "previous-generation"
			}
			if testCase.evaluation != "" {
				service.Store.mu.Lock()
				service.Store.db.Tasks[meta.ID] = Evaluation{Phase: testCase.evaluation, Generation: generation, At: thisSignIn}
				err := service.Store.save()
				service.Store.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, report := range testCase.reports {
				if err := state.AppendStatus(h.State, meta.ID, report); err != nil {
					t.Fatal(err)
				}
			}
			if testCase.isPromptedAfter {
				promptedAfterItsReports(t, service.Store, meta)
			}
			if testCase.lifecycle != "" {
				action := "resume"
				if testCase.lifecycle == "paused" || testCase.lifecycle == "pausing" {
					action = "pause"
				} else if testCase.lifecycle == "stopped" || testCase.lifecycle == "stopping" {
					action = "stop"
				}
				if testCase.action != "" {
					action = testCase.action
				}
				if testCase.isResumeOperation {
					meta.ResumeOperation = "changed"
					if err := state.WriteTaskMeta(h.State, meta); err != nil {
						t.Fatal(err)
					}
				}
				if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: generation, Operation: "changed", Action: action, Phase: testCase.lifecycle, Started: thisSignIn}); err != nil {
					t.Fatal(err)
				}
			}
			if testCase.isRetired {
				if err := os.Remove(state.TaskMetaPath(h.State, meta.ID)); err != nil {
					t.Fatal(err)
				}
			}
			if testCase.isRunning {
				if err := os.Remove(filepath.Join(h.State, "hosts", meta.ID+".json")); err != nil {
					t.Fatal(err)
				}
				program, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				typed := filepath.Join(h.Root, "typed.txt")
				hostProgram(t, h.State, meta.ID, typed, program, "-test.run=^TestNativeTerminalProgram$", "--", "native-terminal-program", typed, h.State)
			}
			if !testCase.shouldLaunch {
				service.Options.Dispatch.Disk = func() (Disk, error) { return Disk{}, errors.New("disk cannot be read") }
			}

			reading(t, service, 1)

			came := recorder.came()
			if testCase.shouldLaunch {
				if !slices.Equal(came, []string{meta.ID}) {
					t.Fatalf("launched %v, want alpha resumed", came)
				}
			} else if len(came) != 0 {
				t.Fatalf("launched %v after %s", came, testCase.name)
			}
			record, err := state.ReadComeback(h.State)
			if err != nil {
				t.Fatal(err)
			}
			if testCase.shouldLaunch || testCase.isRunning {
				if len(record.Goblins) != 1 || record.Goblins[0].State != state.ComebackBack {
					t.Fatalf("comeback %+v, want alpha back", record.Goblins)
				}
			} else if len(record.Goblins) != 0 {
				t.Fatalf("comeback %+v, want alpha left as it was", record.Goblins)
			}
		})
	}
}

func TestTheComebackReconcilesWaitingGenerationsInTheSameSignIn(t *testing.T) {
	for _, testCase := range []struct {
		name, phase             string
		isRunning, shouldChange bool
	}{
		{name: "waiting without a terminal", phase: state.ComebackWaiting, shouldChange: true},
		{name: "waiting with a live terminal", phase: state.ComebackWaiting, isRunning: true},
		{name: "stopped", phase: state.ComebackStopped},
		{name: "back", phase: state.ComebackBack},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := &comebackRecorder{}
			service, h, _ := comebackBoard(t, recorder, [2]float64{8, 8})
			meta := workingGoblin(t, h, "alpha", lastSignIn.Add(time.Hour))
			generation := meta.SpawnGen
			planned := thisSignIn.Add(time.Minute)
			if err := state.WriteComeback(h.State, state.Comeback{SignedIn: thisSignIn, Planned: planned, Goblins: []state.ComebackEntry{
				{ID: meta.ID, Generation: generation, State: testCase.phase, Reason: "kept reason"},
			}}); err != nil {
				t.Fatal(err)
			}
			meta.SpawnGen = "published-generation"
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			if testCase.isRunning {
				if err := os.Remove(filepath.Join(h.State, "hosts", meta.ID+".json")); err != nil {
					t.Fatal(err)
				}
				hostTerminal(t, h.State, meta.ID)
			}
			service.starting = "manual"
			wakes := fleetWakes{MemoryAbove: 1, MemoryReadAt: thisSignIn.Add(time.Minute)}

			err := service.checkMemory(t.Context(), &wakes, thisSignIn.Add(2*time.Minute))
			if err != nil || wakes.MemoryAbove != 2 || len(recorder.came()) != 0 {
				t.Fatalf("error %v, readings %d, resumed %v", err, wakes.MemoryAbove, recorder.came())
			}
			record, err := state.ReadComeback(h.State)
			if err != nil || len(record.Goblins) != 1 {
				t.Fatalf("comeback %+v, error %v", record, err)
			}
			if testCase.shouldChange {
				generation = meta.SpawnGen
			}
			entry := record.Goblins[0]
			if entry.Generation != generation || entry.State != testCase.phase || entry.Reason != "kept reason" || !record.Planned.Equal(planned) || !record.SignedIn.Equal(thisSignIn) {
				t.Fatalf("comeback %+v, want %s on %s with the original plan", record, testCase.phase, generation)
			}
			if record.Waiting(meta.ID, meta.SpawnGen) != testCase.shouldChange {
				t.Fatalf("waiting on the published generation = %v, want %v", record.Waiting(meta.ID, meta.SpawnGen), testCase.shouldChange)
			}
			snapshot, err := service.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			card := slices.IndexFunc(snapshot.Tasks, func(task Task) bool { return task.ID == meta.ID })
			if card < 0 || (snapshot.Tasks[card].Comeback != nil) != testCase.shouldChange {
				t.Fatalf("task's comeback on the published generation: %+v", snapshot.Tasks)
			}
			if testCase.shouldChange && snapshot.Tasks[card].Comeback.State != state.ComebackWaiting {
				t.Fatalf("task's comeback %+v, want waiting", snapshot.Tasks[card].Comeback)
			}
			if err := os.Chtimes(state.ComebackPath(h.State), lastSignIn, lastSignIn); err != nil {
				t.Fatal(err)
			}
			if err := service.checkMemory(t.Context(), &wakes, thisSignIn.Add(3*time.Minute)); err != nil {
				t.Fatal(err)
			}
			if info, err := os.Stat(state.ComebackPath(h.State)); err != nil || !info.ModTime().Equal(lastSignIn) {
				t.Fatalf("unchanged comeback was rewritten, stat error %v", err)
			}
		})
	}
}
