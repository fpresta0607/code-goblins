package supervisor

import (
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func resumeClick(handler *HTTP, meta state.TaskMeta, operation string) int {
	return taskControlRequest(handler, "/api/tasks/lifecycle", map[string]string{"task": meta.ID, "generation": meta.SpawnGen, "operation": operation, "action": "resume"}).Code
}

func startClick(handler *HTTP, id string) int {
	return postStart(handler, `{"task":"`+id+`"}`, "board.local", "http://board.local", orderToken).Code
}

// cardOf is task id's card in a fresh snapshot.
func cardOf(t *testing.T, handler *HTTP, id string) Task {
	t.Helper()
	snapshot, err := handler.Service.SnapshotSince(handler.Service.Revision())
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(snapshot.Tasks, func(task Task) bool { return task.ID == id })
	if index < 0 {
		t.Fatalf("the snapshot lists no %s: %+v", id, snapshot.Tasks)
	}
	return snapshot.Tasks[index]
}

// awaitCalls waits for cfo to have been run count times, and gives a moment
// more for a call too many to show.
func awaitCalls(t *testing.T, spawner *spawnRecorder, count int) [][]string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(spawner.recorded()) < count && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	return spawner.recorded()
}

// awaitSettled waits for every start and change the board runs to end.
func awaitSettled(t *testing.T, s *Service) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.starts.Lock()
		isBusy := s.starting != "" || len(s.changing) > 0
		s.starts.Unlock()
		if !isBusy {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("a start or a change is still under way")
}

func commands(calls [][]string) []string {
	var named []string
	for _, call := range calls {
		named = append(named, call[0]+" "+call[1])
	}
	return named
}

// A Resume clicked while another goblin starts is accepted at once, its card
// says Resuming, and it runs as soon as that start ends: one click.
func TestAResumeClickedWhileAnotherGoblinStartsRunsOnceThatStartEnds(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{release: make(chan struct{})}
	handler, h := startBoard(t, 16*gigabyte, spawner)
	queueBriefedTask(t, h, "- **next-task** - Ship it", plainBrief)
	paused := pausedGoblin(t, h, "paused-task", "overlord", "", time.Now().Add(-time.Minute))
	if code := startClick(handler, "next-task"); code != 202 {
		t.Fatalf("start = %d, want 202", code)
	}

	// Act
	code := resumeClick(handler, paused, "resume-1")
	card := cardOf(t, handler, paused.ID)
	close(spawner.release)
	calls := awaitCalls(t, spawner, 2)
	awaitSettled(t, handler.Service)

	// Assert
	if code != 202 {
		t.Fatalf("resume = %d, want it accepted while the other goblin starts", code)
	}
	if card.Phase != "resuming" || !card.Asked {
		t.Fatalf("card while it waits its turn = %+v, want it resuming and waiting its turn", card)
	}
	if got := commands(calls); !slices.Equal(got, []string{"spawn next-task", "resume paused-task"}) {
		t.Fatalf("cfo ran %q, want the start and then the resume", got)
	}
}

// A Start clicked while another goblin starts is accepted at once, its card
// says Starting, and it starts as soon as the first is up.
func TestAStartClickedWhileAnotherGoblinStartsRunsOnceThatStartEnds(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{release: make(chan struct{})}
	handler, h := startBoard(t, 16*gigabyte, spawner)
	queueBriefedTask(t, h, "- **next-task** - Ship it\n- **second** - Also", plainBrief)
	writeFile(t, h.Data+"/second/brief.md", plainBrief)
	if code := startClick(handler, "next-task"); code != 202 {
		t.Fatalf("first start = %d, want 202", code)
	}

	// Act
	code := startClick(handler, "second")
	card := cardOf(t, handler, "second")
	close(spawner.release)
	calls := awaitCalls(t, spawner, 2)
	awaitSettled(t, handler.Service)

	// Assert
	if code != 202 {
		t.Fatalf("second start = %d, want it accepted while the first starts", code)
	}
	if !card.Starting || !card.Asked {
		t.Fatalf("card while it waits its turn = %+v, want it starting and waiting its turn", card)
	}
	if got := commands(calls); !slices.Equal(got, []string{"spawn next-task", "spawn second"}) {
		t.Fatalf("cfo ran %q, want both starts, one after the other", got)
	}
}

