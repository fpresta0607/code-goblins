package supervisor

import (
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

const helperBrief = "Write the migration for the accounts table.\n"

// helperBoard is a supervisor whose goblin g1 works on branch feat/x in a
// real repository, on a machine with available memory and ample disk and
// commit, whose cfo spawn and cfo send are spawner.
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
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if calls := spawner.recorded(); len(calls) >= n {
			return calls
		}
	}
	t.Fatalf("cfo ran %q, want %d commands", spawner.recorded(), n)
	return nil
}

// waitStartEnds waits until the start s runs has ended and told everyone.
func waitStartEnds(t *testing.T, s *Service) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		s.starts.Lock()
		starting := s.starting
		s.starts.Unlock()
		if starting == "" {
			return
		}
	}
	t.Fatal("the start never ended")
}

func TestAHelperStartsThroughCfoSpawnOnABranchOffItsParentsAndTellsItsParent(t *testing.T) {
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
	want := []string{"spawn", "g1-h1", "--project", filepath.Join(h.Root, "app"), "--brief", brief, "--harness", "claude", "--model", "claude-opus-5-5", "--effort", "xhigh", "--mode", "local-only", "--parent", "g1", "--title", "Accounts migration"}
	if !slices.Equal(calls[0], want) {
		t.Errorf("cfo ran %q\nwant %q", calls[0], want)
	}
	if told := calls[1]; len(told) != 3 || told[0] != "send" || told[1] != "g1" || !strings.Contains(told[2], "Your helper g1-h1 is up on branch feat/x-h1") {
		t.Errorf("the parent was told %q, want its helper up", told)
	}
	records, err := wake.Pending(h.State)
	if err != nil || !slices.ContainsFunc(records, func(record wake.Record) bool {
		return record.Key == "g1-h1" && strings.HasPrefix(record.Detail, "started: helper of g1")
	}) {
		t.Errorf("wake records = %+v, %v; want the CFO told the helper started", records, err)
	}
}

func TestAHelperThatCannotStartIsReportedToItsParent(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{output: "cfo spawn: the brief's project could not be read", err: os.ErrInvalid}
	s, h := helperBoard(t, 6*gigabyte, spawner)

	// Act
	_, err := s.acceptHelper(HelperRequest{Parent: "g1", Brief: helperBrief})
	calls := waitForCalls(t, spawner, 2)
	waitStartEnds(t, s)

	// Assert
	if err != nil {
		t.Fatalf("acceptHelper: %v", err)
	}
	if told := calls[1]; told[0] != "send" || told[1] != "g1" || !strings.Contains(told[2], "could not start: cfo spawn: the brief's project could not be read") {
		t.Errorf("the parent was told %q, want why its helper did not start", told)
	}
	records, _ := wake.Pending(h.State)
	if !slices.ContainsFunc(records, func(record wake.Record) bool { return strings.HasPrefix(record.Detail, "start failed: helper of g1") }) {
		t.Errorf("wake records = %+v, want the CFO told the start failed", records)
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
				t.Errorf("cfo ran %q for a refused helper", calls)
			}
			if _, statErr := os.Stat(filepath.Join(h.Data, "g1-h1")); statErr == nil && test.name != "a helper of a helper" && test.name != "a second helper" {
				t.Errorf("a refused helper left its brief folder")
			}
		})
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
