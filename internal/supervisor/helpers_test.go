package supervisor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

const helperBrief = "## Task\nWrite the migration for the accounts table.\n## Authentication\ncredentials: none\nmcp: none\n"

// helperBoard is a supervisor whose goblin g1 works on branch feat/x in a
// real repository, on a machine with available memory and ample disk and
// commit, whose goblins spawn and goblins send are spawner.
func helperBoard(t *testing.T, available uint64, spawner *spawnRecorder) (*Service, home.Home) {
	t.Helper()
	handler, h := startBoard(t, available, spawner)
	s := handler.Service
	s.Options.Progress = execx.OSRunner{}
	worktree := filepath.Join(h.Root, "worktrees", "app", "g1")
	for _, args := range [][]string{
		{"init", "-q", "--initial-branch=main", worktree},
		{"-C", worktree, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "seed"},
		{"-C", worktree, "switch", "-q", "--create", "feat/x"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	writeHelperTask(t, h, state.TaskMeta{ID: "g1", Project: filepath.Join(h.Root, "app"), Worktree: worktree, Model: "claude-opus-5-5", Effort: "xhigh", SpawnGen: "s1"})
	return s, h
}

func writeHelperTask(t *testing.T, h home.Home, meta state.TaskMeta) {
	t.Helper()
	meta.Window, meta.Harness, meta.Kind, meta.Backend = "native", "claude", "ship", "native"
	if meta.Worktree == "" {
		meta.Worktree = filepath.Join(h.Root, "worktrees", "app", meta.ID)
	}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
}

// waitForCalls waits until spawner has run n commands.
func waitForCalls(t *testing.T, spawner *spawnRecorder, n int) [][]string {
	t.Helper()
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if calls := spawner.recorded(); len(calls) >= n {
			return calls
		}
	}
	t.Fatalf("goblins ran %q, want %d commands", spawner.recorded(), n)
	return nil
}

// waitStartEnds waits until the start s runs has ended and told everyone.
func waitStartEnds(t *testing.T, s *Service) {
	t.Helper()
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		s.starts.Lock()
		starting := s.starting
		s.starts.Unlock()
		if starting == "" {
			return
		}
	}
	t.Fatal("the start never ended")
}

func TestAHelperStartsThroughCeoSpawnOnABranchOffItsParentsAndTellsItsParent(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{output: "spawned g1-h1 harness=claude kind=ship"}
	s, h := helperBoard(t, 6*gigabyte, spawner)

	// Act
	started, err := s.acceptHelper(HelperRequest{Parent: "g1", Brief: helperBrief, Title: "Accounts migration"})
	calls := waitForCalls(t, spawner, 2)
	waitStartEnds(t, s)

	// Assert
	if err != nil {
		t.Fatalf("acceptHelper: %v", err)
	}
	if started.ID != "g1-h1" || started.Branch != "feat/x-h1" {
		t.Errorf("started = %+v, want g1-h1 on feat/x-h1", started)
	}
	brief := filepath.Join(h.Data, "g1-h1", "brief.md")
	if data, err := os.ReadFile(brief); err != nil || string(data) != helperBrief {
		t.Errorf("brief = %q, %v; want the goblin's text", data, err)
	}
	want := []string{"spawn", "g1-h1", "--project", filepath.Join(h.Root, "app"), "--brief", brief, "--mode", "local-only", "--parent", "g1", "--title", "Accounts migration"}
	if !slices.Equal(calls[0], want) {
		t.Errorf("goblins ran %q\nwant %q", calls[0], want)
	}
	if told := calls[1]; len(told) != 3 || told[0] != "send" || told[1] != "g1" || !strings.Contains(told[2], "Your helper g1-h1 is up on branch feat/x-h1") {
		t.Errorf("the parent was told %q, want its helper up", told)
	}
	records, err := wake.Pending(h.State)
	if err != nil || !slices.ContainsFunc(records, func(record wake.Record) bool {
		return record.Key == "g1-h1" && strings.HasPrefix(record.Detail, "started: helper of g1")
	}) {
		t.Errorf("wake records = %+v, %v; want the CEO told the helper started", records, err)
	}
}

