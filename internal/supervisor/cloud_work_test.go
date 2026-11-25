package supervisor

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/githubtest"
	"github.com/fpresta0607/code-goblins/internal/handoff"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/train"
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
// branch raise no pr_health wake, while one of the same repository into
// main still does, and the cloud's is seen for the Command Center at its
// head.
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
	intoMain := `{"number":211,"url":"https://github.com/o/r/pull/211","title":"Fix the footer","headRefName":"feat/footer","baseRefName":"main","author":{"login":"teammate"},"headRefOid":"f00df00df00df00df00df00df00df00df00df00d","mergeable":"CONFLICTING","statusCheckRollup":[]}`
	forge.pulls = strings.TrimSuffix(rollupWith("ed99ac3f00", "CONFLICTING", scanPassedRun), "]") + "," + cloud + "," + intoMain + "]"
	s.Options.CI = forge
	s.Options.GitHub = githubtest.Client(forge)
	now := time.Date(2026, 10, 7, 3, 48, 0, 0, time.UTC)

	// Act
	for poll := 0; poll < 3; poll++ {
		pollForge(t, s, h, &now)
	}

	// Assert
	if health := fleetWakeRecords(t, h, "pr"); len(health) != 1 || !strings.Contains(health[0].Detail, "https://github.com/o/r/pull/211") {
		t.Fatalf("pr_health wakes = %+v, want one, for the pull request into main alone, while the task is in the cloud", health)
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
// its turn, as a Resume does, rather than being refused. His press is
// written only when its turn comes, so one a Pause or a Leave it withdrew
// before then runs no return and leaves no press for one to take.
func TestBringBackWaitsItsTurnWhileAnotherGoblinLaunches(t *testing.T) {
	for _, test := range []struct {
		name       string
		withdrawBy string
		wantReturn bool
	}{
		{name: "its turn comes", wantReturn: true},
		{name: "a pause withdraws it first", withdrawBy: "pause"},
		{name: "leave it withdraws it first", withdrawBy: "leave"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 5*gigabyte, spawner)
			meta := state.TaskMeta{ID: "task", Title: "Do work", SpawnGen: "generation-1", Project: h.Root, Worktree: h.Root, Backend: "native"}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			handOff(t, h, meta, "feat/parser", "https://github.com/o/r/pull/209", handoff.CloudSighting{PullRequest: cloudPullRequest, Title: "Name the bad header", Author: "teammate", Head: cloudHead, IsTeammates: true})
			s := handler.Service
			s.starts.Lock()
			s.starting = "other-task"
			s.starts.Unlock()

			// Act
			response := taskControlRequest(handler, "/api/tasks/cloud", map[string]string{"task": "task", "generation": meta.SpawnGen, "action": "bring_back", "pull_request": cloudPullRequest, "head": cloudHead})
			s.starts.Lock()
			isWaiting := s.isAsked("task")
			s.starts.Unlock()
			waiting, err := handoff.ReadRecord(h.State, meta.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch test.withdrawBy {
			case "pause":
				taskControlRequest(handler, "/api/tasks/lifecycle", map[string]string{"task": "task", "generation": meta.SpawnGen, "operation": "pause-1", "action": "pause"})
				waitForChange(t, handler, meta.ID)
			case "leave":
				if left := taskControlRequest(handler, "/api/tasks/cloud", map[string]string{"task": "task", "generation": meta.SpawnGen, "action": "leave", "pull_request": cloudPullRequest, "head": cloudHead}); left.Code != 202 {
					t.Fatalf("leave it = %d %s, want 202", left.Code, left.Body)
				}
			}
			s.starts.Lock()
			s.starting = ""
			s.starts.Unlock()
			s.runAsked()
			waitForChange(t, handler, meta.ID)

			// Assert
			record, err := handoff.ReadRecord(h.State, meta.ID)
			if response.Code != 202 || !isWaiting || waiting.Press != nil || err != nil || (record.Press != nil) != test.wantReturn || test.wantReturn && record.Press.Head != cloudHead {
				t.Fatalf("response %d %s, waiting %v with press %+v, record %+v (%v); want 202, waiting with no press, and a press %v", response.Code, response.Body, isWaiting, waiting.Press, record, err, test.wantReturn)
			}
			isReturned := slices.ContainsFunc(spawner.recorded(), func(call []string) bool {
				return slices.Equal(call, []string{"handoff", "return", "task", "--pr", cloudPullRequest})
			})
			if isReturned != test.wantReturn {
				t.Fatalf("calls %v, want return on the pull request %v", spawner.recorded(), test.wantReturn)
			}
			// A Bring back he took back is no failure for the CEO to hear of.
			if told := fleetWakeRecords(t, h, "notify"); len(told) != 0 {
				t.Fatalf("the CEO was told %+v, want nothing", told)
			}
		})
	}
}

