package supervisor

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// lockingSpawner runs cfo spawn and cfo resume as they meet the home's spawn
// lock: each takes it for its run, and one that finds it held fails as they
// print it. Every other command succeeds.
type lockingSpawner struct {
	spawnRecorder
	stateDir string
	met      chan struct{}
	once     sync.Once
}

func (r *lockingSpawner) spawn(ctx context.Context, args []string) (string, error) {
	_, _ = r.spawnRecorder.spawn(ctx, args)
	refusal := map[string]string{"spawn": "spawn: acquire spawn lock: ", "resume": "another task is starting: "}[args[0]]
	if refusal == "" {
		return "", nil
	}
	if _, err := lock.AcquireExclusiveNamed(r.stateDir, ".spawn.lock"); err != nil {
		r.once.Do(func() { close(r.met) })
		return refusal + err.Error() + "\n", errors.New("cfo exited 1")
	}
	return "done\n", lock.ReleaseExclusiveNamed(r.stateDir, ".spawn.lock")
}

func (r *lockingSpawner) runs(command string) [][]string {
	return slices.DeleteFunc(r.recorded(), func(call []string) bool { return call[0] != command })
}

func TestAStartThatMeetsAHandRunSpawnTriesAgainOnceTheLockFrees(t *testing.T) {
	for _, test := range []struct {
		name, command string
		// start begins the start under test on the board s runs.
		start func(t *testing.T, s *Service, h home.Home)
		// failure is why the start failed, empty once it went through.
		failure func(s *Service, spawner *lockingSpawner) string
	}{
		{
			name: "a helper's", command: "spawn",
			start: func(t *testing.T, s *Service, h home.Home) {
				if _, err := s.acceptHelper(HelperRequest{Parent: "g1", Brief: helperBrief}); err != nil {
					t.Fatal(err)
				}
			},
			failure: func(_ *Service, spawner *lockingSpawner) string {
				for _, call := range spawner.runs("send") {
					if call[0] == "send" && !strings.Contains(call[2], "is up on branch") {
						return call[2]
					}
				}
				return ""
			},
		},
		{
			name: "a queued task's", command: "spawn",
			start: func(t *testing.T, s *Service, h home.Home) {
				queueBriefedTask(t, h, "- **next-task** - Ship it", plainBrief)
				if err := s.startQueued("next-task", true); err != nil {
					t.Fatal(err)
				}
			},
			failure: func(s *Service, _ *lockingSpawner) string { return s.startErrors["next-task"] },
		},
		{
			name: "an automatic resume's", command: "resume",
			start: func(t *testing.T, s *Service, h home.Home) {
				pausedGoblin(t, h, "paused-task", "memory", "", time.Now().UTC().Add(-time.Hour))
				record, err := state.ReadLifecycle(h.State, "paused-task")
				if err != nil {
					t.Fatal(err)
				}
				if err := s.resumeAutomatically(record); err != nil {
					t.Fatal(err)
				}
			},
			failure: func(s *Service, _ *lockingSpawner) string { return s.changeErrors["paused-task"].Message },
		},
		{
			name: "the board's Resume's", command: "resume",
			start: func(t *testing.T, s *Service, h home.Home) {
				meta := pausedGoblin(t, h, "paused-task", "overlord", "", time.Now().UTC().Add(-time.Hour))
				if response := taskControlRequest(NewHTTP(s, "board.local", nil), "/api/tasks/lifecycle", map[string]string{"task": meta.ID, "generation": meta.SpawnGen, "operation": "resume-1", "action": "resume"}); response.Code != 202 {
					t.Fatalf("resume = %d %s", response.Code, response.Body)
				}
			},
			failure: func(s *Service, _ *lockingSpawner) string { return s.changeErrors["paused-task"].Message },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: a cfo spawn run by hand holds the spawn lock.
			spawner := &lockingSpawner{met: make(chan struct{})}
			s, h := helperBoard(t, 8*gigabyte, &spawner.spawnRecorder)
			spawner.stateDir = h.State
			s.Options.Dispatch.Spawn = spawner.spawn
			if _, err := lock.AcquireExclusiveNamed(h.State, ".spawn.lock"); err != nil {
				t.Fatal(err)
			}

			// Act: the start meets the lock, and the hand-run spawn ends.
			test.start(t, s, h)
			select {
			case <-spawner.met:
			case <-time.After(10 * time.Second):
				t.Fatal("the start never met the held spawn lock")
			}
			if err := lock.ReleaseExclusiveNamed(h.State, ".spawn.lock"); err != nil {
				t.Fatal(err)
			}
			awaitDispatch(t, s, &spawner.spawnRecorder, 2)

			// Assert
			runs := spawner.runs(test.command)
			if len(runs) != 2 || !slices.Equal(runs[0], runs[1]) {
				t.Fatalf("cfo %s ran %q, want the same run twice", test.command, runs)
			}
			s.starts.Lock()
			failure := test.failure(s, spawner)
			s.starts.Unlock()
			if failure != "" {
				t.Fatalf("the start failed: %s", failure)
			}
		})
	}
}
