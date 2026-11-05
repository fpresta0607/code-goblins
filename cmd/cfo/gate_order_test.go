package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// taskHome gives the test a fleet home whose backlog says backlog, and returns
// its data folder.
func taskHome(t *testing.T, backlog string) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CFO_HOME", root)
	t.Setenv("CFO_STATE_OVERRIDE", "")
	t.Setenv("NO_MISTAKES_GATE", "")
	data := filepath.Join(root, "data")
	for _, dir := range []string{data, filepath.Join(root, "state")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(data, "backlog.md"), []byte(backlog), 0o644); err != nil {
		t.Fatal(err)
	}
	return data
}

// inLine waits until the runs waiting in slots pass ok, for at most a minute,
// and returns them.
func inLine(t *testing.T, slots, what string, ok func(waiting []verify.Standing) bool) []verify.Standing {
	t.Helper()
	var waiting []verify.Standing
	eventually(t, what, func() bool {
		var err error
		_, waiting, err = verify.Line(slots)
		return err == nil && ok(waiting)
	})
	return waiting
}

// A gate's Test step, as its wrapper's --gate-run names it, passes a goblin's
// own turn that asked first. cfo gate turns says why the goblin's run waits
// behind it, and cfo gate turns --json keeps every key and adds none.
func TestAGateTestStepPassesAQueuedGoblinTurn(t *testing.T) {
	// Arrange
	_, comparison := gateEnvironmentFixture(t)
	slots := turnLine(t)
	dir := commandFolder(t)
	holder := startGateTurn(t, dir, nil, turnCommand(dir, "holder", 0, true)...)
	eventually(t, "the holder started", func() bool { return left(dir, "holder", "started") })
	goblin := startGateTurn(t, dir, nil, append([]string{"--as", "a goblin's own turn"}, turnCommand(dir, "goblin", 0, false)...)...)
	inLine(t, slots, "the goblin's own turn waits", func(waiting []verify.Standing) bool { return len(waiting) == 1 })
	var stepOut, stepErrors lockedBuffer
	stepEnded := make(chan int, 1)
	go func() {
		stepEnded <- gateTurn(turnRuntime(), &stepOut, &stepErrors, append([]string{"--as", "a gate's Test step", "--gate-run", "run", "--test-comparison-sha", comparison}, turnCommand(dir, "step", 0, true)...)...)
	}()
	inLine(t, slots, "the Test step stands first", func(waiting []verify.Standing) bool {
		return len(waiting) == 2 && strings.HasPrefix(waiting[0].Who, "a gate's Test step")
	})
	var turns, turnsJSON, turnsErrors bytes.Buffer

	// Act
	turnsExit := runGateTurns(&turns, &turnsErrors, plenty)
	jsonExit := runGateTurnsJSON(&turnsJSON, &turnsErrors, plenty)
	letGo(t, dir, "holder")
	eventually(t, "the Test step started", func() bool { return left(dir, "step", "started") })
	hasGoblinStarted := left(dir, "goblin", "started")
	letGo(t, dir, "step")

	// Assert
	if exit := holder.exit(t, "the holder"); exit != 0 {
		t.Errorf("the holder exited %d; want the turn it had started left to run to its end", exit)
	}
	select {
	case exit := <-stepEnded:
		if exit != 0 {
			t.Errorf("the Test step exited %d; stderr=%q", exit, stepErrors.String())
		}
	case <-time.After(time.Minute):
		t.Fatalf("the Test step had not ended; stderr=%q", stepErrors.String())
	}
	if exit := goblin.exit(t, "the goblin's own turn"); exit != 0 || hasGoblinStarted {
		t.Errorf("the goblin's own turn exited %d, started before the Test step = %v; want 0 and false", exit, hasGoblinStarted)
	}
	if want := ", behind run 1: a gate's Test step goes ahead of a goblin's own turn"; turnsExit != 0 || !strings.Contains(turns.String(), want) {
		t.Errorf("cfo gate turns exited %d and printed %q; want a line holding %q", turnsExit, turns.String(), want)
	}
	var report struct {
		Waiting []map[string]any `json:"waiting"`
	}
	if err := json.Unmarshal(turnsJSON.Bytes(), &report); jsonExit != 0 || err != nil || len(report.Waiting) != 2 {
		t.Fatalf("cfo gate turns --json exited %d and printed %q (%v); want two waiting runs", jsonExit, turnsJSON.String(), err)
	}
	want := []string{"budget_seconds", "estimate_bytes", "is_low_priority", "now", "pid", "since", "used_bytes", "who"}
	for _, run := range report.Waiting {
		if keys := slices.Sorted(maps.Keys(run)); !slices.Equal(keys, want) {
			t.Errorf("a waiting run's keys are %q; want exactly %q, as the board reads them", keys, want)
		}
	}
}

