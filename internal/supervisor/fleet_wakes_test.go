package supervisor

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// memoryReadings stands in for the memory meter: each reading is the next
// pair of available memory and available commit, in gigabytes.
type memoryReadings struct {
	readings [][2]float64
}

func (m *memoryReadings) read() (Memory, error) {
	next := m.readings[0]
	m.readings = m.readings[1:]
	return Memory{Available: uint64(next[0] * gigabyte), CommitAvailable: uint64(next[1] * gigabyte), Total: 32 * gigabyte}, nil
}

func fleetService(t *testing.T) (*Service, home.Home) {
	t.Helper()
	handler, h := orderBoard(t)
	return handler.Service, h
}

// fleetWakeRecords returns the queued wakes of kind.
func fleetWakeRecords(t *testing.T, h home.Home, kind string) []wake.Record {
	t.Helper()
	records, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	var matching []wake.Record
	for _, record := range records {
		if record.Kind == kind {
			matching = append(matching, record)
		}
	}
	return matching
}

// readMemory takes one reading per value at a minute apart and returns the
// memory wakes raised meanwhile.
func readMemory(t *testing.T, s *Service, h home.Home, meter *memoryReadings, now *time.Time, pairs ...[2]float64) int {
	t.Helper()
	before := len(fleetWakeRecords(t, h, "memory"))
	meter.readings = append(meter.readings, pairs...)
	for range pairs {
		*now = now.Add(time.Minute)
		if err := s.checkFleet(context.Background(), *now); err != nil {
			t.Fatal(err)
		}
	}
	return len(fleetWakeRecords(t, h, "memory")) - before
}

// memory_ready wakes once memory and commit both read at or above the 5 GB
// mark on two readings in a row while a task waits in the queue, then not
// again until a reading falls under the 4 GB floor and crosses back.
func TestMemoryReadyWakesOnceAndAgainOnlyAfterFallingUnderTheFloor(t *testing.T) {
	s, h := fleetService(t)
	meter := &memoryReadings{}
	s.Options.Dispatch = &Dispatch{Memory: meter.read}
	queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins)", plainBrief)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	over := [2]float64{6.2, 7.1}

	if woke := readMemory(t, s, h, meter, &now, over); woke != 0 {
		t.Fatalf("one reading over the mark woke %d times, want none", woke)
	}
	if woke := readMemory(t, s, h, meter, &now, over); woke != 1 {
		t.Fatalf("the second reading in a row over the mark woke %d times, want one", woke)
	}
	records := fleetWakeRecords(t, h, "memory")
	for _, want := range []string{"memory_ready:", "6.2 GB of memory and 7.1 GB of commit", "next: dispatch the next queued task, next-task"} {
		if !strings.Contains(records[0].Detail, want) {
			t.Errorf("memory wake %q lacks %q", records[0].Detail, want)
		}
	}
	if woke := readMemory(t, s, h, meter, &now, [2]float64{4.5, 7}, over, over, over, [2]float64{7, 4.2}, over, over); woke != 0 {
		t.Fatalf("crossing back without falling under the floor woke %d times, want none", woke)
	}
	now = now.Add(memoryWakeGap)
	if woke := readMemory(t, s, h, meter, &now, [2]float64{3.8, 7}, over, over); woke != 1 {
		t.Fatalf("falling under the floor and crossing back woke %d times, want one", woke)
	}
}

// Both readings must reach the mark: a machine with memory to spare and its
// commit nearly spent starts nothing, and nor does one short of memory.
func TestMemoryReadyNeedsMemoryAndCommitBoth(t *testing.T) {
	s, h := fleetService(t)
	meter := &memoryReadings{}
	s.Options.Dispatch = &Dispatch{Memory: meter.read}
	queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins)", plainBrief)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

	if woke := readMemory(t, s, h, meter, &now, [2]float64{16, 4.9}, [2]float64{16, 4.9}, [2]float64{4.9, 16}, [2]float64{4.9, 16}); woke != 0 {
		t.Fatalf("readings short of commit or of memory woke %d times, want none", woke)
	}
}

