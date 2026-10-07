package supervisor

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// While runnable work waits with memory free and nothing starts, the CFO is
// woken after idleWakeAfter, and again each idleWakeAfter it lasts, naming
// each waiting task and why it did not start. Work that waits on another task
// or already finished is not runnable and wakes nobody, and nor does work
// waiting on memory, which memory_ready covers. On 2026-10-07 memory_ready
// woke once and a CFO that could not start the work idled four hours.
func TestTheCFOIsWokenAgainWhileRunnableWorkWaitsAndNothingStarts(t *testing.T) {
	cases := []struct {
		name      string
		available float64
		arrange   func(t *testing.T, h home.Home)
		spawnFail bool
		wantWhy   string
		// began is the reading at which the work is first seen waiting:
		// the reading after its start failed, or the first.
		began int
	}{
		{
			name:      "its start failed",
			available: 8,
			arrange: func(t *testing.T, h home.Home) {
				queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins)", plainBrief)
			},
			spawnFail: true,
			wantWhy:   "next-task: its last start failed: refused: the auth preflight is red",
			began:     1,
		},
		{
			name:      "its row needs the CFO",
			available: 8,
			arrange: func(t *testing.T, h home.Home) {
				queueBriefedTask(t, h, "- **next-task** - Ship it", "# Brief next-task\n\n## Task\n\nShip it.\n")
			},
			wantWhy: "next-task: The brief for next-task names no project",
		},
		{
			name:      "it waits on another task",
			available: 8,
			arrange: func(t *testing.T, h home.Home) {
				queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins) blocked-by: other-task - after it", plainBrief)
			},
		},
		{
			name:      "it already finished",
			available: 8,
			arrange: func(t *testing.T, h home.Home) {
				queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins)", plainBrief)
				writeFile(t, filepath.Join(h.State, "archive", "next-task.status.20261006T120000Z"), "2026-10-06T11:59:00Z done: returned worktree C:\\w via cfo cleanup\n")
			},
		},
		{
			name:      "memory is short",
			available: 4.5,
			arrange: func(t *testing.T, h home.Home) {
				queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins)", plainBrief)
			},
			spawnFail: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, uint64(testCase.available*gigabyte), spawner)
			testCase.arrange(t, h)
			if testCase.spawnFail {
				handler.Service.Options.Dispatch.Spawn = func(ctx context.Context, args []string) (string, error) {
					_, _ = spawner.spawn(ctx, args)
					return "refused: the auth preflight is red", errors.New("exit status 1")
				}
			}
			start := time.Now().UTC().Truncate(time.Minute)
			var woke []int

			// Act
			for minute := range 63 {
				if err := handler.Service.checkFleet(t.Context(), start.Add(time.Duration(minute)*time.Minute)); err != nil {
					t.Fatal(err)
				}
				awaitDispatch(t, handler.Service, spawner, len(spawner.recorded()))
				if count := len(fleetWakeRecords(t, h, "idle")); count > len(woke) {
					woke = append(woke, minute)
				}
			}

			// Assert
			wakes := fleetWakeRecords(t, h, "idle")
			if testCase.wantWhy == "" {
				if len(wakes) != 0 {
					t.Fatalf("idle wakes %v, want none", wakes)
				}
				return
			}
			if want := []int{testCase.began + 30, testCase.began + 60}; len(woke) != 2 || woke[0] != want[0] || woke[1] != want[1] {
				t.Fatalf("idle wakes at minutes %v, want %v: 30 minutes after the work began waiting at minute %d, and 30 after that", woke, want, testCase.began)
			}
			if detail := wakes[0].Detail; !strings.HasPrefix(detail, "idle: ") || !strings.Contains(detail, testCase.wantWhy) || !strings.Contains(detail, "8.0 GB of memory") {
				t.Errorf("wake = %q, want the free memory and %q", detail, testCase.wantWhy)
			}
		})
	}
}

// The board names what the scheduler did at its last reading with memory
// free: what it started or resumed, else why nothing waiting started, else
// that nothing waits.
func TestTheSnapshotNamesWhatTheSchedulerDid(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(t *testing.T, h home.Home)
		fail    bool
		want    string
	}{
		{name: "it started the next task", arrange: func(t *testing.T, h home.Home) {
			queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins)", plainBrief)
		}, want: "starting next-task"},
		{name: "it resumed a cleared pause", arrange: func(t *testing.T, h home.Home) {
			pausedGoblin(t, h, "paused-task", "dependency", "date:2000-01-01T00:00:00Z", time.Now().UTC().Add(-time.Hour))
		}, want: "resuming paused-task"},
		{name: "its last start failed", arrange: func(t *testing.T, h home.Home) {
			queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins)", plainBrief)
		}, fail: true, want: "nothing starts: next-task: its last start failed: refused: the auth preflight is red"},
		{name: "nothing waits", arrange: func(t *testing.T, h home.Home) {}, want: "nothing waits to start"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 8*gigabyte, spawner)
			testCase.arrange(t, h)
			if testCase.fail {
				handler.Service.Options.Dispatch.Spawn = func(ctx context.Context, args []string) (string, error) {
					_, _ = spawner.spawn(ctx, args)
					return "refused: the auth preflight is red", errors.New("exit status 1")
				}
			}
			now := time.Now().UTC()

			// Act
			readings := 1
			if testCase.fail {
				readings = 2
			}
			for reading := range readings {
				if err := handler.Service.checkFleet(t.Context(), now.Add(time.Duration(reading)*time.Minute)); err != nil {
					t.Fatal(err)
				}
				awaitDispatch(t, handler.Service, spawner, len(spawner.recorded()))
			}

			// Assert
			snapshot, err := handler.Service.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Scheduling == nil || snapshot.Scheduling.Text != testCase.want {
				t.Fatalf("scheduling = %+v, want %q", snapshot.Scheduling, testCase.want)
			}
		})
	}
}
