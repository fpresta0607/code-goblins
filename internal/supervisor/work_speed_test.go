package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type progressGit struct {
	head, pushed string
}

func progressWakeCount(t *testing.T, service *Service) int {
	t.Helper()
	count := 0
	for _, record := range fleetWakeRecords(t, service.Store.Home, "check") {
		if record.Key == "slow-task" && strings.HasPrefix(record.Detail, "progress_stalled:") {
			count++
		}
	}
	return count
}

func (git *progressGit) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	if request.Args[0] == "rev-parse" {
		return execx.Result{Stdout: []byte(git.head)}, nil
	}
	return execx.Result{Stdout: []byte("*\trefs/heads/feat/task\t" + git.head + "\n \trefs/remotes/origin/feat/task\t" + git.pushed + "\n")}, nil
}

type stalledGit struct {
	progressGit
	stalledDirectories []string
}

func (git *stalledGit) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if slices.Contains(git.stalledDirectories, request.Dir) {
		<-ctx.Done()
		return execx.Result{}, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return execx.Result{}, err
	}
	return git.progressGit.Run(ctx, request)
}

func TestProgressWatchMeasuresLaterGoblinsPastAStalledWorktree(t *testing.T) {
	service, h := fleetService(t)
	liveGoblin(t, h, "a-stalled-task", h.Root)
	liveGoblin(t, h, "b-later-task", h.Root)
	head := strings.Repeat("a", 40)
	service.Options.Progress = &stalledGit{progressGit: progressGit{head: head, pushed: head}, stalledDirectories: []string{filepath.Join(h.Root, ".worktrees", "gb-a-stalled-task")}}

	err := service.checkFleet(t.Context(), time.Now().UTC())

	if err == nil || !strings.Contains(err.Error(), "a-stalled-task") || strings.Contains(err.Error(), "b-later-task") {
		t.Fatalf("errors=%v, want only the stalled goblin's", err)
	}
	watched, err := readFleetWakes(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if got := watched.Progress["b-later-task"].Head; got != head {
		t.Fatalf("later goblin head=%q, want it measured past the stalled worktree", got)
	}
}

func TestProgressPassUsesOneDeadlineForAllStalledGoblins(t *testing.T) {
	service, h := fleetService(t)
	var stalledDirectories []string
	for _, id := range []string{"a-stalled", "b-stalled", "c-stalled"} {
		liveGoblin(t, h, id, h.Root)
		stalledDirectories = append(stalledDirectories, filepath.Join(h.Root, ".worktrees", "gb-"+id))
	}
	liveGoblin(t, h, "d-healthy", h.Root)
	head := strings.Repeat("a", 40)
	service.Options.Progress = &stalledGit{progressGit: progressGit{head: head, pushed: head}, stalledDirectories: stalledDirectories}
	started := time.Now()

	err := service.checkFleet(t.Context(), started.UTC())
	elapsed := time.Since(started)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("progress errors=%v, want the stalled probes' deadline", err)
	}
	if elapsed > PROGRESS_PASS_TIMEOUT+5*time.Second {
		t.Fatalf("progress pass took %s for three stalled goblins, exceeding its single %s budget", elapsed, PROGRESS_PASS_TIMEOUT)
	}
	watched, err := readFleetWakes(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if got := watched.Progress["d-healthy"].Head; got != head {
		t.Fatalf("healthy goblin head=%q, want progress recorded within the same pass", got)
	}
}

func TestProgressWatchReportsOnceAndResetsOnRealProgress(t *testing.T) {
	for _, source := range []string{"commit", "push", "gate", "report"} {
		t.Run(source, func(t *testing.T) {
			service, h := fleetService(t)
			liveGoblin(t, h, "slow-task", h.Root)
			git := &progressGit{head: strings.Repeat("a", 40), pushed: strings.Repeat("a", 40)}
			service.Options.Progress = git
			now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
			writeFile(t, filepath.Join(h.State, "slow-task.status"), now.Format(time.RFC3339)+" working: Implementing recovery\n")
			for _, elapsed := range []time.Duration{0, 19 * time.Minute, 20 * time.Minute, 21 * time.Minute} {
				if err := service.checkFleet(t.Context(), now.Add(elapsed)); err != nil {
					t.Fatal(err)
				}
				wakes := progressWakeCount(t, service)
				want := 0
				if elapsed >= 20*time.Minute {
					want = 1
				}
				if wakes != want {
					t.Fatalf("at %s progress wakes=%d, want %d", elapsed, wakes, want)
				}
			}
			switch source {
			case "commit":
				git.head = strings.Repeat("b", 40)
			case "push":
				git.pushed = strings.Repeat("b", 40)
			case "gate":
				service.Store.db.Tasks["slow-task"] = Evaluation{Generation: "s1", GateStep: "test", At: now.Add(22 * time.Minute)}
				if err := service.Store.save(); err != nil {
					t.Fatal(err)
				}
			case "report":
				writeFile(t, filepath.Join(h.State, "slow-task.status"), now.Add(22*time.Minute).Format(time.RFC3339)+" working: Recovery is tested\n")
			}
			if err := service.checkFleet(t.Context(), now.Add(22*time.Minute)); err != nil {
				t.Fatal(err)
			}
			if err := service.checkFleet(t.Context(), now.Add(41*time.Minute)); err != nil {
				t.Fatal(err)
			}
			if got := progressWakeCount(t, service); got != 1 {
				t.Fatalf("timer was not reset by %s: wakes=%d", source, got)
			}
			if err := service.checkFleet(t.Context(), now.Add(42*time.Minute)); err != nil {
				t.Fatal(err)
			}
			if got := progressWakeCount(t, service); got != 2 {
				t.Fatalf("new stall was not reported: wakes=%d", got)
			}
		})
	}
}

// A goblin whose latest report says it waits on something, finished, or
// asked the CFO expects no commit, push or report until that changes, and
// each of those has its own wake: ci_finished, done, its question. Twenty
// quiet minutes after one is nothing new, so progress_stalled stays for a
// goblin that says it is working.
func TestProgressWatchLeavesAGoblinThatSaidItWaitsOrFinished(t *testing.T) {
	for _, report := range []string{
		"waiting on ci: PR 432 is pushed and its one CI run is going",
		"waiting on overlord: his sign-in on the page",
		"done: PR https://github.com/o/r/pull/432",
		"blocked: Shall I fix the folder trust prompt? options: Fix it (Recommended) | Leave it",
	} {
		t.Run(report, func(t *testing.T) {
			// Arrange
			service, h := fleetService(t)
			liveGoblin(t, h, "slow-task", h.Root)
			service.Options.Progress = &progressGit{head: strings.Repeat("a", 40), pushed: strings.Repeat("a", 40)}
			now := time.Date(2026, 10, 7, 15, 0, 0, 0, time.UTC)
			writeFile(t, filepath.Join(h.State, "slow-task.status"), now.Format(time.RFC3339)+" "+report+"\n")

			// Act
			for _, elapsed := range []time.Duration{0, 25 * time.Minute, 50 * time.Minute} {
				if err := service.checkFleet(t.Context(), now.Add(elapsed)); err != nil {
					t.Fatal(err)
				}
			}

			// Assert
			if got := progressWakeCount(t, service); got != 0 {
				t.Fatalf("progress wakes = %d after %q, want none", got, report)
			}
		})
	}
}

func TestProgressWatchDoesNotTreatRepeatedReportsOrPausedWaitsAsWork(t *testing.T) {
	service, h := fleetService(t)
	liveGoblin(t, h, "slow-task", h.Root)
	service.Options.Progress = &progressGit{head: strings.Repeat("a", 40)}
	now := time.Now().UTC().Truncate(time.Second)
	writeFile(t, filepath.Join(h.State, "slow-task.status"), now.Format(time.RFC3339)+" working: Testing\n")
	if err := service.checkFleet(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.State, "slow-task.status"), now.Add(19*time.Minute).Format(time.RFC3339)+" working: Testing\n")
	if err := service.checkFleet(t.Context(), now.Add(20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if progressWakeCount(t, service) != 1 {
		t.Fatal("repeated report hid a stall")
	}
	pausedGoblin(t, h, "slow-task", "overlord", "", now.Add(21*time.Minute))
	if err := service.checkFleet(t.Context(), now.Add(45*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if progressWakeCount(t, service) != 1 {
		t.Fatal("intentional pause reported as a stall")
	}
}

// worktreeGit answers each worktree with its own head, pushed as it is.
type worktreeGit struct{ heads map[string]string }

func (git *worktreeGit) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	head := git.heads[request.Dir]
	if request.Args[0] == "rev-parse" {
		return execx.Result{Stdout: []byte(head)}, nil
	}
	return execx.Result{Stdout: []byte("*\trefs/heads/feat/task\t" + head + "\n \trefs/remotes/origin/feat/task\t" + head + "\n")}, nil
}

func stallWakes(t *testing.T, service *Service, id string) int {
	t.Helper()
	count := 0
	for _, record := range fleetWakeRecords(t, service.Store.Home, "check") {
		if record.Key == id && strings.HasPrefix(record.Detail, "progress_stalled:") {
			count++
		}
	}
	return count
}

func TestAParentWaitingOnItsHelperProgressesWithItsHelper(t *testing.T) {
	for _, test := range []struct {
		name     string
		awaited  string
		isFolded bool
	}{
		{"its helper", "parent-task-h1", true},
		{"another task", "other-task", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: a parent that only waits, on its helper or on another
			// task, while the awaited goblin commits.
			service, h := fleetService(t)
			liveGoblin(t, h, "parent-task", h.Root)
			liveGoblin(t, h, "other-task", h.Root)
			helper := state.TaskMeta{ID: "parent-task-h1", Parent: "parent-task", Project: h.Root, Worktree: filepath.Join(h.Root, ".worktrees", "gb-parent-task-h1"), Harness: "claude", Backend: "native", SpawnGen: "s1"}
			if err := state.WriteTaskMeta(h.State, helper); err != nil {
				t.Fatal(err)
			}
			awaited := filepath.Join(h.Root, ".worktrees", "gb-"+test.awaited)
			git := &worktreeGit{heads: map[string]string{
				filepath.Join(h.Root, ".worktrees", "gb-parent-task"): strings.Repeat("a", 40),
				filepath.Join(h.Root, ".worktrees", "gb-other-task"):  strings.Repeat("a", 40),
				helper.Worktree: strings.Repeat("a", 40),
			}}
			service.Options.Progress = git
			now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
			writeFile(t, filepath.Join(h.State, "parent-task.status"), now.Format(time.RFC3339)+" waiting on "+test.awaited+": merging its work once it is done\n")
			if err := service.checkFleet(t.Context(), now); err != nil {
				t.Fatal(err)
			}
			git.heads[awaited] = strings.Repeat("b", 40)
			if err := service.checkFleet(t.Context(), now.Add(15*time.Minute)); err != nil {
				t.Fatal(err)
			}

			// Act: 25 minutes after the parent's own last progress, 10 after
			// the awaited goblin's commit.
			err := service.checkFleet(t.Context(), now.Add(25*time.Minute))

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			watched, err := readFleetWakes(h.State)
			if err != nil {
				t.Fatal(err)
			}
			progress := watched.Progress["parent-task"]
			if !test.isFolded {
				if stallWakes(t, service, "parent-task") != 1 || !progress.At.Equal(now) {
					t.Fatalf("a wait on a task that is not its helper took that task's progress: wakes=%d progress=%+v", stallWakes(t, service, "parent-task"), progress)
				}
				return
			}
			if got := stallWakes(t, service, "parent-task"); got != 0 {
				t.Fatalf("parent waiting on its committing helper drew %d progress wakes, want none", got)
			}
			if !progress.At.Equal(now.Add(15*time.Minute)) || progress.Source != "commit (helper parent-task-h1)" {
				t.Fatalf("parent progress=%+v, want its helper's commit at %s", progress, now.Add(15*time.Minute))
			}
			// The helper's own stall is reported once, as the helper's.
			if err := service.checkFleet(t.Context(), now.Add(36*time.Minute)); err != nil {
				t.Fatal(err)
			}
			if helperWakes, parentWakes := stallWakes(t, service, "parent-task-h1"), stallWakes(t, service, "parent-task"); helperWakes != 1 || parentWakes != 0 {
				t.Fatalf("a stalled helper drew %d wakes and its waiting parent %d, want 1 and 0", helperWakes, parentWakes)
			}
			// A paused helper is watched by nobody, so its parent's stall is
			// the parent's to report.
			pause := time.Date(2026, 10, 7, 12, 40, 0, 0, time.UTC)
			if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: helper.ID, Generation: helper.SpawnGen, Operation: "pause-helper", Action: "pause", Phase: "paused", Started: pause, Updated: pause, Pause: &state.PauseCondition{Reason: "overlord", At: pause}, Reason: "overlord"}); err != nil {
				t.Fatal(err)
			}
			if err := service.checkFleet(t.Context(), now.Add(45*time.Minute)); err != nil {
				t.Fatal(err)
			}
			if got := stallWakes(t, service, "parent-task"); got != 1 {
				t.Fatalf("parent waiting on a paused helper drew %d progress wakes, want 1", got)
			}
		})
	}
}