// Memory coming back matters only to work waiting on it: with nothing queued
// and nobody waiting it wakes nobody, and a goblin that reported waiting on
// memory is named in the wake.
func TestMemoryReadyWakesOnlyForWorkWaitingOnIt(t *testing.T) {
	s, h := fleetService(t)
	meter := &memoryReadings{}
	s.Options.Dispatch = &Dispatch{Memory: meter.read}
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	over := [2]float64{8, 8}

	if woke := readMemory(t, s, h, meter, &now, over, over, over); woke != 0 {
		t.Fatalf("memory ready with nothing waiting woke %d times, want none", woke)
	}
	meta := state.TaskMeta{ID: "cg-heavy", Project: `C:\dev\code-goblins`, Harness: "codex", Backend: "native", SpawnGen: "s1"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(h.State, "cg-heavy", "waiting on memory: the full test suite needs 5 GB"); err != nil {
		t.Fatal(err)
	}
	if woke := readMemory(t, s, h, meter, &now, over); woke != 1 {
		t.Fatalf("memory ready with a goblin waiting on it woke %d times, want one", woke)
	}
	if detail := fleetWakeRecords(t, h, "memory")[0].Detail; !strings.Contains(detail, "next: tell cg-heavy that the memory its heavy work waits on is free") {
		t.Fatalf("memory wake %q does not tell the CFO to release cg-heavy", detail)
	}
}

// fakeForge answers gh and git the way GitHub and a checkout would, from
// canned output the test changes between polls, and counts the calls.
type fakeForge struct {
	mu        sync.Mutex
	pulls     string
	runs      string
	jobs      string
	branch    string
	calls     []string
	listCalls int
}

func (f *fakeForge) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	command := req.Name + " " + strings.Join(req.Args, " ")
	f.calls = append(f.calls, command)
	switch {
	case strings.HasPrefix(command, "git branch --show-current"):
		return execx.Result{Stdout: []byte(f.branch + "\n")}, nil
	case strings.HasPrefix(command, "git symbolic-ref"):
		return execx.Result{Stdout: []byte("origin/main\n")}, nil
	case strings.HasPrefix(command, "gh pr list"):
		f.listCalls++
		return execx.Result{Stdout: []byte(f.pulls)}, nil
	case strings.HasPrefix(command, "gh run list"):
		return execx.Result{Stdout: []byte(f.runs)}, nil
	case strings.HasPrefix(command, "gh run view"):
		return execx.Result{Stdout: []byte(f.jobs)}, nil
	}
	return execx.Result{ExitCode: 1, Stderr: []byte("unexpected " + command)}, nil
}