// A Bring back whose turn comes while a poll or a goblins handoff command
// holds the handoff record waits for a later turn rather than failing, and
// runs once the record is free.
func TestBringBackWaitsWhileTheHandoffRecordIsInUse(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 5*gigabyte, spawner)
	meta := state.TaskMeta{ID: "task", Title: "Do work", SpawnGen: "generation-1", Project: h.Root, Worktree: h.Root, Backend: "native"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	handOff(t, h, meta, "feat/parser", "https://github.com/o/r/pull/209", handoff.CloudSighting{PullRequest: cloudPullRequest, Title: "Name the bad header", Author: "teammate", Head: cloudHead, IsTeammates: true})
	s := handler.Service
	s.starts.Lock()
	s.starting = "other-task"
	s.starts.Unlock()
	if response := taskControlRequest(handler, "/api/tasks/cloud", map[string]string{"task": "task", "generation": meta.SpawnGen, "action": "bring_back", "pull_request": cloudPullRequest, "head": cloudHead}); response.Code != 202 {
		t.Fatalf("bring back = %d %s, want 202", response.Code, response.Body)
	}
	if _, err := lock.AcquireExclusiveNamed(h.State, ".handoff-task.lock"); err != nil {
		t.Fatal(err)
	}
	s.starts.Lock()
	s.starting = ""
	s.starts.Unlock()

	// Act
	s.runAsked()
	s.starts.Lock()
	isWaiting := s.isAsked("task")
	s.starts.Unlock()
	heldCalls, heldNotifies := spawner.recorded(), fleetWakeRecords(t, h, "notify")
	if err := lock.ReleaseExclusiveNamed(h.State, ".handoff-task.lock"); err != nil {
		t.Fatal(err)
	}
	s.runAsked()
	waitForChange(t, handler, meta.ID)

	// Assert
	if !isWaiting || len(heldCalls) != 0 || len(heldNotifies) != 0 {
		t.Fatalf("while held: waiting %v, calls %v, notifies %+v; want it waiting with no call and nothing told", isWaiting, heldCalls, heldNotifies)
	}
	record, err := handoff.ReadRecord(h.State, meta.ID)
	if err != nil || record.Press == nil || record.Press.Head != cloudHead || !slices.EqualFunc(spawner.recorded(), [][]string{{"handoff", "return", "task", "--pr", cloudPullRequest}}, slices.Equal) {
		t.Fatalf("once free: record %+v (%v), calls %v; want the press and return on the pull request", record, err, spawner.recorded())
	}
}

// A sighting is labelled a teammate's against the account gh works as, so
// while that account cannot be read none is sighted, its failure is told as
// the train's is once it failed failingPasses polls in a row, and the next
// poll that reads it tells the account's own pull request as its own.
func TestCloudWorkIsNotSightedWhileTheAccountIsUnread(t *testing.T) {
	// Arrange
	s, h := fleetService(t)
	meta := state.TaskMeta{ID: "task", Title: "Do work", SpawnGen: "generation-1", Project: h.Root, Worktree: h.Root, Backend: "native"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	handOff(t, h, meta, "feat/parser", "https://github.com/o/r/pull/209")
	goblins := []ciGoblin{{id: meta.ID, meta: meta}}
	handedOff := handedOffGoblins(h.State, goblins)
	listed := []train.PullRequest{{Number: 210, URL: cloudPullRequest, Title: "Name the bad header", HeadRefName: "claude/cloud-work", BaseRefName: "feat/parser", HeadRefOid: cloudHead}}
	listed[0].Author.Login = "o"
	isUnread := true
	github := githubtest.Client(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if isUnread {
			githubtest.Refuse(w, http.StatusBadGateway, "Bad gateway")
			return
		}
		githubtest.Write(w, http.StatusOK, map[string]string{"login": "o"})
	}))
	now := time.Date(2026, 11, 25, 20, 0, 0, 0, time.UTC)

	// Act
	var w fleetWakes
	var unreadErrs []error
	for range failingPasses {
		unreadErrs = append(unreadErrs, s.sightCloudWork(t.Context(), github, &w, h.Root, goblins, handedOff, listed, &fleetOwners{}, now))
	}
	unread, unreadReadErr := handoff.ReadRecord(h.State, meta.ID)
	isUnread = false
	err := s.sightCloudWork(t.Context(), github, &w, h.Root, goblins, handedOff, listed, &fleetOwners{}, now)

	// Assert
	record, readErr := handoff.ReadRecord(h.State, meta.ID)
	isToldLast := unreadErrs[failingPasses-1] != nil && slices.IndexFunc(unreadErrs, func(err error) bool { return err != nil }) == failingPasses-1
	if !isToldLast || unreadReadErr != nil || len(unread.Cloud) != 0 {
		t.Fatalf("with the account unread: %v, cloud %+v (%v); want an error at poll %d alone and nothing sighted", unreadErrs, unread.Cloud, unreadReadErr, failingPasses)
	}
	if err != nil || readErr != nil || len(record.Cloud) != 1 || record.Cloud[0].IsTeammates {
		t.Fatalf("with the account read: %v, cloud %+v (%v); want the account's own sighted", err, record.Cloud, readErr)
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

// The cloud watch's read of the account counts its failed polls of a
// repository, as the train's does, and the count ends with the repository's
// watch and with its last handed-off task, so a failure while a later
// handoff is watched waits out its own polls before it is told.
func TestTheCloudWatchsFailedAccountReadsEndWithItsWatch(t *testing.T) {
	for name, end := range map[string]func(s *Service, w *fleetWakes, now time.Time){
		"the repository leaves the watch": func(_ *Service, w *fleetWakes, now time.Time) { w.watch(nil, now.Add(repoWatchFor)) },
		"no task there is handed off": func(s *Service, w *fleetWakes, now time.Time) {
			if err := s.sightCloudWork(t.Context(), nil, w, "o/r", nil, map[string]handoff.Record{}, nil, nil, now); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			s, _ := fleetService(t)
			now := time.Date(2026, 11, 25, 19, 0, 0, 0, time.UTC)
			w := &fleetWakes{Repos: map[string]time.Time{"o/r": now}, Failing: map[string]int{"cloud viewer:o/r": failingPasses - 1}}

			// Act
			end(s, w, now)

			// Assert
			if count, isCounted := w.Failing["cloud viewer:o/r"]; isCounted {
				t.Fatalf("the failed account reads still count %d, want none", count)
			}
		})
	}
}