// A second click on Resume or Start while the first is under way, or still
// waits its turn, is accepted and changes nothing: cfo runs once.
func TestASecondClickWhileTheFirstIsUnderWayRunsNothingTwice(t *testing.T) {
	for _, test := range []struct {
		name      string
		isWaiting bool
		isStart   bool
	}{
		{name: "resume under way"},
		{name: "resume waiting its turn", isWaiting: true},
		{name: "start under way", isStart: true},
		{name: "start waiting its turn", isWaiting: true, isStart: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{release: make(chan struct{})}
			handler, h := startBoard(t, 16*gigabyte, spawner)
			queueBriefedTask(t, h, "- **next-task** - Ship it\n- **other** - Other", plainBrief)
			writeFile(t, h.Data+"/other/brief.md", plainBrief)
			paused := pausedGoblin(t, h, "paused-task", "overlord", "", time.Now().Add(-time.Minute))
			if test.isWaiting && startClick(handler, "other") != 202 {
				t.Fatal("the start that holds the turn was refused")
			}
			click := func(operation string) int {
				if test.isStart {
					return startClick(handler, "next-task")
				}
				return resumeClick(handler, paused, operation)
			}

			// Act
			first, second, third := click("resume-1"), click("resume-1"), click("resume-2")
			close(spawner.release)
			runs := 1
			if test.isWaiting {
				runs = 2
			}
			calls := awaitCalls(t, spawner, runs)
			awaitSettled(t, handler.Service)

			// Assert
			if first != 202 || second != 202 || third != 202 {
				t.Fatalf("clicks = %d, %d, %d, want every one accepted", first, second, third)
			}
			want := "resume paused-task"
			if test.isStart {
				want = "spawn next-task"
			}
			if got := commands(calls); strings.Count(strings.Join(got, ","), want) != 1 {
				t.Fatalf("cfo ran %q, want %q once", got, want)
			}
		})
	}
}

// A Start or Resume clicked while memory is under the mark is accepted, its
// card waits its turn, and it runs once memory reaches the mark.
func TestAClickWhileMemoryIsShortRunsOnceMemoryReachesTheMark(t *testing.T) {
	for _, isStart := range []bool{false, true} {
		name := "resume"
		if isStart {
			name = "start"
		}
		t.Run(name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 16*gigabyte, spawner)
			var available atomic.Uint64
			available.Store(4 * gigabyte)
			handler.Service.Options.Dispatch.Memory = func() (Memory, error) {
				return Memory{Available: available.Load(), Total: 32 * gigabyte, CommitAvailable: 40 * gigabyte, CommitLimit: 48 * gigabyte}, nil
			}
			queueBriefedTask(t, h, "- **next-task** - Ship it", plainBrief)
			paused := pausedGoblin(t, h, "paused-task", "overlord", "", time.Now().Add(-time.Minute))
			id, code := paused.ID, 0

			// Act
			if isStart {
				id, code = "next-task", startClick(handler, "next-task")
			} else {
				code = resumeClick(handler, paused, "resume-1")
			}
			waiting := cardOf(t, handler, id)
			handler.Service.runAsked()
			short := spawner.recorded()
			available.Store(8 * gigabyte)
			handler.Service.runAsked()
			calls := awaitCalls(t, spawner, 1)
			awaitSettled(t, handler.Service)

			// Assert
			if code != 202 {
				t.Fatalf("click = %d, want it accepted while memory is short", code)
			}
			if !waiting.Asked || isStart && !waiting.Starting || !isStart && waiting.Phase != "resuming" {
				t.Fatalf("card while memory is short = %+v, want it waiting its turn", waiting)
			}
			if len(short) != 0 {
				t.Fatalf("cfo ran %q while memory was short", commands(short))
			}
			if len(calls) != 1 || calls[0][1] != id {
				t.Fatalf("cfo ran %q, want %s once memory reached the mark", commands(calls), id)
			}
		})
	}
}