// toolCallGoblin is a native Claude goblin inside one long tool call, read a
// minute at a time as the supervisor reads it: its family tree through the
// board's reader, over a harness whose one job is the tool call, and the
// monitor's latest look at its screen. It commits, pushes and reports nothing.
type toolCallGoblin struct {
	service *Service
	home    home.Home
	clock   time.Time
	// toolCPU is the processor time the tool call has used, screen the
	// digest the monitor read of the goblin's screen, and conversation the
	// goblin's own Claude Code transcript.
	toolCPU      time.Duration
	screen       string
	conversation string
}

func newToolCallGoblin(t *testing.T, now time.Time) *toolCallGoblin {
	t.Helper()
	service, h := fleetService(t)
	liveGoblin(t, h, "slow-task", h.Root)
	launched := now.Add(-time.Hour)
	data, err := json.Marshal(host.Record{ID: "slow-task", HostPID: os.Getpid(), ChildPID: 100, ChildStart: launched, Started: launched, Pipe: "fixture", Token: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.State, "hosts", "slow-task.json"), string(data))
	userHome := t.TempDir()
	goblin := &toolCallGoblin{service: service, home: h, clock: now, toolCPU: time.Second, screen: "the suite's first lines", conversation: filepath.Join(userHome, ".claude", "projects", "slow-task", "session-1.jsonl")}
	writeFile(t, goblin.conversation, `{"type":"user","timestamp":"`+now.Format(time.RFC3339)+`"}`+"\n")
	if err := os.Chtimes(goblin.conversation, now, now); err != nil {
		t.Fatal(err)
	}
	service.Options.Tree = &fleettree.Reader{
		Home:     userHome,
		Recorded: func(state.TaskMeta) string { return "session-1" },
		Now:      func() time.Time { return goblin.clock },
		Processes: func() ([]fleettree.Process, error) {
			return []fleettree.Process{
				{PID: 100, ParentPID: 1, Exe: "claude.exe", Created: 1, Started: launched},
				{PID: 102, ParentPID: 100, Exe: "bash.exe", Created: 2, Started: launched.Add(20 * time.Minute)},
				{PID: 103, ParentPID: 102, Exe: "node.exe", Created: 3, Started: launched.Add(20*time.Minute + time.Second), CPU: goblin.toolCPU},
			}, nil
		},
		Listeners:   func() (map[int][]int, error) { return nil, nil },
		CommandLine: func(pid int) (string, error) { return map[int]string{102: `bash -c "npm run test:browser"`, 103: "node --test"}[pid], nil },
	}
	service.Options.Progress = &progressGit{head: strings.Repeat("a", 40), pushed: strings.Repeat("a", 40)}
	writeFile(t, filepath.Join(h.State, "slow-task.status"), now.Format(time.RFC3339)+" working: Running the 432-test browser suite\n")
	return goblin
}

