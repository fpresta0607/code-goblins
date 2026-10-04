package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

const gigabyte = 1 << 30

// spawnRecorder stands in for cfo spawn: it records each call and answers
// with the outcome the test gives it, after release when release is set.
type spawnRecorder struct {
	mu      sync.Mutex
	calls   [][]string
	output  string
	err     error
	release chan struct{}
}

func (r *spawnRecorder) spawn(_ context.Context, args []string) (string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, args)
	r.mu.Unlock()
	if r.release != nil {
		<-r.release
	}
	return r.output, r.err
}

func (r *spawnRecorder) recorded() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]string(nil), r.calls...)
}

// startBoard is a board that can start goblins on a machine with available
// physical memory and ample commit.
func startBoard(t *testing.T, available uint64, spawner *spawnRecorder) (*HTTP, home.Home) {
	t.Helper()
	return startBoardWith(t, Memory{Available: available, Total: 32 * gigabyte, CommitAvailable: 40 * gigabyte, CommitLimit: 48 * gigabyte}, spawner)
}

func startBoardWith(t *testing.T, memory Memory, spawner *spawnRecorder) (*HTTP, home.Home) {
	t.Helper()
	handler, h := orderBoard(t)
	handler.Service.Options.Dispatch = &Dispatch{
		Memory:        func() (Memory, error) { return memory, nil },
		CommitHolders: func() ([]CommitHolder, error) { return nil, nil },
		Spawn:         spawner.spawn,
	}
	return handler, h
}

