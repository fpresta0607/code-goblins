package supervisor

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
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
	over := [2]float64{6.25, 7.15}

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

// fakeForge answers gh and git the way GitHub and a checkout would for one
// repository and the one goblin worktree in it, from canned output the test
// changes between polls, and counts that repository's pull request listings.
// Any other repository has no pull requests or runs, and any other worktree
// no branch, as the fixture home's own task has. origin's HEAD names main
// unless noOriginHead says the repository has none, and then originBranches
// are the branches its origin has. runListDirs holds where push runs were
// listed. noOrigin says the repository has no origin remote, ghFailure is
// what gh prints when it cannot read the repository, and ghCalls counts the
// gh calls made in it.
type fakeForge struct {
	mu             sync.Mutex
	repo           string
	worktree       string
	pulls          string
	runs           string
	jobs           string
	branch         string
	noOrigin       bool
	noOriginHead   bool
	originBranches []string
	ghFailure      string
	ghCalls        int
	listCalls      int
	runListDirs    []string
}

func (f *fakeForge) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	command := req.Name + " " + strings.Join(req.Args, " ")
	inRepo := strings.EqualFold(filepath.Clean(req.Dir), filepath.Clean(f.repo))
	answer := func(output string) (execx.Result, error) {
		if !inRepo {
			output = "[]"
		}
		return execx.Result{Stdout: []byte(output)}, nil
	}
	if inRepo && req.Name == "gh" {
		f.ghCalls++
		if f.ghFailure != "" {
			return execx.Result{ExitCode: 1, Stderr: []byte(f.ghFailure)}, nil
		}
	}
	switch {
	case strings.HasPrefix(command, "git config --get remote.origin.url"):
		if inRepo && f.noOrigin {
			return execx.Result{ExitCode: 1}, nil
		}
		return execx.Result{Stdout: []byte("https://github.com/o/r.git\n")}, nil
	case strings.HasPrefix(command, "git branch --show-current"):
		if !strings.EqualFold(filepath.Clean(req.Dir), filepath.Clean(f.worktree)) {
			return execx.Result{Stdout: []byte("\n")}, nil
		}
		return execx.Result{Stdout: []byte(f.branch + "\n")}, nil
	case strings.HasPrefix(command, "git symbolic-ref"):
		if inRepo && f.noOriginHead {
			return execx.Result{ExitCode: 1, Stderr: []byte("fatal: ref refs/remotes/origin/HEAD is not a symbolic ref")}, nil
		}
		return execx.Result{Stdout: []byte("origin/main\n")}, nil
	case strings.HasPrefix(command, "git rev-parse --verify --quiet refs/remotes/origin/"):
		if !slices.Contains(f.originBranches, strings.TrimPrefix(req.Args[len(req.Args)-1], "refs/remotes/origin/")) {
			return execx.Result{ExitCode: 1}, nil
		}
		return execx.Result{Stdout: []byte("4e8bd9e536\n")}, nil
	case strings.HasPrefix(command, "gh pr list"):
		if inRepo {
			f.listCalls++
		}
		return answer(f.pulls)
	case strings.HasPrefix(command, "gh run list"):
		f.runListDirs = append(f.runListDirs, filepath.Clean(req.Dir))
		return answer(f.runs)
	case strings.HasPrefix(command, "gh run view"):
		return execx.Result{Stdout: []byte(f.jobs)}, nil
	}
	return execx.Result{ExitCode: 1, Stderr: []byte("unexpected " + command)}, nil
}