// read takes the supervisor's readings at minute: the monitor's look at the
// screen, the board's tree, then the stall check.
func (g *toolCallGoblin) read(t *testing.T, at time.Time) {
	t.Helper()
	g.clock = at
	if err := monitor.WriteObservation(g.home.State, monitor.Observation{TaskID: "slow-task", Endpoint: (herdr.Target{}).String(), EndpointVerdict: monitor.ProbePresent, Digest: g.screen, LastObserved: at, LastSeen: at, LastProgress: at, Health: monitor.HealthBusy, Reason: monitor.None}); err != nil {
		t.Fatal(err)
	}
	g.service.readTrees(t.Context())
	if err := g.service.checkFleet(t.Context(), at); err != nil {
		t.Fatal(err)
	}
}

// On 2026-10-08 the CFO drew about 30 progress_stalled wakes in nine hours for
// goblins plainly at work: a 432-test browser suite, a 200-run stress
// acceptance, a speech-model benchmark. A goblin inside one long tool call
// commits, pushes and reports nothing, but it is working while the tool's
// processes use the processor, its screen fills with output, or its
// transcript grows.
func TestEvidenceOfWorkKeepsALongToolCallFromStalling(t *testing.T) {
	for _, test := range []struct {
		evidence string
		work     func(t *testing.T, goblin *toolCallGoblin, at time.Time)
	}{
		{"its own processes", func(_ *testing.T, goblin *toolCallGoblin, _ time.Time) { goblin.toolCPU += 30 * time.Second }},
		{"screen output", func(_ *testing.T, goblin *toolCallGoblin, at time.Time) {
			goblin.screen = "ok " + at.Format("15:04") + " - board renders"
		}},
		{"transcript", func(t *testing.T, goblin *toolCallGoblin, at time.Time) {
			file, err := os.OpenFile(goblin.conversation, os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			_, err = fmt.Fprintf(file, "{\"type\":\"user\",\"timestamp\":%q}\n", at.Format(time.RFC3339))
			if err := errors.Join(err, file.Close(), os.Chtimes(goblin.conversation, at, at)); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.evidence, func(t *testing.T) {
			// Arrange
			now := time.Date(2026, 10, 8, 1, 30, 0, 0, time.UTC)
			goblin := newToolCallGoblin(t, now)

			// Act: 45 readings a minute apart, the work showing at each.
			for minute := 0; minute <= 45; minute++ {
				at := now.Add(time.Duration(minute) * time.Minute)
				test.work(t, goblin, at)
				goblin.read(t, at)
			}

			// Assert
			if got := progressWakeCount(t, goblin.service); got != 0 {
				t.Fatalf("a long tool call showing %s drew %d progress_stalled wakes, want none", test.evidence, got)
			}
			watched, err := readFleetWakes(goblin.home.State)
			if err != nil {
				t.Fatal(err)
			}
			if progress := watched.Progress["slow-task"]; progress.Source != test.evidence || !progress.At.Equal(now.Add(45*time.Minute)) {
				t.Fatalf("progress = %+v, want %s at the last reading", progress, test.evidence)
			}
		})
	}
}

// A goblin whose screen, transcript and processes are all still for the
// window has stopped: it wakes once, and again only after evidence of
// progress has come and stopped again.
func TestAWedgedGoblinWithAStillScreenAndAnIdleTreeWakesOnce(t *testing.T) {
	// Arrange
	now := time.Date(2026, 10, 8, 1, 30, 0, 0, time.UTC)
	goblin := newToolCallGoblin(t, now)
	readUntil := func(from, to int) {
		for minute := from; minute <= to; minute++ {
			goblin.read(t, now.Add(time.Duration(minute)*time.Minute))
		}
	}

	// Act and assert: still for 41 minutes, then output, then still again.
	readUntil(0, 19)
	if got := progressWakeCount(t, goblin.service); got != 0 {
		t.Fatalf("progress wakes = %d inside the window, want none", got)
	}
	readUntil(20, 41)
	if got := progressWakeCount(t, goblin.service); got != 1 {
		t.Fatalf("a still goblin drew %d progress wakes over 41 minutes, want exactly one", got)
	}
	goblin.screen = "the suite printed again"
	readUntil(42, 61)
	if got := progressWakeCount(t, goblin.service); got != 1 {
		t.Fatalf("progress wakes = %d within the window after new output, want still one", got)
	}
	readUntil(62, 62)
	if got := progressWakeCount(t, goblin.service); got != 2 {
		t.Fatalf("progress wakes = %d once the output stopped for the window again, want two", got)
	}
	records := fleetWakeRecords(t, goblin.home, "check")
	detail := records[len(records)-1].Detail
	for _, want := range []string{"progress_stalled: slow-task has shown no", "screen output", "transcript", "its own processes", "last progress: screen output"} {
		if !strings.Contains(detail, want) {
			t.Errorf("wake %q lacks %q", detail, want)
		}
	}
}

func TestSnapshotDoesNotReadProgressOrDurationsBeforeTheCycle(t *testing.T) {
	service, h := fleetService(t)
	liveGoblin(t, h, "progress-task", h.Root)
	service.cycle(t.Context(), false)
	if err := writeFleetWakes(h.State, fleetWakes{
		Progress:  map[string]WorkProgress{"progress-task": {Generation: "s1", At: time.Now().UTC(), Head: strings.Repeat("a", 40)}},
		Durations: []CIDuration{{Repository: "owner/repo", Kind: "ci", Seconds: 780}},
	}); err != nil {
		t.Fatal(err)
	}

	snapshot, err := service.Snapshot()

	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.CIDurations) != 0 {
		t.Fatal("snapshot read durations before the cycle refreshed them")
	}
	for _, task := range snapshot.Tasks {
		if task.Progress != nil {
			t.Fatal("snapshot read progress before the cycle refreshed it")
		}
	}
}

func TestSnapshotRefreshesProgressAndDurationsWhenTheCycleReadsTheirRecord(t *testing.T) {
	service, h := fleetService(t)
	liveGoblin(t, h, "progress-task", h.Root)
	if _, err := service.Snapshot(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		head    string
		seconds int64
	}{
		{strings.Repeat("a", 40), 780},
		{strings.Repeat("b", 40), 630},
	} {
		now := time.Now().UTC()
		watched := fleetWakes{
			Progress:  map[string]WorkProgress{"progress-task": {Generation: "s1", At: now, Head: test.head}},
			Durations: []CIDuration{{Repository: "owner/repo", Kind: "ci", Seconds: test.seconds}},
		}
		if err := writeFleetWakes(h.State, watched); err != nil {
			t.Fatal(err)
		}
		service.cycle(t.Context(), false)

		snapshot, err := service.Snapshot()

		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.CIDurations) != 1 || snapshot.CIDurations[0].Seconds != test.seconds {
			t.Fatalf("durations=%+v, want the changed record's %d seconds", snapshot.CIDurations, test.seconds)
		}
		index := slices.IndexFunc(snapshot.Tasks, func(task Task) bool { return task.ID == "progress-task" })
		if index < 0 {
			t.Fatal("snapshot lost the progress task")
		}
		task := snapshot.Tasks[index]
		if task.Progress == nil || task.Progress.Head != test.head {
			t.Fatalf("task=%+v, want the changed progress head %s", task, test.head)
		}
	}
}
