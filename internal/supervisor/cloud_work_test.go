package supervisor

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/githubtest"
	"github.com/fpresta0607/code-goblins/internal/handoff"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const (
	cloudPullRequest = "https://github.com/o/r/pull/210"
	cloudHead        = "c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00"
)

// handOff marks task meta handed off in its session on branch, its packet
// in the pull request packetIn, with the cloud's pull requests seen.
func handOff(t *testing.T, h home.Home, meta state.TaskMeta, branch, packetIn string, seen ...handoff.CloudSighting) {
	t.Helper()
	at := time.Date(2026, 11, 25, 19, 0, 0, 0, time.UTC)
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: "handoff-1", Action: "pause", Phase: "paused", Pause: &state.PauseCondition{Reason: "handoff", At: at}}); err != nil {
		t.Fatal(err)
	}
	if err := handoff.WriteRecord(h.State, handoff.Record{Task: meta.ID, Started: at, Steps: []string{handoff.StepPause, handoff.StepPush, handoff.StepPullRequest, handoff.StepPacket}, Branch: branch, PullRequest: packetIn, Head: "ed99ac3f00", HandedOff: &at, Cloud: seen}); err != nil {
		t.Fatal(err)
	}
}

// While a task is in the cloud, its pull request and the cloud's into its
// branch raise no pr_health wake, and the cloud's is seen for the Command
// Center at its head.
func TestAHandedOffTasksPullRequestsRaiseNoHealthWakeAndTheCloudsIsSeen(t *testing.T) {
	// Arrange
	s, h := fleetService(t)
	project := t.TempDir()
	runsWorkflows(t, project)
	liveGoblin(t, h, "cg-wakes", project)
	meta, err := state.ReadTaskMeta(h.State, "cg-wakes")
	if err != nil {
		t.Fatal(err)
	}
	handOff(t, h, meta, "feat/wakes", "https://github.com/o/r/pull/209")
	forge := forgeFor(project, "cg-wakes")
	forge.branch, forge.runs = "feat/wakes", "[]"
	cloud := `{"number":210,"url":"` + cloudPullRequest + `","title":"Name the bad header","headRefName":"claude/cloud-work","baseRefName":"feat/wakes","author":{"login":"o"},"headRefOid":"` + cloudHead + `","mergeable":"CONFLICTING","statusCheckRollup":[]}`
	forge.pulls = strings.TrimSuffix(rollupWith("ed99ac3f00", "CONFLICTING", scanPassedRun), "]") + "," + cloud + "]"
	s.Options.CI = forge
	s.Options.GitHub = githubtest.Client(forge)
	now := time.Date(2026, 10, 7, 3, 48, 0, 0, time.UTC)

	// Act
	for poll := 0; poll < 3; poll++ {
		pollForge(t, s, h, &now)
	}

	// Assert
	if health := fleetWakeRecords(t, h, "pr"); len(health) != 0 {
		t.Fatalf("pr_health wakes = %+v, want none while the task is in the cloud", health)
	}
	record, err := handoff.ReadRecord(h.State, meta.ID)
	if err != nil || len(record.Cloud) != 1 || record.Cloud[0].PullRequest != cloudPullRequest || record.Cloud[0].Head != cloudHead || record.Cloud[0].IsTeammates {
		t.Fatalf("record = %+v (%v), want the cloud's pull request seen at its head", record.Cloud, err)
	}
}

func TestCloudWorkLeavesOrBringsBackOnlyTheHeadTheItemShowed(t *testing.T) {
	seen := handoff.CloudSighting{PullRequest: cloudPullRequest, Title: "Name the bad header", Author: "teammate", Head: cloudHead, IsTeammates: true}
	for _, test := range []struct {
		name      string
		input     map[string]string
		wantCode  int
		wantCalls [][]string
		want      func(handoff.Record) bool
	}{
		{name: "bring back records the press and runs return on that pull request", input: map[string]string{"action": "bring_back", "head": cloudHead}, wantCode: 202,
			wantCalls: [][]string{{"handoff", "return", "task", "--pr", cloudPullRequest}},
			want: func(record handoff.Record) bool {
				return record.Press != nil && record.Press.PullRequest == cloudPullRequest && record.Press.Head == cloudHead
			}},
		{name: "leave it closes the item", input: map[string]string{"action": "leave", "head": cloudHead}, wantCode: 202,
			want: func(record handoff.Record) bool { return record.Press == nil && record.Cloud[0].IsLeft }},
		{name: "a head that moved since the item was shown", input: map[string]string{"action": "bring_back", "head": strings.Repeat("a", 40)}, wantCode: 409,
			want: func(record handoff.Record) bool { return record.Press == nil && !record.Cloud[0].IsLeft }},
		{name: "a session that changed", input: map[string]string{"action": "bring_back", "head": cloudHead, "generation": "stale"}, wantCode: 409,
			want: func(record handoff.Record) bool { return record.Press == nil }},
		{name: "an action it does not take", input: map[string]string{"action": "merge", "head": cloudHead}, wantCode: 400,
			want: func(record handoff.Record) bool { return record.Press == nil }},
		{name: "a head that is no commit", input: map[string]string{"action": "leave", "head": "HEAD"}, wantCode: 400,
			want: func(record handoff.Record) bool { return !record.Cloud[0].IsLeft }},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 5*gigabyte, spawner)
			meta := state.TaskMeta{ID: "task", Title: "Do work", SpawnGen: "generation-1", Project: h.Root, Worktree: h.Root, Backend: "native"}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			handOff(t, h, meta, "feat/parser", "https://github.com/o/r/pull/209", seen)
			input := map[string]string{"task": "task", "generation": meta.SpawnGen, "pull_request": cloudPullRequest}
			for key, value := range test.input {
				input[key] = value
			}

			// Act
			response := taskControlRequest(handler, "/api/tasks/cloud", input)
			waitForChange(t, handler, meta.ID)

			// Assert
			record, err := handoff.ReadRecord(h.State, meta.ID)
			if response.Code != test.wantCode || err != nil || !test.want(record) || !slices.EqualFunc(spawner.recorded(), test.wantCalls, slices.Equal) {
				t.Fatalf("response %d %s, record %+v (%v), calls %v; want %d and %v", response.Code, response.Body, record, err, spawner.recorded(), test.wantCode, test.wantCalls)
			}
		})
	}
}

