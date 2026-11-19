//go:build windows

package supervisor

import (
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// mebibyte is a mebibyte in bytes, the unit the process trees are written in.
const mebibyte = 1 << 20

// rehosted gives meta's goblin a terminal host of its own, a stand-in process
// of this test that ends with it, started at started, and returns its pid.
func rehosted(t *testing.T, h home.Home, meta state.TaskMeta, started time.Time) int {
	t.Helper()
	standIn := exec.Command("ping", "-n", "60", "127.0.0.1")
	if err := standIn.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = standIn.Process.Kill(); _, _ = standIn.Process.Wait() })
	data, err := json.Marshal(host.Record{ID: meta.ID, HostPID: standIn.Process.Pid, Started: started, Pipe: "fixture", Token: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.State, "hosts", meta.ID+".json"), string(data))
	return standIn.Process.Pid
}

// fakeTrees lists a host holding hostBytes and a child holding childBytes for
// each pid of trees, and fails when it is nil.
func fakeTrees(trees map[int][2]uint64) func() ([]fleettree.Process, error) {
	return func() ([]fleettree.Process, error) {
		if trees == nil {
			return nil, errors.New("the process list cannot be read")
		}
		var processes []fleettree.Process
		for pid, bytes := range trees {
			processes = append(processes,
				fleettree.Process{PID: pid, ParentPID: 1, Memory: bytes[0]},
				fleettree.Process{PID: pid + 100000, ParentPID: pid, Memory: bytes[1]})
		}
		return processes, nil
	}
}

// The memory floor pauses the goblin whose process tree grew most since the
// guard's reading before, though another is lower in the Overlord's order, and
// the lowest when no tree grew or the processes cannot be listed.
func TestTheGuardPausesTheGoblinWhoseProcessTreeGrewMost(t *testing.T) {
	for _, test := range []struct {
		name   string
		before map[int][2]uint64
		after  map[int][2]uint64
		want   string
	}{
		{name: "the older goblin's tree grew most", before: map[int][2]uint64{0: {100 * mebibyte, 100 * mebibyte}, 1: {100 * mebibyte, 100 * mebibyte}}, after: map[int][2]uint64{0: {100 * mebibyte, 600 * mebibyte}, 1: {150 * mebibyte, 100 * mebibyte}}, want: "older-task"},
		{name: "no tree grew", before: map[int][2]uint64{0: {100 * mebibyte, 600 * mebibyte}, 1: {100 * mebibyte, 100 * mebibyte}}, after: map[int][2]uint64{0: {100 * mebibyte, 100 * mebibyte}, 1: {100 * mebibyte, 100 * mebibyte}}, want: "newer-task"},
		{name: "the processes cannot be listed", before: map[int][2]uint64{0: {100 * mebibyte, 100 * mebibyte}, 1: {100 * mebibyte, 100 * mebibyte}}, want: "newer-task"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			s, h, older, newer, now := floorBoard(t, false, spawner, [2]float64{8, 8}, [2]float64{3, 8})
			started := time.Now().Add(time.Hour)
			pids := []int{rehosted(t, h, older, started), rehosted(t, h, newer, started)}
			byPid := func(trees map[int][2]uint64) map[int][2]uint64 {
				if trees == nil {
					return nil
				}
				return map[int][2]uint64{pids[0]: trees[0], pids[1]: trees[1]}
			}
			s.Options.Dispatch.Processes = fakeTrees(byPid(test.before))

			// Act
			readGuard(t, s, now)
			s.Options.Dispatch.Processes = fakeTrees(byPid(test.after))
			readGuard(t, s, now.Add(memoryGuardEvery))
			calls := awaitRecorded(t, spawner, 1)

			// Assert
			if len(calls) != 1 || calls[0][0] != "pause" || calls[0][1] != test.want {
				t.Fatalf("dispatches = %v, want %s paused", calls, test.want)
			}
		})
	}
}

// The guard measures what a start costs: a goblin it saw from its host's
// start has the most its process tree held written as its harness's sample
// once launchSettle has passed, and one first seen later than a minute after
// its start is never measured.
func TestTheGuardWritesTheStartCostOfAGoblinItSawStart(t *testing.T) {
	for _, test := range []struct {
		name      string
		firstSeen time.Duration
		want      []StartCost
	}{
		{name: "seen from its start", firstSeen: 10 * time.Second, want: []StartCost{{Bytes: 700 * mebibyte, Task: "starting-task"}}},
		{name: "first seen two minutes after its start", firstSeen: 2 * time.Minute},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 16*gigabyte, spawner)
			s := handler.Service
			started := time.Now().Add(time.Hour)
			meta := state.TaskMeta{ID: "starting-task", Backend: "native", Harness: "codex", SpawnGen: "generation-starting-task"}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			pid := rehosted(t, h, meta, started)
			var current [2]uint64
			s.Options.Dispatch.Processes = func() ([]fleettree.Process, error) { return fakeTrees(map[int][2]uint64{pid: current})() }
			readings := []struct {
				after time.Duration
				tree  [2]uint64
			}{
				{test.firstSeen, [2]uint64{100 * mebibyte, 300 * mebibyte}},
				{3 * time.Minute, [2]uint64{100 * mebibyte, 600 * mebibyte}},
				{4 * time.Minute, [2]uint64{100 * mebibyte, 200 * mebibyte}},
			}

			// Act
			for _, reading := range readings {
				current = reading.tree
				readGuard(t, s, started.Add(reading.after))
			}
			beforeSettled, err := readStartCosts(h.State)
			if err != nil {
				t.Fatal(err)
			}
			current = [2]uint64{100 * mebibyte, 50 * mebibyte}
			readGuard(t, s, started.Add(launchSettle))
			costs, err := readStartCosts(h.State)

			// Assert
			if err != nil || len(beforeSettled) != 0 {
				t.Fatalf("costs before launchSettle = %+v, after = %+v (%v), want none before", beforeSettled, costs, err)
			}
			got := costs["codex"]
			if len(got) != len(test.want) || len(costs) > 1 {
				t.Fatalf("codex costs = %+v (all %+v), want %+v", got, costs, test.want)
			}
			for i, want := range test.want {
				if got[i].Bytes != want.Bytes || got[i].Task != want.Task || !got[i].At.Equal(started.Add(launchSettle)) {
					t.Errorf("codex cost %d = %+v, want %d bytes of %s", i, got[i], want.Bytes, want.Task)
				}
			}
		})
	}
}