// A goblin's own turn takes the priority on its task's backlog row to the
// line: a higher task's run passes a lower one's that asked first.
func TestGateTurnStandsInLineByItsTaskPriority(t *testing.T) {
	// Arrange
	slots := turnLine(t)
	taskHome(t, "# Backlog\n\n## Queued\n\n- [ ] cg-low - Low (repo: demo) (kind: ship) (priority: "+supervisor.RootPriority+")\n- [ ] cg-high - High (repo: demo) (kind: ship) (priority: 1)\n")
	dir := commandFolder(t)
	holder := startGateTurn(t, dir, nil, turnCommand(dir, "holder", 0, true)...)
	eventually(t, "the holder started", func() bool { return left(dir, "holder", "started") })
	// A test binary's child refuses the home it inherits, so these runs are
	// this process's. Each reads its task before it joins the line.
	turn := func(task, as, name string, isHeld bool) <-chan int {
		t.Setenv("CFO_TASK_ID", task)
		ended := make(chan int, 1)
		go func() {
			var stdout, stderr lockedBuffer
			ended <- gateTurn(turnRuntime(), &stdout, &stderr, append([]string{"--as", as}, turnCommand(dir, name, 0, isHeld)...)...)
		}()
		return ended
	}
	low := turn("cg-low", "the lower task", "low", false)
	inLine(t, slots, "the lower task waits", func(waiting []verify.Standing) bool { return len(waiting) == 1 })
	high := turn("cg-high", "the higher task", "high", true)

	// Act
	waiting := inLine(t, slots, "the higher task stands first", func(waiting []verify.Standing) bool {
		return len(waiting) == 2 && strings.HasPrefix(waiting[0].Who, "the higher task")
	})
	letGo(t, dir, "holder")
	eventually(t, "the higher task started", func() bool { return left(dir, "high", "started") })
	hasLowStarted := left(dir, "low", "started")
	letGo(t, dir, "high")

	// Assert
	if want := "task priority 1 goes ahead of task priority ops-root"; waiting[1].Behind != want {
		t.Errorf("the lower task stands behind for %q; want %q", waiting[1].Behind, want)
	}
	if exit := holder.exit(t, "the holder"); exit != 0 {
		t.Errorf("the holder exited %d; want 0", exit)
	}
	for name, ended := range map[string]<-chan int{"the higher task": high, "the lower task": low} {
		select {
		case exit := <-ended:
			if exit != 0 {
				t.Errorf("%s exited %d; want 0", name, exit)
			}
		case <-time.After(time.Minute):
			t.Fatalf("%s had not ended a minute after the line reached it", name)
		}
	}
	if hasLowStarted {
		t.Error("the lower task started before the higher one that passed it")
	}
}