// liveGoblin writes a live task record for id working in project.
func liveGoblin(t *testing.T, h home.Home, id, project string) {
	t.Helper()
	meta := state.TaskMeta{ID: id, Project: project, Worktree: filepath.Join(project, ".worktrees", "gb-"+id), Harness: "claude", Backend: "native", SpawnGen: "s1"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
}

const pendingChecks = `[{"number":209,"url":"https://github.com/o/r/pull/209","headRefName":"feat/wakes","headRefOid":"3d7072f8aa","statusCheckRollup":[
{"__typename":"CheckRun","name":"test","status":"IN_PROGRESS","conclusion":"","startedAt":"2026-09-30T12:00:00Z","completedAt":"0001-01-01T00:00:00Z"},
{"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"SUCCESS","completedAt":"2026-09-30T12:03:00Z"}]}]`

const passedChecks = `[{"number":209,"url":"https://github.com/o/r/pull/209","headRefName":"feat/wakes","headRefOid":"3d7072f8aa","statusCheckRollup":[
{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SUCCESS","completedAt":"2026-09-30T12:09:00Z"},
{"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"SUCCESS","completedAt":"2026-09-30T12:03:00Z"},
{"__typename":"StatusContext","context":"GitGuardian Security Checks","state":"SUCCESS"}]}]`

const failedRerun = `[{"number":209,"url":"https://github.com/o/r/pull/209","headRefName":"feat/wakes","headRefOid":"3d7072f8aa","statusCheckRollup":[
{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE","completedAt":"2026-09-30T12:30:00Z"},
{"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"SUCCESS","completedAt":"2026-09-30T12:03:00Z"},
{"__typename":"StatusContext","context":"GitGuardian Security Checks","state":"SUCCESS"}]}]`

// pollForge runs one CI poll ciPollEvery after the last and returns the ci
// wakes raised by it.
func pollForge(t *testing.T, s *Service, h home.Home, now *time.Time) []wake.Record {
	t.Helper()
	before := len(fleetWakeRecords(t, h, "ci"))
	*now = now.Add(ciPollEvery)
	if err := s.checkFleet(context.Background(), *now); err != nil {
		t.Fatal(err)
	}
	return fleetWakeRecords(t, h, "ci")[before:]
}

// ci_finished wakes once when every check on a live goblin's open pull
// request has concluded: never while one still runs, never twice for the
// same result, and again when a rerun finishes with a new one, naming the
// checks that failed.
func TestCIFinishedWakesOncePerCompletionOfAGoblinsPullRequest(t *testing.T) {
	s, h := fleetService(t)
	project := t.TempDir()
	liveGoblin(t, h, "cg-wakes", project)
	forge := &fakeForge{branch: "feat/wakes", runs: "[]", pulls: pendingChecks}
	s.Options.CI = forge
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

	if woke := pollForge(t, s, h, &now); len(woke) != 0 {
		t.Fatalf("a check still running woke the CFO: %+v", woke)
	}
	forge.pulls = passedChecks
	passed := pollForge(t, s, h, &now)
	if len(passed) != 1 || passed[0].Key != "cg-wakes" {
		t.Fatalf("checks all finished = %+v, want one wake keyed by the goblin", passed)
	}
	for _, want := range []string{"ci_finished:", "PR #209 (feat/wakes)", "all 3 passed", "next:", "merge"} {
		if !strings.Contains(passed[0].Detail, want) {
			t.Errorf("wake %q lacks %q", passed[0].Detail, want)
		}
	}
	if again := pollForge(t, s, h, &now); len(again) != 0 {
		t.Fatalf("the same result woke the CFO again: %+v", again)
	}
	forge.pulls = failedRerun
	if held := pollForge(t, s, h, &now); len(held) != 0 {
		t.Fatalf("a second completion within the gap woke the CFO at once: %+v", held)
	}
	failed := pollForge(t, s, h, &now)
	if len(failed) != 1 || !strings.Contains(failed[0].Detail, "1 of 3 failed (test)") || !strings.Contains(failed[0].Detail, "cfo send cg-wakes") {
		t.Fatalf("a rerun that failed, once the gap passed = %+v, want one wake naming the failed check", failed)
	}
}

// A pull request of no live goblin, or on another branch, wakes nobody.
func TestCIFinishedIgnoresPullRequestsNoLiveGoblinOwns(t *testing.T) {
	s, h := fleetService(t)
	project := t.TempDir()
	liveGoblin(t, h, "cg-other", project)
	forge := &fakeForge{branch: "feat/something-else", runs: "[]", pulls: passedChecks}
	s.Options.CI = forge
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

	if woke := pollForge(t, s, h, &now); len(woke) != 0 {
		t.Fatalf("another branch's pull request woke the CFO: %+v", woke)
	}
}

// main's push CI going red wakes once per red run, naming the workflow, the
// job that failed and the run; a cancelled run and a green one wake nobody.
func TestCIFinishedWakesWhenMainsPushCIGoesRed(t *testing.T) {
	s, h := fleetService(t)
	project := t.TempDir()
	liveGoblin(t, h, "cg-wakes", project)
	forge := &fakeForge{branch: "feat/wakes", pulls: "[]", jobs: `{"jobs":[{"name":"test","conclusion":"failure"},{"name":"vet","conclusion":"success"}]}`}
	forge.runs = `[{"databaseId":36740825611,"workflowName":"go","status":"completed","conclusion":"failure","headSha":"4e8bd9e536","url":"https://github.com/o/r/actions/runs/36740825611"},
{"databaseId":36716818409,"workflowName":"install","status":"completed","conclusion":"cancelled","headSha":"ef7ec0d57f","url":"https://github.com/o/r/actions/runs/36716818409"},
{"databaseId":36716818403,"workflowName":"go","status":"completed","conclusion":"failure","headSha":"ef7ec0d57f","url":"https://github.com/o/r/actions/runs/36716818403"}]`
	s.Options.CI = forge
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

	red := pollForge(t, s, h, &now)
	if len(red) != 1 || red[0].Key != "main:"+filepath.Base(project) {
		t.Fatalf("main red = %+v, want one wake keyed by the repository's main", red)
	}
	for _, want := range []string{"ci_finished:", "main's push CI is red", "workflow go", "job test", "run 36740825611", "next:"} {
		if !strings.Contains(red[0].Detail, want) {
			t.Errorf("wake %q lacks %q", red[0].Detail, want)
		}
	}
	if again := pollForge(t, s, h, &now); len(again) != 0 {
		t.Fatalf("the same red run woke the CFO again: %+v", again)
	}
	forge.runs = `[{"databaseId":36750000000,"workflowName":"go","status":"completed","conclusion":"success","headSha":"5f00000000","url":"https://github.com/o/r/actions/runs/36750000000"}]`
	if green := pollForge(t, s, h, &now); len(green) != 0 {
		t.Fatalf("a green run woke the CFO: %+v", green)
	}
	forge.runs = `[{"databaseId":36760000000,"workflowName":"go","status":"completed","conclusion":"timed_out","headSha":"6f00000000","url":"https://github.com/o/r/actions/runs/36760000000"}]`
	if again := pollForge(t, s, h, &now); len(again) != 1 || !strings.Contains(again[0].Detail, "run 36760000000") {
		t.Fatalf("main red again = %+v, want one wake for the new run", again)
	}
}

// GitHub is asked at most every two minutes, however often the fleet is
// read, because its calls come out of the hourly allowance every goblin
// shares.
func TestCIIsPolledAtMostEveryTwoMinutes(t *testing.T) {
	s, h := fleetService(t)
	liveGoblin(t, h, "cg-wakes", t.TempDir())
	forge := &fakeForge{branch: "feat/wakes", runs: "[]", pulls: pendingChecks}
	s.Options.CI = forge
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

	for range 5 {
		now = now.Add(fleetWatchEvery)
		if err := s.checkFleet(context.Background(), now); err != nil {
			t.Fatal(err)
		}
	}
	if forge.listCalls != 3 {
		t.Fatalf("five readings a minute apart listed pull requests %d times, want 3", forge.listCalls)
	}
}