// forgeFor is a fake forge for goblin id working in project.
func forgeFor(project, id string) *fakeForge {
	return &fakeForge{repo: project, worktree: filepath.Join(project, ".worktrees", "gb-"+id)}
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
	forge := forgeFor(project, "cg-wakes")
	forge.branch, forge.runs, forge.pulls = "feat/wakes", "[]", pendingChecks
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
	forge := forgeFor(project, "cg-other")
	forge.branch, forge.runs, forge.pulls = "feat/something-else", "[]", passedChecks
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
	forge := forgeFor(project, "cg-wakes")
	forge.branch, forge.pulls, forge.jobs = "feat/wakes", "[]", `{"jobs":[{"name":"test","conclusion":"failure"},{"name":"vet","conclusion":"success"}]}`
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
	project := t.TempDir()
	liveGoblin(t, h, "cg-wakes", project)
	forge := forgeFor(project, "cg-wakes")
	forge.branch, forge.runs, forge.pulls = "feat/wakes", "[]", pendingChecks
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

// A reading that lands a moment short of two minutes after a poll still
// polls, so the ticker's jitter cannot stretch the cadence to three minutes,
// and one a minute after a poll never does.
func TestCIIsPolledAgainAMomentShortOfTwoMinutes(t *testing.T) {
	s, h := fleetService(t)
	project := t.TempDir()
	liveGoblin(t, h, "cg-wakes", project)
	forge := forgeFor(project, "cg-wakes")
	forge.branch, forge.runs, forge.pulls = "feat/wakes", "[]", pendingChecks
	s.Options.CI = forge
	polled := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

	for _, reading := range []struct {
		after time.Duration
		want  int
	}{{0, 1}, {60 * time.Second, 1}, {119 * time.Second, 2}} {
		if err := s.checkFleet(context.Background(), polled.Add(reading.after)); err != nil {
			t.Fatal(err)
		}
		if forge.listCalls != reading.want {
			t.Fatalf("after the reading %s past a poll, pull requests were listed %d times, want %d", reading.after, forge.listCalls, reading.want)
		}
	}
}

const redGoRun = `[{"databaseId":36740825611,"workflowName":"go","status":"completed","conclusion":"failure","headSha":"4e8bd9e536","url":"https://github.com/o/r/actions/runs/36740825611"}]`

// Each red workflow is its own result: a push that turns two workflows red
// on main raises two wakes in one poll, each naming its own workflow and run.
func TestCIFinishedWakesOncePerRedWorkflowOfOnePush(t *testing.T) {
	s, h := fleetService(t)
	project := t.TempDir()
	liveGoblin(t, h, "cg-wakes", project)
	forge := forgeFor(project, "cg-wakes")
	forge.branch, forge.pulls, forge.jobs = "feat/wakes", "[]", `{"jobs":[{"name":"test","conclusion":"failure"}]}`
	forge.runs = `[{"databaseId":36740825611,"workflowName":"go","status":"completed","conclusion":"failure","headSha":"4e8bd9e536","url":"https://github.com/o/r/actions/runs/36740825611"},
{"databaseId":36740825612,"workflowName":"install","status":"completed","conclusion":"failure","headSha":"4e8bd9e536","url":"https://github.com/o/r/actions/runs/36740825612"}]`
	s.Options.CI = forge
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

	red := pollForge(t, s, h, &now)

	if len(red) != 2 {
		t.Fatalf("two workflows red on main = %+v, want two wakes", red)
	}
	for i, want := range []string{"workflow go, job test, run 36740825611", "workflow install, job test, run 36740825612"} {
		if !strings.Contains(red[i].Detail, want) {
			t.Errorf("wake %q lacks %q", red[i].Detail, want)
		}
	}
}

// A repository whose origin has no HEAD is read by the branch its origin
// does have: one whose default branch is master wakes for master's red run.
func TestCIFinishedFindsMasterWhenOriginHasNoHead(t *testing.T) {
	s, h := fleetService(t)
	project := t.TempDir()
	liveGoblin(t, h, "cg-wakes", project)
	forge := forgeFor(project, "cg-wakes")
	forge.branch, forge.pulls, forge.runs, forge.jobs = "feat/wakes", "[]", redGoRun, `{"jobs":[{"name":"test","conclusion":"failure"}]}`
	forge.noOriginHead, forge.originBranches = true, []string{"master"}
	s.Options.CI = forge
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

	red := pollForge(t, s, h, &now)

	if len(red) != 1 || !strings.Contains(red[0].Detail, "master's push CI is red") {
		t.Fatalf("master red = %+v, want one wake for master", red)
	}
}

// A repository whose default branch cannot be found is never read as main:
// the poll says so, naming it, asks GitHub nothing about its runs and still
// polls the other repositories.
func TestCIFinishedReportsARepositoryWithNoDefaultBranch(t *testing.T) {
	s, h := fleetService(t)
	project, other := t.TempDir(), t.TempDir()
	liveGoblin(t, h, "cg-wakes", project)
	liveGoblin(t, h, "cg-other", other)
	forge := forgeFor(project, "cg-wakes")
	forge.branch, forge.pulls, forge.runs, forge.jobs = "feat/wakes", "[]", redGoRun, `{"jobs":[{"name":"test","conclusion":"failure"}]}`
	forge.noOriginHead = true
	s.Options.CI = forge
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

	err := s.checkFleet(context.Background(), now)

	if err == nil || !strings.Contains(err.Error(), "the default branch of "+project+" cannot be found") {
		t.Fatalf("poll error = %v, want one naming %s and its missing default branch", err, project)
	}
	if woke := fleetWakeRecords(t, h, "ci"); len(woke) != 0 {
		t.Fatalf("a repository with no default branch woke the CFO: %+v", woke)
	}
	if slices.Contains(forge.runListDirs, filepath.Clean(project)) {
		t.Fatalf("push runs were listed for %s, whose default branch is unknown", project)
	}
	if !slices.Contains(forge.runListDirs, filepath.Clean(other)) {
		t.Fatalf("push runs were listed in %v, want %s polled too", forge.runListDirs, other)
	}
}

// A pull request a live goblin still owns keeps its reported checks however
// long it stays open, so its one result never wakes the CFO a second time.
func TestCIFinishedNeverRepeatsForAPullRequestOpenForWeeks(t *testing.T) {
	s, h := fleetService(t)
	project := t.TempDir()
	liveGoblin(t, h, "cg-wakes", project)
	forge := forgeFor(project, "cg-wakes")
	forge.branch, forge.runs, forge.pulls = "feat/wakes", "[]", passedChecks
	s.Options.CI = forge
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

	for range 16 {
		now = now.Add(24 * time.Hour)
		if err := s.checkFleet(context.Background(), now); err != nil {
			t.Fatal(err)
		}
	}

	if woke := fleetWakeRecords(t, h, "ci"); len(woke) != 1 {
		t.Fatalf("one result on a pull request open for sixteen days woke %d times, want one: %+v", len(woke), woke)
	}
}

// What state/fleet-wakes.json remembers of a pull request, its reported
// checks and when it last woke the CFO, is dropped once no live goblin has
// owned it open for ciRecordFor, so the record does not grow for ever.
func TestFleetWakesForgetAPullRequestNoLongerWatched(t *testing.T) {
	s, h := fleetService(t)
	project := t.TempDir()
	liveGoblin(t, h, "cg-wakes", project)
	forge := forgeFor(project, "cg-wakes")
	forge.branch, forge.runs, forge.pulls = "feat/wakes", "[]", passedChecks
	s.Options.CI = forge
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	if woke := pollForge(t, s, h, &now); len(woke) != 1 {
		t.Fatalf("checks all finished = %+v, want one wake", woke)
	}
	forge.pulls = "[]"

	now = now.Add(ciRecordFor)
	if err := s.checkFleet(context.Background(), now); err != nil {
		t.Fatal(err)
	}

	remembered, err := readFleetWakes(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(remembered.Checks) != 0 || len(remembered.Woke) != 0 {
		t.Fatalf("a pull request closed for a week is still remembered: checks %v, woke %v", remembered.Checks, remembered.Woke)
	}
}

// unreadableForge is a service whose one goblin works in a repository gh
// cannot read, the forge that says why, and the repository.
func unreadableForge(t *testing.T) (*Service, home.Home, *fakeForge, string) {
	t.Helper()
	s, h := fleetService(t)
	project := t.TempDir()
	liveGoblin(t, h, "cg-wakes", project)
	forge := forgeFor(project, "cg-wakes")
	forge.branch, forge.pulls, forge.runs = "feat/wakes", "[]", "[]"
	forge.ghFailure = "To get started with GitHub CLI, please run: gh auth login"
	s.Options.CI = forge
	return s, h, forge, project
}

// A repository with no origin remote is a supported setup, not a fault: gh
// is asked nothing about it, and it raises no error and no wake.
func TestCIUnreadableIgnoresARepositoryWithNoOrigin(t *testing.T) {
	s, h := fleetService(t)
	project := t.TempDir()
	liveGoblin(t, h, "cg-wakes", project)
	forge := forgeFor(project, "cg-wakes")
	forge.branch, forge.pulls, forge.runs, forge.jobs = "feat/wakes", passedChecks, redGoRun, `{"jobs":[{"name":"test","conclusion":"failure"}]}`
	forge.noOrigin = true
	s.Options.CI = forge

	err := s.checkFleet(context.Background(), time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC))

	if err != nil {
		t.Fatalf("a repository with no origin returned %v, want no error", err)
	}
	if forge.ghCalls != 0 {
		t.Fatalf("gh was called %d times in a repository with no origin, want none", forge.ghCalls)
	}
	if woke := fleetWakeRecords(t, h, "ci"); len(woke) != 0 {
		t.Fatalf("a repository with no origin woke the CFO: %+v", woke)
	}
}

// A repository gh cannot read keeps its named error at every reading,
// polling or not, and wakes the CFO once, when the same failure was met on
// two polls in a row, however long it then lasts.
func TestCIUnreadableWakesOnceForAFailureMetOnTwoPolls(t *testing.T) {
	s, h, _, project := unreadableForge(t)
	polled := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

	for _, reading := range []struct {
		after time.Duration
		woke  int
	}{{0, 0}, {time.Minute, 0}, {2 * time.Minute, 1}, {3 * time.Minute, 1}, {4 * time.Minute, 1}, {6 * time.Minute, 1}} {
		err := s.checkFleet(context.Background(), polled.Add(reading.after))
		if err == nil || !strings.Contains(err.Error(), "list the push runs of "+project) || !strings.Contains(err.Error(), "gh auth login") {
			t.Fatalf("the reading at %s returned %v, want the named error of %s", reading.after, err, project)
		}
		woke := fleetWakeRecords(t, h, "ci")
		if len(woke) != reading.woke {
			t.Fatalf("by the reading at %s the CFO was woken %d times, want %d: %+v", reading.after, len(woke), reading.woke, woke)
		}
	}
	woke := fleetWakeRecords(t, h, "ci")[0]
	if woke.Key != "repo:"+filepath.Base(project) || !strings.HasPrefix(woke.Detail, "ci_unreadable: ") || !strings.Contains(woke.Detail, project) || !strings.Contains(woke.Detail, "next:") {
		t.Fatalf("wake = %+v, want one keyed by the repository, starting ci_unreadable and naming it and the next step", woke)
	}
}

// A different failure wakes again once it too was met on two polls, a
// failure that clears wakes nobody and leaves no record in
// state/fleet-wakes.json, and the same failure coming back wakes again.
func TestCIUnreadableWakesAgainForAChangedOrReturningFailure(t *testing.T) {
	s, h, forge, project := unreadableForge(t)
	signedOut := forge.ghFailure
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	poll := func() (int, error) {
		t.Helper()
		now = now.Add(3 * time.Minute)
		err := s.checkFleet(context.Background(), now)
		return len(fleetWakeRecords(t, h, "ci")), err
	}
	for _, step := range []struct {
		failure string
		woke    int
	}{{signedOut, 0}, {signedOut, 1}, {"HTTP 404: Not Found", 1}, {"HTTP 404: Not Found", 2}} {
		forge.ghFailure = step.failure
		if woke, _ := poll(); woke != step.woke {
			t.Fatalf("after a poll failing with %q the CFO was woken %d times, want %d", step.failure, woke, step.woke)
		}
	}

	forge.ghFailure = ""
	woke, err := poll()

	if err != nil || woke != 2 {
		t.Fatalf("the repository reading again returned %v and %d wakes, want no error and no new wake", err, woke)
	}
	data, err := fsx.ReadFile(fleetWakesPath(h.State))
	if err != nil {
		t.Fatal(err)
	}
	var remembered map[string]json.RawMessage
	if err := json.Unmarshal(data, &remembered); err != nil {
		t.Fatal(err)
	}
	if failure, kept := remembered["unreadable"]; kept {
		t.Fatalf("state/fleet-wakes.json still remembers a failure of %s: %s", project, failure)
	}
	forge.ghFailure = signedOut
	if woke, _ := poll(); woke != 2 {
		t.Fatalf("the failure met once after it cleared woke the CFO %d times, want 2", woke)
	}
	if woke, _ := poll(); woke != 3 {
		t.Fatalf("the failure back on two polls woke the CFO %d times, want 3", woke)
	}
}

// What state/fleet-wakes.json remembers of a failure outlives a restart: a
// new supervisor on the same state shows the line before it polls and does
// not wake the CFO for it again.
func TestCIUnreadableStandsAcrossARestartWithoutWakingAgain(t *testing.T) {
	s, h, _, project := unreadableForge(t)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	for range 2 {
		now = now.Add(ciPollEvery)
		_ = s.checkFleet(context.Background(), now)
	}
	if woke := fleetWakeRecords(t, h, "ci"); len(woke) != 1 {
		t.Fatalf("a failure met twice woke %d times, want one", len(woke))
	}
	restarted := &Service{Store: s.Store, Options: s.Options}

	unpolled := restarted.checkFleet(context.Background(), now.Add(time.Minute))
	polled := restarted.checkFleet(context.Background(), now.Add(ciPollEvery))

	for _, err := range []error{unpolled, polled} {
		if err == nil || !strings.Contains(err.Error(), "list the push runs of "+project) {
			t.Fatalf("after a restart a reading returned %v, want the named error of %s", err, project)
		}
	}
	if woke := fleetWakeRecords(t, h, "ci"); len(woke) != 1 {
		t.Fatalf("the restart woke the CFO again: %+v", woke)
	}
}

// The board keeps the line for as long as the failure lasts: two recovery
// cycles with no reading between them both show it.
func TestCIUnreadableShowsAtEveryRecoveryCycle(t *testing.T) {
	s, _, _, project := unreadableForge(t)
	s.work, s.subscribers = make(chan struct{}, 1), map[chan struct{}]struct{}{}
	_ = s.checkFleet(context.Background(), time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC))

	s.cycle(context.Background(), true)
	first := s.lastError
	s.cycle(context.Background(), true)

	for _, shown := range []string{first, s.lastError} {
		if !strings.Contains(shown, "list the push runs of "+project) {
			t.Fatalf("a recovery cycle showed %q, want the named error of %s", shown, project)
		}
	}
}