func TestHelperRoutesItsOwnBriefUnderADeepParent(t *testing.T) {
	for _, test := range []struct {
		name, brief string
		flags       []string
	}{
		{"ordinary", "## Task\nAdd a search field.\n", nil},
		{"bounded", "## Task\nRename the label to Search.\n", nil},
		{"harness", "## Task\nFix the label.\nharness: codex\n", []string{"--harness", "codex"}},
		{"provider model", "## Task\nFix the label.\nmodel: provider/model-id\n", []string{"--model", "provider/model-id"}},
		{"effort", "## Task\nFix the label.\neffort: medium\n", []string{"--effort", "medium"}},
		{"default effort", "## Task\nFix the label.\neffort: default\n", []string{"--effort", "default"}},
		{"ownership and credentials stay inherited", "## Project\nother-project\n## Task\nFix the label.\n## Authentication\ncredentials: extra-service\nmcp: extra-server\n", nil},
		{"all overrides", "## Task\nFix the label.\n## Delivery\nharness: codex\nmodel: gpt-6.1-sol\neffort: high\nmode: local-only\n", []string{"--harness", "codex", "--model", "gpt-6.1-sol", "--effort", "high"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			s, h := helperBoard(t, 6*gigabyte, spawner)
			// Act
			_, err := s.acceptHelper(HelperRequest{Parent: "g1", Brief: test.brief})
			if err != nil {
				t.Fatal(err)
			}
			calls := waitForCalls(t, spawner, 2)
			waitStartEnds(t, s)
			// Assert
			want := []string{"spawn", "g1-h1", "--project", filepath.Join(h.Root, "app"), "--brief", filepath.Join(h.Data, "g1-h1", "brief.md")}
			want = append(want, test.flags...)
			want = append(want, "--mode", "local-only", "--parent", "g1")
			if !slices.Equal(calls[0], want) {
				t.Fatalf("goblins ran %q, want %q", calls[0], want)
			}
		})
	}
}

func TestHelperRejectsInvalidSettingsBeforeWritingOrDispatching(t *testing.T) {
	for _, setting := range []string{"harness: shell", "model: invalid$value", "effort: invalid$value", "effort: banana", "model: invalid model", "effort: high low", "harness: codex claude", "mode: direct-PR", "mode: no-mistakes", "mode: unknown"} {
		t.Run(setting, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			s, h := helperBoard(t, 6*gigabyte, spawner)
			// Act
			_, err := s.acceptHelper(HelperRequest{Parent: "g1", Brief: "## Task\nFix the label.\n" + setting + "\n"})
			// Assert
			if err == nil {
				waitStartEnds(t, s)
				t.Fatal("invalid settings accepted")
			}
			if len(spawner.recorded()) != 0 {
				t.Fatal("invalid helper dispatched")
			}
			for _, path := range []string{filepath.Join(h.Data, "g1-h1"), filepath.Join(h.State, "g1-h1.status")} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("invalid helper left %s: %v", path, err)
				}
			}
			if s.starting != "" {
				t.Fatal("invalid helper retained the start slot")
			}
		})
	}
}

func TestAHelperThatCannotStartIsReportedToItsParent(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{output: "goblins spawn: the brief's project could not be read", err: os.ErrInvalid}
	s, h := helperBoard(t, 6*gigabyte, spawner)

	// Act
	_, err := s.acceptHelper(HelperRequest{Parent: "g1", Brief: helperBrief})
	calls := waitForCalls(t, spawner, 2)
	waitStartEnds(t, s)

	// Assert
	if err != nil {
		t.Fatalf("acceptHelper: %v", err)
	}
	if told := calls[1]; told[0] != "send" || told[1] != "g1" || !strings.Contains(told[2], "could not start: goblins spawn: the brief's project could not be read") {
		t.Errorf("the parent was told %q, want why its helper did not start", told)
	}
	records, _ := wake.Pending(h.State)
	if !slices.ContainsFunc(records, func(record wake.Record) bool { return strings.HasPrefix(record.Detail, "start failed: helper of g1") }) {
		t.Errorf("wake records = %+v, want the CEO told the start failed", records)
	}
}