func postStart(handler *HTTP, body, host, origin, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest("POST", "http://"+host+"/api/tasks/start", strings.NewReader(body))
	request.Host = host
	request.Header.Set("Content-Type", "application/json")
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if token != "" {
		request.Header.Set("X-CFO-Token", token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func queueBriefedTask(t *testing.T, h home.Home, row, brief string) {
	t.Helper()
	writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n"+row+"\n")
	writeFile(t, filepath.Join(h.Data, "next-task", "brief.md"), brief)
}

const plainBrief = "# Brief next-task\n\n## Project\n\nC:\\dev\\code-goblins\n\n## Task\n\nShip it.\n"

func TestSnapshotDoesNotWaitForAStartReadingMachineMemory(t *testing.T) {
	// Arrange
	handler, h := startBoard(t, 8*gigabyte, &spawnRecorder{})
	queueBriefedTask(t, h, "- **next-task** - Next task (repo: code-goblins)", plainBrief)
	reading, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	handler.Service.Options.Dispatch.Memory = func() (Memory, error) {
		if calls.Add(1) == 1 {
			close(reading)
			<-release
			return Memory{}, errors.New("memory reading failed")
		}
		return Memory{Available: 8 * gigabyte, CommitAvailable: 8 * gigabyte}, nil
	}
	startFinished := make(chan error, 1)
	go func() { startFinished <- handler.Service.startTask("next-task") }()
	<-reading
	end := sync.OnceFunc(func() { close(release) })
	defer end()

	// Act
	finished := make(chan error, 1)
	go func() { _, err := handler.Service.Snapshot(); finished <- err }()

	// Assert
	select {
	case err := <-finished:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(time.Second):
		t.Error("snapshot waited for a Start's machine read while holding s.starts")
		end()
		if err := <-finished; err != nil {
			t.Error(err)
		}
	}
	end()
	if err := <-startFinished; err == nil {
		t.Error("a failed memory reading started the task")
	}
	if len(handler.Service.changing) != 0 || handler.Service.starting != "" {
		t.Error("a refused Start kept its reservation")
	}
}

func TestSharedSnapshotStopsShowingAStartRefusedAfterItsMemoryRead(t *testing.T) {
	// Arrange
	handler, h := startBoard(t, 8*gigabyte, &spawnRecorder{})
	queueBriefedTask(t, h, "- **next-task** - Next task (repo: code-goblins)", plainBrief)
	service := handler.Service
	reading, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	service.Options.Dispatch.Memory = func() (Memory, error) {
		if calls.Add(1) == 1 {
			close(reading)
			<-release
			return Memory{}, errors.New("memory reading failed")
		}
		return Memory{Available: 8 * gigabyte, CommitAvailable: 8 * gigabyte}, nil
	}
	startFinished := make(chan error, 1)
	go func() { startFinished <- service.startTask("next-task") }()
	<-reading
	during, err := service.SnapshotSince(service.Revision())
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if index := slices.IndexFunc(during.Tasks, func(task Task) bool { return task.ID == "next-task" }); index < 0 || !during.Tasks[index].Starting {
		t.Fatalf("the snapshot built while Start read memory does not show it starting: %+v", during.Tasks)
	}
	if err := <-startFinished; err == nil {
		t.Fatal("a failed memory reading started the task")
	}

	// Act
	after, err := service.SnapshotSince(service.Revision())

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if index := slices.IndexFunc(after.Tasks, func(task Task) bool { return task.ID == "next-task" }); index < 0 || after.Tasks[index].Starting {
		t.Fatalf("the shared snapshot still shows a refused Start starting: %+v", after.Tasks)
	}
}

// waitStarted waits until the board no longer shows the start in progress. A
// snapshot read while the start writes the wake queue can meet a Windows
// sharing violation, and the board simply reads again, so this does too.
func waitStarted(t *testing.T, handler *HTTP, id string) Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := handler.Service.Snapshot()
		if err != nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		for _, task := range snapshot.Tasks {
			if task.ID == id && !task.Starting {
				return task
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s still shows Starting", id)
	return Task{}
}

// startRefusal is a refused Start's answer: its reason, and whether its cause
// passes by itself.
type startRefusal struct {
	Error   string `json:"error"`
	Passing bool   `json:"passing"`
}

func decodeRefusal(t *testing.T, response *httptest.ResponseRecorder) startRefusal {
	t.Helper()
	var refusal startRefusal
	if err := json.Unmarshal(response.Body.Bytes(), &refusal); err != nil {
		t.Fatalf("answer %s: %v", response.Body, err)
	}
	return refusal
}

func TestStartRefusesARequestWithoutTheBoardsHostOriginAndToken(t *testing.T) {
	tests := []struct{ name, host, origin, token string }{
		{name: "no token", host: "board.local", origin: "http://board.local"},
		{name: "a stale token", host: "board.local", origin: "http://board.local", token: "stale"},
		{name: "no origin", host: "board.local", token: orderToken},
		{name: "another origin", host: "board.local", origin: "https://evil.invalid", token: orderToken},
		{name: "another host", host: "evil.invalid", origin: "http://evil.invalid", token: orderToken},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 16*gigabyte, spawner)
			queueBriefedTask(t, h, "- **next-task** - Ship it", plainBrief)

			// Act
			response := postStart(handler, `{"task":"next-task"}`, test.host, test.origin, test.token)

			// Assert
			if response.Code != 403 {
				t.Fatalf("start = %d %s, want 403", response.Code, response.Body)
			}
			if calls := spawner.recorded(); len(calls) != 0 {
				t.Fatalf("a refused request ran cfo spawn %v", calls)
			}
		})
	}
}

func TestStartRefusesWithAClearReason(t *testing.T) {
	tests := []struct {
		name      string
		available uint64
		row       string
		brief     string
		live      bool
		body      string
		want      string
		passing   bool
	}{
		{name: "memory just under the 5 GB start mark reads under it", available: 5*gigabyte - gigabyte/40, row: "- **next-task** - Ship it", brief: plainBrief, body: `{"task":"next-task"}`, want: "Only 4.9 GB of memory is free", passing: true},
		{name: "memory just under the 4 GB floor reads under it", available: 4*gigabyte - 1, row: "- **next-task** - Ship it", brief: plainBrief, body: `{"task":"next-task"}`, want: "Only 3.9 GB of memory is free", passing: true},
		{name: "memory under the 4 GB floor", available: 3*gigabyte + gigabyte/2, row: "- **next-task** - Ship it", brief: plainBrief, body: `{"task":"next-task"}`, want: "3.5 GB of memory is free; Start needs 5 GB to keep the 4 GB floor", passing: true},
		{name: "no brief or project", available: 16 * gigabyte, row: "- **next-task** - Ship it", body: `{"task":"next-task"}`, want: "names no project"},
		{name: "a task that already runs", available: 16 * gigabyte, row: "- **next-task** - Ship it", brief: plainBrief, live: true, body: `{"task":"next-task"}`, want: "already runs"},
		{name: "a task nothing queued", available: 16 * gigabyte, row: "- **other** - Other", body: `{"task":"next-task"}`, want: "is not queued"},
		{name: "a brief that names no project", available: 16 * gigabyte, row: "- **next-task** - Ship it", brief: "# Brief\n\n## Task\n\nShip it.\n", body: `{"task":"next-task"}`, want: "names no project"},
		{name: "a harness cfo spawn does not run", available: 16 * gigabyte, row: "- **next-task** - Ship it (harness: shell)", brief: plainBrief, body: `{"task":"next-task"}`, want: "harness shell"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, test.available, spawner)
			writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n"+test.row+"\n")
			if test.brief != "" {
				writeFile(t, filepath.Join(h.Data, "next-task", "brief.md"), test.brief)
			}
			if test.live {
				if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "next-task", Project: h.Root, Worktree: h.Root, Harness: "claude", Mode: "no-mistakes", Kind: "ship", Backend: "native", SpawnGen: "s1"}); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			response := postStart(handler, test.body, "board.local", "http://board.local", orderToken)

			// Assert
			refusal := decodeRefusal(t, response)
			if response.Code != 409 || !strings.Contains(refusal.Error, test.want) || refusal.Passing != test.passing {
				t.Fatalf("start = %d %s, want 409 saying %q with passing %v", response.Code, response.Body, test.want, test.passing)
			}
			if calls := spawner.recorded(); len(calls) != 0 {
				t.Fatalf("a refused start ran cfo spawn %v", calls)
			}
		})
	}
}

