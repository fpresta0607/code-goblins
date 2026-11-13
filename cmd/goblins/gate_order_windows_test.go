package main

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// A gate's Test step, as its wrapper's --gate-run names it, passes a goblin's
// own turn that asked first. goblins gate turns says why the goblin's run waits
// behind it, and goblins gate turns --json keeps every key and adds none.
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
		t.Errorf("goblins gate turns exited %d and printed %q; want a line holding %q", turnsExit, turns.String(), want)
	}
	var report struct {
		Waiting []map[string]any `json:"waiting"`
	}
	if err := json.Unmarshal(turnsJSON.Bytes(), &report); jsonExit != 0 || err != nil || len(report.Waiting) != 2 {
		t.Fatalf("goblins gate turns --json exited %d and printed %q (%v); want two waiting runs", jsonExit, turnsJSON.String(), err)
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
		t.Setenv("GOBLINS_TASK_ID", task)
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