func TestAHelperIsRefusedWithWhyAndWhenToAskAgain(t *testing.T) {
	cases := []struct {
		name      string
		available uint64
		arrange   func(t *testing.T, s *Service, h home.Home)
		request   HelperRequest
		want      []string
	}{
		{"under the memory mark", 4 * gigabyte, nil, HelperRequest{Parent: "g1", Brief: helperBrief}, []string{"5 GB", "ask again"}},
		{"a helper of a helper", 6 * gigabyte, func(t *testing.T, s *Service, h home.Home) {
			writeHelperTask(t, h, state.TaskMeta{ID: "g1-h1", Parent: "g1", Mode: "local-only"})
		}, HelperRequest{Parent: "g1-h1", Brief: helperBrief}, []string{"a helper cannot start helpers"}},
		{"a second helper", 6 * gigabyte, func(t *testing.T, s *Service, h home.Home) {
			writeHelperTask(t, h, state.TaskMeta{ID: "g1-h1", Parent: "g1", Mode: "local-only"})
		}, HelperRequest{Parent: "g1", Brief: helperBrief}, []string{"one helper at a time", "ask again once you have merged it"}},
		{"a paused parent", 6 * gigabyte, func(t *testing.T, s *Service, h home.Home) {
			record := state.Lifecycle{ID: "g1", Generation: "s1", Operation: "op-1", Action: "pause", Phase: "paused", Pause: &state.PauseCondition{Reason: "overlord", At: time.Now().UTC()}}
			if err := state.WriteLifecycle(h.State, record); err != nil {
				t.Fatal(err)
			}
		}, HelperRequest{Parent: "g1", Brief: helperBrief}, []string{"g1 is paused"}},
		{"another start under way", 6 * gigabyte, func(t *testing.T, s *Service, h home.Home) {
			s.starting = "next-task"
		}, HelperRequest{Parent: "g1", Brief: helperBrief}, []string{"next-task is starting", "ask again in a minute"}},
		{"no brief", 6 * gigabyte, nil, HelperRequest{Parent: "g1", Brief: " \n"}, []string{"a brief"}},
		{"busy performance cores", 6 * gigabyte, func(t *testing.T, s *Service, h home.Home) {
			s.Options.Dispatch.Processors = processorsWithFree(0.1)
		}, HelperRequest{Parent: "g1", Brief: helperBrief}, []string{"Only 0.6 of 6 performance cores are free", "ask again in ten minutes"}},
		// Behind a run waiting for 6 GB on a line of two slots the line's
		// next floor is 8 GB, so 6 GB free leaves no room for a helper's
		// session beside it (#1926).
		{"the heavy line keeping its next floor", 6 * gigabyte, func(t *testing.T, s *Service, h home.Home) {
			s.Options.Dispatch.HeavyLine = MachineHeavyLine
			heavyLine(t, 2, 6*gigabyte)
		}, HelperRequest{Parent: "g1", Brief: helperBrief}, []string{"no helper starts, keeping the test slots open: 6.0 GB of memory and commit is free; the next test run needs 8.0 GB and a new goblin 1 GB", "ask again in ten minutes"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			s, h := helperBoard(t, test.available, spawner)
			if test.arrange != nil {
				test.arrange(t, s, h)
			}

			// Act
			_, err := s.acceptHelper(test.request)

			// Assert
			if err == nil {
				t.Fatal("acceptHelper = nil, want refused")
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not say %q", err, want)
				}
			}
			if calls := spawner.recorded(); len(calls) != 0 {
				t.Errorf("goblins ran %q for a refused helper", calls)
			}
			if _, statErr := os.Stat(filepath.Join(h.Data, "g1-h1")); statErr == nil && test.name != "a helper of a helper" && test.name != "a second helper" {
				t.Errorf("a refused helper left its brief folder")
			}
		})
	}
}

// A heavy line that cannot be read holds no helper, as processors that
// cannot be read hold none, and the board says it cannot be read.
func TestAHelperStartsWhenTheHeavyLineCannotBeReadAndTheBoardSaysSo(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{output: "spawned g1-h1 harness=claude kind=ship"}
	s, _ := helperBoard(t, 6*gigabyte, spawner)
	s.Options.Dispatch.HeavyLine = func(Memory) (LineRoom, error) { return LineRoom{}, errors.New("the slots' records are locked") }

	// Act
	_, err := s.acceptHelper(HelperRequest{Parent: "g1", Brief: helperBrief})
	calls := waitForCalls(t, spawner, 1)
	waitStartEnds(t, s)

	// Assert
	if err != nil || calls[0][0] != "spawn" || calls[0][1] != "g1-h1" {
		t.Fatalf("acceptHelper = %v, goblins ran %q; want g1-h1 spawned", err, calls)
	}
	s.mu.Lock()
	reported := s.lastError
	s.mu.Unlock()
	if !strings.Contains(reported, "the machine's heavy line cannot be read, so no start waits on it: the slots' records are locked") {
		t.Errorf("the board's error is %q, want that the heavy line cannot be read", reported)
	}
}

func TestAHelperIsRefusedForAParentOnNoBranch(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	s, h := helperBoard(t, 6*gigabyte, spawner)
	meta, err := state.ReadTaskMeta(h.State, "g1")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", meta.Worktree, "switch", "-q", "--detach").CombinedOutput(); err != nil {
		t.Fatalf("detach: %v\n%s", err, out)
	}

	// Act
	_, err = s.acceptHelper(HelperRequest{Parent: "g1", Brief: helperBrief})

	// Assert
	if err == nil || !strings.Contains(err.Error(), "on no branch") || !strings.Contains(err.Error(), "then ask again") {
		t.Errorf("acceptHelper = %v, want a detached parent refused", err)
	}
}