func TestStartNeedsFiveGigabytesOfBothMemoryAndCommitAndNamesWhatIsShort(t *testing.T) {
	tests := []struct {
		name              string
		available, commit uint64
		want, notWant     string
	}{
		{name: "memory short", available: 3*gigabyte + gigabyte/2, commit: 40 * gigabyte, want: "Only 3.5 GB of memory is free; Start needs 5 GB to keep the 4 GB floor", notWant: "commit"},
		{name: "commit short", available: 16 * gigabyte, commit: 2*gigabyte + gigabyte/2, want: "Only 2.5 GB of commit (RAM plus page file) is free; Start needs 5 GB to keep the 4 GB floor", notWant: "of memory"},
		{name: "commit just under the 5 GB start mark reads under it", available: 16 * gigabyte, commit: 5*gigabyte - gigabyte/40, want: "Only 4.9 GB of commit (RAM plus page file) is free", notWant: "of memory"},
		{name: "both short", available: 3*gigabyte + gigabyte/2, commit: 2*gigabyte + gigabyte/2, want: "Only 3.5 GB of memory and 2.5 GB of commit (RAM plus page file) are free; Start needs 5 GB to keep the 4 GB floor"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoardWith(t, Memory{Available: test.available, Total: 32 * gigabyte, CommitAvailable: test.commit, CommitLimit: 48 * gigabyte}, spawner)
			queueBriefedTask(t, h, "- **next-task** - Ship it", plainBrief)

			// Act
			response := postStart(handler, `{"task":"next-task"}`, "board.local", "http://board.local", orderToken)

			// Assert
			refusal := decodeRefusal(t, response)
			if response.Code != 409 || !strings.Contains(refusal.Error, test.want) || !refusal.Passing {
				t.Fatalf("start = %d %s, want a passing 409 saying %q", response.Code, response.Body, test.want)
			}
			if test.notWant != "" && strings.Contains(refusal.Error, test.notWant) {
				t.Fatalf("refusal %q names %q, which is not short", refusal.Error, test.notWant)
			}
			if calls := spawner.recorded(); len(calls) != 0 {
				t.Fatalf("a refused start ran cfo spawn %v", calls)
			}
		})
	}
}