// A Pause or Stop clicked after a Resume that still waits its turn takes the
// Resume back: the goblin is paused or stopped, never resumed after it.
func TestAPauseOrStopAfterAWaitingResumeTakesItBack(t *testing.T) {
	for _, action := range []string{"pause", "stop"} {
		t.Run(action, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{release: make(chan struct{})}
			handler, h := startBoard(t, 16*gigabyte, spawner)
			queueBriefedTask(t, h, "- **next-task** - Ship it", plainBrief)
			paused := pausedGoblin(t, h, "paused-task", "overlord", "", time.Now().Add(-time.Minute))
			if startClick(handler, "next-task") != 202 || resumeClick(handler, paused, "resume-1") != 202 {
				t.Fatal("the start or the resume was refused")
			}

			// Act
			code := taskControlRequest(handler, "/api/tasks/lifecycle", map[string]string{"task": paused.ID, "generation": paused.SpawnGen, "operation": action + "-2", "action": action}).Code
			close(spawner.release)
			calls := awaitCalls(t, spawner, 2)
			awaitSettled(t, handler.Service)

			// Assert
			command := map[string]string{"pause": "pause", "stop": "kill"}[action]
			if code != 202 || slices.Contains(commands(calls), "resume paused-task") || !slices.Contains(commands(calls), command+" paused-task") {
				t.Fatalf("%s = %d, cfo ran %q, want the %s run and the resume taken back", action, code, commands(calls), action)
			}
		})
	}
}

// Shirley's pause failed though her session was gone, and her card stayed in
// progress, saying "Pause did not finish" in yellow (2026-10-08). A goblin
// whose pause failed and whose terminal no longer runs shows paused, and
// Resume takes it.
func TestAGoblinWhosePauseFailedAndWhoseTerminalEndedShowsPausedAndResumes(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 16*gigabyte, spawner)
	meta := state.TaskMeta{ID: "shirley", SpawnGen: "generation-1", Project: h.Root, Worktree: h.Root, Backend: "native"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, RequestGeneration: meta.SpawnGen, Operation: "pause-1", Action: "pause", Phase: "failed", Updated: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}

	// Act
	card := cardOf(t, handler, meta.ID)
	code := resumeClick(handler, meta, "resume-1")
	calls := awaitCalls(t, spawner, 1)

	// Assert
	if card.Phase != "paused" || card.Lifecycle == nil || card.Lifecycle.Phase != "paused" {
		t.Fatalf("card = %+v, lifecycle %+v, want it paused", card, card.Lifecycle)
	}
	if code != 202 || len(calls) != 1 || calls[0][0] != "resume" {
		t.Fatalf("resume = %d, cfo ran %v, want it resumed", code, calls)
	}
}

// A board Resume that cfo refuses before it records anything, as goblins did
// with "usage: goblins resume", goes to the CFO, which nothing else tells.
func TestABoardResumeTheCLIRefusesTellsTheCFO(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{output: "usage: goblins resume\n", err: errors.New("exit status 2")}
	handler, h := startBoard(t, 16*gigabyte, spawner)
	paused := pausedGoblin(t, h, "paused-task", "overlord", "", time.Now().Add(-time.Minute))

	// Act
	code := resumeClick(handler, paused, "resume-1")
	awaitCalls(t, spawner, 1)
	awaitSettled(t, handler.Service)

	// Assert
	records, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if code != 202 || !slices.ContainsFunc(records, func(record wake.Record) bool {
		return record.Key == paused.ID && strings.HasPrefix(record.Detail, "resume failed: ") && strings.Contains(record.Detail, "usage: goblins resume")
	}) {
		t.Fatalf("resume = %d, wakes = %+v, want the CFO told the resume failed and why", code, records)
	}
}