// One helper start at a time holds the start slot as the board's Start does,
// so a second request and a Start wait behind it.
func TestAHelperStartHoldsTheStartSlotUntilItsSpawnEnds(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{release: make(chan struct{})}
	s, _ := helperBoard(t, 6*gigabyte, spawner)

	// Act
	if _, err := s.acceptHelper(HelperRequest{Parent: "g1", Brief: helperBrief}); err != nil {
		t.Fatal(err)
	}
	waitForCalls(t, spawner, 1)
	_, second := s.acceptHelper(HelperRequest{Parent: "g1", Brief: helperBrief})
	startErr := s.startQueued("next-task", true)
	close(spawner.release)
	waitStartEnds(t, s)

	// Assert
	if second == nil || !strings.Contains(second.Error(), "g1-h1 is starting") {
		t.Errorf("a second request while g1-h1 starts = %v, want it told to wait", second)
	}
	if startErr == nil || !strings.Contains(startErr.Error(), "g1-h1 is starting") {
		t.Errorf("a Start while g1-h1 starts = %v, want it told to wait", startErr)
	}
}

func TestAHelperPausedWithItsParentResumesOnceItsParentRunsAgain(t *testing.T) {
	for _, test := range []struct {
		name      string
		isParent  bool
		isRunning bool
		isResumed bool
	}{
		{"its parent resumed", true, true, true},
		{"its parent still paused", true, false, false},
		{"paused by itself while its parent runs", false, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: the Overlord paused the parent, which paused its
			// helper with the same condition, or the helper on its own.
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 8*gigabyte, spawner)
			s := handler.Service
			at := time.Now().UTC().Add(-time.Hour)
			parent := pausedGoblin(t, h, "fam", "overlord", "", at)
			if test.isRunning {
				if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: parent.ID, Generation: parent.SpawnGen, Operation: "resume-fam", Action: "resume", Phase: "running", Started: at, Updated: at}); err != nil {
					t.Fatal(err)
				}
			}
			helper := state.TaskMeta{ID: "fam-h1", Parent: parent.ID, SpawnGen: "generation-fam-h1", Backend: "native", Project: h.Root, Worktree: filepath.Join(h.Root, ".worktrees", "gb-fam-h1")}
			if err := state.WriteTaskMeta(h.State, helper); err != nil {
				t.Fatal(err)
			}
			operation := "pause-fam-h1"
			if test.isParent {
				operation = state.HelperOperation("pause-fam", helper.ID)
			}
			if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: helper.ID, Generation: helper.SpawnGen, Operation: operation, Action: "pause", Phase: "paused", Started: at, Updated: at, Pause: &state.PauseCondition{Reason: "overlord", At: at}, Reason: "overlord"}); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n")
			s.Options.Dispatch.Memory = (&memoryReadings{readings: [][2]float64{{8, 8}, {8, 8}}}).read

			// Act
			err := s.checkFleet(t.Context(), time.Now().UTC())

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if !test.isResumed {
				s.starts.Lock()
				changing := len(s.changing)
				s.starts.Unlock()
				if calls := spawner.recorded(); changing != 0 || len(calls) != 0 {
					t.Fatalf("dispatched %q (%d changing), want the helper left paused", calls, changing)
				}
				return
			}
			calls := awaitDispatch(t, s, spawner, 1)
			if len(calls) != 1 || calls[0][0] != "resume" || calls[0][1] != helper.ID {
				t.Fatalf("dispatched %q, want the helper resumed", calls)
			}
		})
	}
}

func TestTheBoardSaysWhichPausedHelperResumesWithItsParent(t *testing.T) {
	for _, test := range []struct {
		name         string
		operation    string
		isWithParent bool
	}{
		{"paused with its parent", state.HelperOperation("pause-fam", "fam-h1"), true},
		{"paused by itself", "pause-fam-h1", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			handler, h := startBoard(t, 8*gigabyte, &spawnRecorder{})
			at := time.Now().UTC().Add(-time.Hour)
			helper := state.TaskMeta{ID: "fam-h1", Parent: "fam", SpawnGen: "generation-fam-h1", Backend: "native", Project: h.Root, Worktree: filepath.Join(h.Root, ".worktrees", "gb-fam-h1")}
			if err := state.WriteTaskMeta(h.State, helper); err != nil {
				t.Fatal(err)
			}
			if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: helper.ID, Generation: helper.SpawnGen, Operation: test.operation, Action: "pause", Phase: "paused", Started: at, Updated: at, Pause: &state.PauseCondition{Reason: "overlord", At: at}, Reason: "overlord"}); err != nil {
				t.Fatal(err)
			}

			// Act
			snapshot, err := handler.Service.Snapshot()

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			index := slices.IndexFunc(snapshot.Tasks, func(task Task) bool { return task.ID == helper.ID })
			if index < 0 || snapshot.Tasks[index].Lifecycle == nil || snapshot.Tasks[index].Lifecycle.WithParent != test.isWithParent {
				t.Fatalf("the board's tasks are %+v, want %s with_parent %v", snapshot.Tasks, helper.ID, test.isWithParent)
			}
		})
	}
}