func TestStartRunsWithExactlyFiveGigabytesOfBothMemoryAndCommit(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{output: "spawned next-task harness=claude kind=ship"}
	handler, h := startBoardWith(t, Memory{Available: 5 * gigabyte, Total: 32 * gigabyte, CommitAvailable: 5 * gigabyte, CommitLimit: 48 * gigabyte}, spawner)
	queueBriefedTask(t, h, "- **next-task** - Ship it", plainBrief)

	// Act
	response := postStart(handler, `{"task":"next-task"}`, "board.local", "http://board.local", orderToken)

	// Assert
	if response.Code != 202 {
		t.Fatalf("start = %d %s, want 202", response.Code, response.Body)
	}
	waitStarted(t, handler, "next-task")
	if calls := spawner.recorded(); len(calls) != 1 {
		t.Fatalf("cfo spawn ran %d times, want once", len(calls))
	}
}

func TestStartRefusesOnABoardThatCannotStartGoblins(t *testing.T) {
	// Arrange
	handler, h := orderBoard(t)
	queueBriefedTask(t, h, "- **next-task** - Ship it", plainBrief)

	// Act
	response := postStart(handler, `{"task":"next-task"}`, "board.local", "http://board.local", orderToken)

	// Assert
	if response.Code != 409 || !strings.Contains(response.Body.String(), "cannot start goblins") {
		t.Fatalf("start = %d %s, want 409", response.Code, response.Body)
	}
}

func TestStartDispatchesThroughCfoSpawnAndTellsTheCFO(t *testing.T) {
	tests := []struct {
		name  string
		row   string
		brief string
		want  []string
	}{
		{
			name:  "the fleet's defaults",
			row:   "- **next-task** - Ship it (repo: code-goblins)",
			brief: plainBrief,
			want:  []string{"spawn", "next-task", "--project", `C:\dev\code-goblins`, "--harness", "claude", "--model", "claude-opus-5-5", "--effort", "xhigh"},
		},
		{
			name:  "what the row and the brief name, the row first",
			row:   "- **next-task** - Ship it (repo: code-goblins, harness: codex, model: gpt-6-astra)",
			brief: plainBrief + "\n## Delivery\n\nkind: ship\nmode: direct-PR\neffort: high\nmodel: ignored-behind-the-row\n",
			want:  []string{"spawn", "next-task", "--project", `C:\dev\code-goblins`, "--harness", "codex", "--model", "gpt-6-astra", "--effort", "high", "--mode", "direct-PR"},
		},
		{
			name:  "the row's repo when the brief names no project",
			row:   "- **next-task** - Ship it (repo: PrecisionDocs-AI)",
			brief: "# Brief\n\n## Task\n\nShip it.\n",
			want:  []string{"spawn", "next-task", "--project", "PrecisionDocs-AI", "--harness", "claude", "--model", "claude-opus-5-5", "--effort", "xhigh"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{output: "spawned next-task harness=claude kind=ship"}
			handler, h := startBoard(t, 5*gigabyte, spawner)
			queueBriefedTask(t, h, test.row, test.brief)
			if err := fleet.WriteAttention(h, []string{"task-1"}); err != nil {
				t.Fatal(err)
			}

			// Act
			response := postStart(handler, `{"task":"next-task"}`, "board.local", "http://board.local", orderToken)
			waitStarted(t, handler, "next-task")

			// Assert
			if response.Code != 202 {
				t.Fatalf("start = %d %s, want 202", response.Code, response.Body)
			}
			calls := spawner.recorded()
			if len(calls) != 1 {
				t.Fatalf("cfo spawn ran %d times, want once", len(calls))
			}
			brief := filepath.Join(h.Data, "next-task", "brief.md")
			want := append(append([]string{}, test.want[:4]...), append([]string{"--brief", brief}, test.want[4:]...)...)
			if !reflect.DeepEqual(calls[0], want) {
				t.Fatalf("cfo %v, want cfo %v", calls[0], want)
			}
			if order, _ := fleet.ReadAttention(h); !reflect.DeepEqual(order, []string{"next-task", "task-1"}) {
				t.Fatalf("attention order = %v, want the started task on top", order)
			}
			records, err := wake.Pending(h.State)
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 1 || records[0].Kind != "notify" || records[0].Key != "next-task" || !strings.HasPrefix(records[0].Detail, "started: the Overlord started this from the board") {
				t.Fatalf("wake records = %+v, want one notify telling the CFO", records)
			}
		})
	}
}