// Every command that takes a turn for a task takes its task's priority with
// it: each here is of a priority under a run with no task, so that run passes
// it, and the command still ends once the line reaches it.
func TestEveryTurnOfATaskStandsByItsPriority(t *testing.T) {
	const row = "## Queued\n\n- [ ] %s - A task (repo: demo) (kind: ship) (priority: 2)\n"
	for name, arrange := range map[string]func(t *testing.T) (slots string, run func(stdout, stderr io.Writer) int){
		"cfo gate test": func(t *testing.T) (string, func(stdout, stderr io.Writer) int) {
			t.Chdir(testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"}))
			taskHome(t, strings.Replace(row, "%s", "cg-example", 1))
			t.Setenv(verify.TurnVariable, "")
			t.Setenv("CFO_TASK_ID", "cg-example")
			return filepath.Join(os.Getenv("CFO_VERIFY_DIR"), "slots"), func(stdout, stderr io.Writer) int { return gateTestWith(standIn(), stdout, stderr) }
		},
		"cfo gate prepush": func(t *testing.T) (string, func(stdout, stderr io.Writer) int) {
			t.Chdir(prepushModule(t))
			taskHome(t, strings.Replace(row, "%s", "cg-example", 1))
			t.Setenv(verify.TurnVariable, "")
			t.Setenv("CFO_TASK_ID", "cg-example")
			runtime := turnRuntime()
			runtime.gateDisk = roomy
			runtime.pushRun = func(context.Context, []string, string, []string, io.Writer, io.Writer) (int, error) { return 0, nil }
			return filepath.Join(os.Getenv("CFO_VERIFY_DIR"), "slots"), func(stdout, stderr io.Writer) int { return gatePrepushWith(runtime, stdout, stderr) }
		},
		"cfo verify": func(t *testing.T) (string, func(stdout, stderr io.Writer) int) {
			runtime, _ := measuredTask(t, `"verification": {"fast": [["git", "version"]]}`)
			h, err := runtime.resolveHome()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(h.Data, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(h.Data, "backlog.md"), []byte(strings.Replace(row, "%s", "t1", 1)), 0o644); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(os.Getenv("CFO_VERIFY_DIR"), "slots"), func(stdout, stderr io.Writer) int {
				return runWithRuntime([]string{"verify", "t1"}, stdout, stderr, runtime)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			slots, run := arrange(t)
			lineRun := verify.Admission{Dir: slots, Available: func() (uint64, error) { return 16 << 30, nil }, Limit: time.Minute, Poll: 10 * time.Millisecond}
			holding := lineRun
			holding.Who = "the holder"
			holder, err := holding.Wait(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Release()
			var stdout, stderr lockedBuffer
			exited := make(chan int, 1)
			go func() { exited <- run(&stdout, &stderr) }()
			inLine(t, slots, "the task's run waits", func(waiting []verify.Standing) bool { return len(waiting) == 1 })
			noTask := lineRun
			noTask.Who = "a run with no task"
			passed := make(chan error, 1)
			go func() {
				turn, err := noTask.Wait(context.Background())
				passed <- errors.Join(err, turn.Release())
			}()

			// Act
			waiting := inLine(t, slots, "the run with no task stands first", func(waiting []verify.Standing) bool {
				return len(waiting) == 2 && waiting[0].Who == noTask.Who
			})
			if err := holder.Release(); err != nil {
				t.Fatal(err)
			}

			// Assert
			if want := "no task priority goes ahead of task priority 2"; waiting[1].Behind != want {
				t.Errorf("the task's run stands behind for %q; want %q", waiting[1].Behind, want)
			}
			if err := <-passed; err != nil {
				t.Errorf("the run with no task: %v", err)
			}
			select {
			case exit := <-exited:
				if exit != 0 {
					t.Errorf("the task's run exited %d; stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
				}
			case <-time.After(time.Minute):
				t.Fatalf("the task's run had not ended a minute after its turn came; stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}
}

// A task's priority is what its backlog row says, wherever the row stands.
// What names none leaves the run the place of a run with no task.
func TestTaskPriorityIsWhatTheBacklogRowSays(t *testing.T) {
	const backlog = "## Queued\n\n" +
		"- [ ] cg-queued - A task (repo: demo) (priority: 2)\n" +
		"- [ ] cg-defect - A task (repo: demo) (priority: production-defect)\n" +
		"- [ ] cg-plain - A task (repo: demo)\n" +
		"- [ ] cg-held - A task (repo: demo) (hold-kind: parked) (priority: 3)\n\n" +
		"## Parked\n\n- [ ] cg-parked - A task (repo: demo) (priority: 1)\n"
	for task, want := range map[string]string{"cg-queued": "2", "cg-defect": "production-defect", "cg-plain": "", "cg-held": "3", "cg-parked": "1", "cg-no-row": "", "": ""} {
		t.Run(task, func(t *testing.T) {
			// Arrange
			taskHome(t, backlog)
			var says bytes.Buffer

			// Act
			priority := taskPriority("cfo gate turn", task, &says, defaultCommandRuntime())

			// Assert
			if priority != want || says.Len() != 0 {
				t.Errorf("taskPriority(%q) = %q and said %q; want %q and nothing said", task, priority, says.String(), want)
			}
		})
	}
}

// A run whose priority cannot be read still stands in line, and says so.
func TestTaskPrioritySaysWhenItCannotBeRead(t *testing.T) {
	for name, runtime := range map[string]func(t *testing.T) commandRuntime{
		"an unreadable backlog": func(t *testing.T) commandRuntime {
			data := taskHome(t, "")
			if err := os.Remove(filepath.Join(data, "backlog.md")); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(data, "backlog.md"), 0o755); err != nil {
				t.Fatal(err)
			}
			return defaultCommandRuntime()
		},
		"no home": func(*testing.T) commandRuntime {
			runtime := defaultCommandRuntime()
			runtime.resolveHome = func() (home.Home, error) { return home.Home{}, os.ErrNotExist }
			return runtime
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			var says bytes.Buffer

			// Act
			priority := taskPriority("cfo gate turn", "cg-example", &says, runtime(t))

			// Assert
			if priority != "" || !strings.HasPrefix(says.String(), "cfo gate turn: its task's priority cannot be read, so it takes a place in line as a run with no task: ") {
				t.Errorf("taskPriority = %q and said %q; want none, and why under the run's own name", priority, says.String())
			}
		})
	}
}