// Bring back resumes the goblin, so while another goblin launches it waits
// its turn, as a Resume does, with his press already recorded, rather than
// being refused, and runs once the launch ends.
func TestBringBackWaitsItsTurnWhileAnotherGoblinLaunches(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 5*gigabyte, spawner)
	meta := state.TaskMeta{ID: "task", Title: "Do work", SpawnGen: "generation-1", Project: h.Root, Worktree: h.Root, Backend: "native"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	handOff(t, h, meta, "feat/parser", "https://github.com/o/r/pull/209", handoff.CloudSighting{PullRequest: cloudPullRequest, Title: "Name the bad header", Author: "o", Head: cloudHead})
	s := handler.Service
	s.starts.Lock()
	s.starting = "other-task"
	s.starts.Unlock()

	// Act
	response := taskControlRequest(handler, "/api/tasks/cloud", map[string]string{"task": "task", "generation": meta.SpawnGen, "action": "bring_back", "pull_request": cloudPullRequest, "head": cloudHead})
	s.starts.Lock()
	isWaiting := s.isAsked("task")
	s.starting = ""
	s.starts.Unlock()
	waitingCalls := spawner.recorded()
	s.runAsked()
	waitForChange(t, handler, meta.ID)

	// Assert
	record, err := handoff.ReadRecord(h.State, meta.ID)
	if response.Code != 202 || !isWaiting || len(waitingCalls) != 0 || err != nil || record.Press == nil || record.Press.Head != cloudHead {
		t.Fatalf("response %d %s, waiting %v with calls %v, record %+v (%v); want 202, waiting with no call and the press recorded", response.Code, response.Body, isWaiting, waitingCalls, record, err)
	}
	if calls := spawner.recorded(); !slices.EqualFunc(calls, [][]string{{"handoff", "return", "task", "--pr", cloudPullRequest}}, slices.Equal) {
		t.Fatalf("calls %v, want return on the pull request once the launch ended", calls)
	}
}

// The board's snapshot carries a handed-off task's pull request and the
// cloud's into its branch, which the Command Center lists as Cloud work
// ready.
func TestTheSnapshotCarriesAHandedOffTasksCloudWork(t *testing.T) {
	// Arrange
	handler, h := startBoard(t, 5*gigabyte, &spawnRecorder{})
	meta := state.TaskMeta{ID: "task", Title: "Do work", SpawnGen: "generation-1", Project: h.Root, Worktree: h.Root, Backend: "native"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	seen := handoff.CloudSighting{PullRequest: cloudPullRequest, Title: "Name the bad header", Author: "o", Head: cloudHead}
	handOff(t, h, meta, "feat/parser", "https://github.com/o/r/pull/209", seen)

	// Act
	snapshot, err := handler.Service.Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(snapshot.Tasks, func(task Task) bool { return task.ID == meta.ID })
	if index < 0 || snapshot.Tasks[index].CloudHandoff == nil || snapshot.Tasks[index].CloudHandoff.PullRequest != "https://github.com/o/r/pull/209" || !slices.Equal(snapshot.Tasks[index].CloudHandoff.Cloud, []handoff.CloudSighting{seen}) {
		t.Fatalf("tasks = %+v, want the handed-off task carrying its cloud work", snapshot.Tasks)
	}
}

func waitForChange(t *testing.T, handler *HTTP, task string) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		handler.Service.starts.Lock()
		isChanging := handler.Service.changing[task] != ""
		handler.Service.starts.Unlock()
		if !isChanging {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s is still changing", task)
}