// cancellingForge is a forge whose supervisor is stopped mid-poll: the call
// at ends the context, and every call from then on fails with the context's
// error, as a real runner's does.
type cancellingForge struct {
	*fakeForge
	cancel context.CancelFunc
	at     func(execx.Request) bool
}

func (c *cancellingForge) Run(ctx context.Context, req execx.Request) (execx.Result, error) {
	if c.at(req) {
		c.cancel()
	}
	if err := ctx.Err(); err != nil {
		return execx.Result{}, err
	}
	return c.fakeForge.Run(ctx, req)
}

// A poll cut short by the supervisor stopping leaves what
// state/fleet-wakes.json remembers of unreadable repositories as it was: a
// repository the CFO was woken for keeps its record, a healthy one gains no
// line, and the same failure after the restart wakes nobody again.
func TestCIUnreadableRecordOutlivesAPollCutShort(t *testing.T) {
	for name, at := range map[string]func(other string) func(execx.Request) bool{
		"at the poll's first call": func(string) func(execx.Request) bool {
			return func(execx.Request) bool { return true }
		},
		"at a healthy repository's first gh call": func(other string) func(execx.Request) bool {
			return func(req execx.Request) bool {
				return req.Name == "gh" && strings.EqualFold(filepath.Clean(req.Dir), filepath.Clean(other))
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, h, forge, _ := unreadableForge(t)
			other := t.TempDir()
			liveGoblin(t, h, "cg-other", other)
			now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
			for range 2 {
				now = now.Add(ciPollEvery)
				_ = s.checkFleet(context.Background(), now)
			}
			if woke := fleetWakeRecords(t, h, "ci"); len(woke) != 1 {
				t.Fatalf("a failure met twice woke %d times, want one", len(woke))
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.Options.CI = &cancellingForge{fakeForge: forge, cancel: cancel, at: at(other)}

			now = now.Add(ciPollEvery)
			err := s.checkFleet(ctx, now)

			if ctx.Err() == nil {
				t.Fatal("the poll was not cut short")
			}
			if err != nil && strings.Contains(err.Error(), "context canceled") {
				t.Fatalf("the poll cut short returned %v, want no line for the stop itself", err)
			}
			remembered, err := readFleetWakes(h.State)
			if err != nil {
				t.Fatal(err)
			}
			if len(remembered.Unreadable) != 1 {
				t.Fatalf("after the poll cut short the record holds %+v, want the one unreadable repository", remembered.Unreadable)
			}
			for repo, record := range remembered.Unreadable {
				if !record.Woke || !strings.Contains(record.Failure, "gh auth login") {
					t.Fatalf("after the poll cut short %s is remembered as %+v, want its failure and that it woke", repo, record)
				}
			}
			restarted := &Service{Store: s.Store, Options: s.Options}
			restarted.Options.CI = forge
			for range 2 {
				now = now.Add(ciPollEvery)
				_ = restarted.checkFleet(context.Background(), now)
			}
			if woke := fleetWakeRecords(t, h, "ci"); len(woke) != 1 {
				t.Fatalf("the same failure after the restart woke the CFO again: %+v", woke)
			}
		})
	}
}