func TestStartShowsAFailedSpawnOnTheCardAndTellsTheCFO(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{output: "preparing\ncfo spawn: project auth preflight refused: GITHUB_TOKEN is red\n", err: errors.New("exit status 1")}
	handler, h := startBoard(t, 5*gigabyte, spawner)
	queueBriefedTask(t, h, "- **next-task** - Ship it", plainBrief)

	// Act
	response := postStart(handler, `{"task":"next-task"}`, "board.local", "http://board.local", orderToken)
	task := waitStarted(t, handler, "next-task")

	// Assert
	if response.Code != 202 {
		t.Fatalf("start = %d %s, want 202", response.Code, response.Body)
	}
	if !strings.Contains(task.StartError, "GITHUB_TOKEN is red") || task.Phase != "queued" {
		t.Fatalf("card = %+v, want it still queued with the spawn's reason", task)
	}
	records, _ := wake.Pending(h.State)
	if len(records) != 1 || !strings.HasPrefix(records[0].Detail, "start failed: ") || !strings.Contains(records[0].Detail, "GITHUB_TOKEN is red") {
		t.Fatalf("wake records = %+v, want the CFO told the start failed and why", records)
	}
	if _, blocking := wake.BlockingNotify(records[0]); blocking {
		t.Fatal("a failed start reads as a goblin's blocking question")
	}
}

func TestStartRetryAnswersARevisionFromWhichTheOldFailureIsGone(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{output: "cfo spawn: project auth preflight refused: GITHUB_TOKEN is red\n", err: errors.New("exit status 1")}
	handler, h := startBoard(t, 5*gigabyte, spawner)
	queueBriefedTask(t, h, "- **next-task** - Ship it", plainBrief)
	postStart(handler, `{"task":"next-task"}`, "board.local", "http://board.local", orderToken)
	failed := waitStarted(t, handler, "next-task")
	stale, err := handler.Service.Snapshot()
	if err != nil || failed.StartError == "" {
		t.Fatalf("first start = %+v, %v; want it failed", failed, err)
	}
	spawner.release = make(chan struct{})
	t.Cleanup(func() {
		close(spawner.release)
		waitStarted(t, handler, "next-task")
	})

	// Act
	response := postStart(handler, `{"task":"next-task"}`, "board.local", "http://board.local", orderToken)
	snapshot, err := handler.Service.Snapshot()

	// Assert
	var accepted struct {
		Revision uint64 `json:"revision"`
	}
	if response.Code != 202 || json.Unmarshal(response.Body.Bytes(), &accepted) != nil {
		t.Fatalf("retry = %d %s, want 202 with its revision", response.Code, response.Body)
	}
	if accepted.Revision <= stale.Revision {
		t.Fatalf("retry revision = %d, want it newer than %d, whose snapshot still shows the old failure", accepted.Revision, stale.Revision)
	}
	if err != nil || snapshot.Revision < accepted.Revision {
		t.Fatalf("snapshot revision = %d, %v; want at least %d", snapshot.Revision, err, accepted.Revision)
	}
	for _, task := range snapshot.Tasks {
		if task.ID == "next-task" && (task.StartError != "" || !task.Starting) {
			t.Fatalf("card = %+v, want it starting without the old failure", task)
		}
	}
}

