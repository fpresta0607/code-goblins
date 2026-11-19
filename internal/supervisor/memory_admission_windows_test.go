//go:build windows

package supervisor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// startingGoblin is a native goblin on harness whose terminal host, this test
// process standing in for it, started at startedAt.
func startingGoblin(t *testing.T, h home.Home, id, harness string, startedAt time.Time) {
	t.Helper()
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: id, Backend: "native", Harness: harness, SpawnGen: "generation-" + id}); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(host.Record{ID: id, HostPID: os.Getpid(), Started: startedAt, Pipe: "fixture", Token: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.State, "hosts", id+".json"), string(data))
}

// listing is the machine's process list with the goblin's host holding
// hostBytes, a child holding childBytes, that child's own child holding
// grandchildBytes, and a process of another tree holding a great deal.
func listing(hostBytes, childBytes, grandchildBytes uint64) func() ([]fleettree.Process, error) {
	return func() ([]fleettree.Process, error) {
		return []fleettree.Process{
			{PID: os.Getpid(), ParentPID: 1, Memory: hostBytes},
			{PID: 90001, ParentPID: os.Getpid(), Memory: childBytes},
			{PID: 90002, ParentPID: 90001, Memory: grandchildBytes},
			{PID: 90003, ParentPID: 1, Memory: 30 * gigabyte},
		}, nil
	}
}

// A goblin that started or resumed a moment ago has not taken its whole start
// yet. The room counts what its tree has still to take of its harness's
// cost, so the next goblin is admitted against memory that is spoken for.
func TestReadRoomCountsWhatAGoblinStillStartingHasYetToTake(t *testing.T) {
	started := time.Now()
	for _, test := range []struct {
		name          string
		processes     func() ([]fleettree.Process, error)
		startedAgo    time.Duration
		measuredGB    float64
		wantRemaining uint64
		wantPending   bool
	}{
		{name: "a tree that grew part way", processes: listing(gb(0.125), gb(0.125), 0), startedAgo: time.Minute, wantRemaining: gb(0.75), wantPending: true},
		{name: "a tree that grew by every process under the host", processes: listing(gb(0.25), gb(0.25), gb(0.25)), startedAgo: time.Minute, wantRemaining: gb(0.25), wantPending: true},
		{name: "a tree that took more than its cost", processes: listing(gb(1), gb(1), gb(1)), startedAgo: time.Minute, wantRemaining: 0, wantPending: true},
		{name: "a measured cost", processes: listing(gb(0.5), 0, 0), startedAgo: time.Minute, measuredGB: 2.5, wantRemaining: gb(2), wantPending: true},
		{name: "processes that cannot be read", processes: func() ([]fleettree.Process, error) { return nil, errors.New("no list") }, startedAgo: time.Minute, wantRemaining: gb(1), wantPending: true},
		{name: "no way to list processes", startedAgo: time.Minute, wantRemaining: gb(1), wantPending: true},
		{name: "a host that started before the settle time", processes: listing(0, 0, 0), startedAgo: launchSettle + time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h := roomHome(t, "")
			startingGoblin(t, h, "starting-task", "claude", started)
			if test.measuredGB > 0 {
				if err := writeStartCost(h.State, "claude", StartCost{Bytes: gb(test.measuredGB), Task: "earlier"}); err != nil {
					t.Fatal(err)
				}
			}
			now := started.Add(test.startedAgo)

			// Act
			room, err := ReadRoom(h, test.processes, now)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if !test.wantPending {
				if len(room.Pending) != 0 {
					t.Fatalf("pending = %+v, want a host past its settle time left out", room.Pending)
				}
				return
			}
			if len(room.Pending) != 1 || room.Pending[0].Task != "starting-task" || room.Pending[0].Harness != "claude" || room.Pending[0].Remaining != test.wantRemaining {
				t.Fatalf("pending = %+v, want starting-task with %d still to take", room.Pending, test.wantRemaining)
			}
		})
	}
}

// A goblin whose terminal's host does not run takes no memory, and neither
// does one that is not native.
func TestReadRoomLeavesOutAGoblinWhoseHostIsGoneOrWhichIsNotNative(t *testing.T) {
	// Arrange
	started := time.Now()
	h := roomHome(t, "")
	startingGoblin(t, h, "gone-task", "claude", started)
	gone, err := json.Marshal(host.Record{ID: "gone-task", HostPID: 1<<31 - 2, Started: started, Pipe: "fixture", Token: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.State, "hosts", "gone-task.json"), string(gone))
	startingGoblin(t, h, "herdr-task", "claude", started)
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "herdr-task", Backend: "herdr", HerdrWorkspaceID: "workspace", HerdrSession: "session", HerdrTabID: "tab", HerdrPaneID: "pane", Harness: "claude", SpawnGen: "generation-herdr"}); err != nil {
		t.Fatal(err)
	}

	// Act
	room, err := ReadRoom(h, listing(0, 0, 0), started.Add(time.Minute))

	// Assert
	if err != nil || len(room.Pending) != 0 {
		t.Fatalf("pending = %+v, %v, want none", room.Pending, err)
	}
}

// A goblin just started is counted before the next is admitted. Free memory
// of 5.5 GB admits a claude start that costs the 1 GB nothing has measured,
// but not with a goblin one minute into its start holding 0.2 GB of it: the
// 0.8 GB it has yet to take is not free either.
func TestAGoblinJustStartedIsCountedBeforeTheNextIsAdmitted(t *testing.T) {
	for _, test := range []struct {
		name       string
		isStarting bool
		dispatch   []string
	}{
		{name: "no goblin is starting", dispatch: []string{"spawn", "next-task"}},
		{name: "a goblin started a minute ago", isStarting: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, gb(5.5), spawner)
			service := handler.Service
			service.Options.Dispatch.Processes = listing(gb(0.1), gb(0.1), 0)
			queueBriefedTask(t, h, roomRow, plainBrief)
			started := time.Now()
			if test.isStarting {
				startingGoblin(t, h, "running-task", "claude", started)
			}

			// Act
			err := service.checkFleet(t.Context(), started.Add(time.Minute))

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if test.dispatch != nil {
				if calls := awaitDispatch(t, service, spawner, 1); !slices.Equal(calls[0][:2], test.dispatch) {
					t.Fatalf("dispatches=%v, want %v first", calls, test.dispatch)
				}
				return
			}
			line := schedulingLine(service)
			if calls := spawner.recorded(); len(calls) != 0 || !strings.Contains(line, "goblins starting 0.8") || !strings.Contains(line, "a claude start needs 5.8 GB") {
				t.Fatalf("dispatches %v and line %q, want none and the 0.8 GB still to be taken named", calls, line)
			}
		})
	}
}