func TestStartTakesOneTaskAtATime(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{release: make(chan struct{})}
	handler, h := startBoard(t, 16*gigabyte, spawner)
	queueBriefedTask(t, h, "- **next-task** - Ship it\n- **second** - Also", plainBrief)
	writeFile(t, filepath.Join(h.Data, "second", "brief.md"), plainBrief)

	// Act
	first := postStart(handler, `{"task":"next-task"}`, "board.local", "http://board.local", orderToken)
	second := postStart(handler, `{"task":"second"}`, "board.local", "http://board.local", orderToken)
	close(spawner.release)
	waitStarted(t, handler, "next-task")

	// Assert
	refusal := decodeRefusal(t, second)
	if first.Code != 202 || second.Code != 409 || !strings.Contains(refusal.Error, "next-task is starting") || !refusal.Passing {
		t.Fatalf("starts = %d, %d %s; want the second refused while the first starts, passing once it is up", first.Code, second.Code, second.Body)
	}
}

func TestSnapshotShowsATaskStartingUntilItsSpawnEndsEvenOnceItRuns(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{release: make(chan struct{})}
	handler, h := startBoard(t, 16*gigabyte, spawner)
	queueBriefedTask(t, h, "- **next-task** - Ship it", plainBrief)
	t.Cleanup(func() {
		close(spawner.release)
		waitStarted(t, handler, "next-task")
	})
	if response := postStart(handler, `{"task":"next-task"}`, "board.local", "http://board.local", orderToken); response.Code != 202 {
		t.Fatalf("start = %d %s, want 202", response.Code, response.Body)
	}
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "next-task", Project: h.Root, Worktree: h.Root, Harness: "claude", Mode: "no-mistakes", Kind: "ship", Backend: "native", SpawnGen: "s1"}); err != nil {
		t.Fatal(err)
	}

	// Act
	snapshot, err := handler.Service.Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range snapshot.Tasks {
		if task.ID == "next-task" {
			if task.Phase == "queued" || !task.Starting {
				t.Fatalf("card = %+v, want it live and still starting while cfo spawn runs", task)
			}
			return
		}
	}
	t.Fatal("the snapshot does not list next-task")
}

func TestSnapshotShowsMemoryCommitAndKernelPoolsAgainstTheFloorAndTheNextStart(t *testing.T) {
	// Arrange
	reading := Memory{Available: 4*gigabyte + gigabyte/10, Total: 32 * gigabyte, CommitAvailable: 20 * gigabyte, CommitLimit: 48 * gigabyte, PagedPool: 15*gigabyte + 6*gigabyte/10, NonpagedPool: 3*gigabyte + gigabyte/5}
	handler, _ := startBoardWith(t, reading, &spawnRecorder{})

	// Act
	snapshot, err := handler.Service.Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := reading
	want.Floor, want.Next = 4*gigabyte, 5*gigabyte
	want.Capacity = &FleetCapacity{Configured: 8}
	if !reflect.DeepEqual(snapshot.Memory, &want) {
		t.Fatalf("memory = %+v, want %+v", snapshot.Memory, want)
	}
}

func TestSnapshotNamesTheTopCommitHoldersOnlyWhileCommitIsTheTighter(t *testing.T) {
	holders := []CommitHolder{{Name: "ChatGPT", Commit: 11 * gigabyte}, {Name: "cfo", Commit: 6 * gigabyte}}
	tests := []struct {
		name              string
		available, commit uint64
		want              []CommitHolder
	}{
		{name: "commit is the tighter", available: 4 * gigabyte, commit: 2 * gigabyte, want: holders},
		{name: "memory is the tighter", available: 3 * gigabyte, commit: 20 * gigabyte},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			handler, _ := startBoardWith(t, Memory{Available: test.available, Total: 32 * gigabyte, CommitAvailable: test.commit, CommitLimit: 48 * gigabyte}, &spawnRecorder{})
			reads := 0
			handler.Service.Options.Dispatch.CommitHolders = func() ([]CommitHolder, error) { reads++; return holders, nil }

			// Act
			snapshot, err := handler.Service.Snapshot()

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(snapshot.Memory.Holders, test.want) {
				t.Fatalf("holders = %+v, want %+v", snapshot.Memory.Holders, test.want)
			}
			if test.want == nil && reads != 0 {
				t.Fatalf("read commit holders %d times while memory was the tighter", reads)
			}
		})
	}
}
