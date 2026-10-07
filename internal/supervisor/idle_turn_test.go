package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// A CFO whose turn ends with no goblin at work while work that could run
// waits and memory is free is woken again, naming the next work: a paused
// goblin whose pause cleared first, then the top of the queue. On 2026-10-07
// the CFO retired its last goblin at 08:20Z with 30 rows queued and 13.6 GB
// free, and nothing reopened its turn for four hours.
func TestAnIdleTurnIsWokenWithTheNextWork(t *testing.T) {
	plenty := Memory{Available: 8 * gigabyte, CommitAvailable: 12 * gigabyte}
	cases := []struct {
		name    string
		memory  Memory
		arrange func(t *testing.T, h home.Home)
		want    []string
	}{
		{
			name:   "queued work and nothing at work",
			memory: plenty,
			arrange: func(t *testing.T, h home.Home) {
				queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins)", plainBrief)
			},
			want: []string{"idle: ", "no goblin at work", "8.0 GB of memory", "next: start next-task", "cfo spawn next-task --project"},
		},
		{
			name:   "a goblin paused for memory comes back first",
			memory: plenty,
			arrange: func(t *testing.T, h home.Home) {
				queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins)", plainBrief)
				pausedGoblin(t, h, "paused-task", "memory", "", time.Now().UTC().Add(-time.Hour))
			},
			want: []string{"next: resume paused-task with cfo resume paused-task", "then start next-task"},
		},
		{
			name:   "a pause whose date passed",
			memory: plenty,
			arrange: func(t *testing.T, h home.Home) {
				pausedGoblin(t, h, "paused-task", "dependency", "date:2000-01-01T00:00:00Z", time.Now().UTC().Add(-time.Hour))
			},
			want: []string{"next: resume paused-task"},
		},
		{
			name:   "a goblin at work",
			memory: plenty,
			arrange: func(t *testing.T, h home.Home) {
				queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins)", plainBrief)
				if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "working-task", SpawnGen: "s1"}); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:   "memory short",
			memory: Memory{Available: 4.5 * gigabyte, CommitAvailable: 12 * gigabyte},
			arrange: func(t *testing.T, h home.Home) {
				queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins)", plainBrief)
			},
		},
		{
			name:   "only finished work",
			memory: plenty,
			arrange: func(t *testing.T, h home.Home) {
				queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins)", plainBrief)
				writeFile(t, filepath.Join(h.State, "archive", "next-task.status.20261006T120000Z"), "2026-10-06T11:59:00Z done: returned worktree C:\\w via cfo cleanup\n")
			},
		},
		{
			name:   "only a goblin paused on the Overlord",
			memory: plenty,
			arrange: func(t *testing.T, h home.Home) {
				pausedGoblin(t, h, "paused-task", "overlord", "", time.Now().UTC().Add(-time.Hour))
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			h := bareHome(t)
			testCase.arrange(t, h)

			// Act
			detail, err := IdleTurnWake(h, testCase.memory, time.Now().UTC())

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			wakes := fleetWakeRecords(t, h, "idle")
			if len(testCase.want) == 0 {
				if detail != "" || len(wakes) != 0 {
					t.Fatalf("detail %q, wakes %v; want the turn left to end", detail, wakes)
				}
				return
			}
			if len(wakes) != 1 || wakes[0].Detail != detail {
				t.Fatalf("wakes %v, want one idle wake carrying %q", wakes, detail)
			}
			for _, want := range testCase.want {
				if !strings.Contains(detail, want) {
					t.Errorf("detail %q, want %q", detail, want)
				}
			}
		})
	}
}

// The same next work wakes an idle turn once per idleWakeAfter, so a CFO that
// cannot start it is not woken at every turn end; other next work wakes it
// at once.
func TestAnIdleTurnIsWokenOncePerNextWorkPerInterval(t *testing.T) {
	// Arrange
	h := bareHome(t)
	queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins)", plainBrief)
	plenty := Memory{Available: 8 * gigabyte, CommitAvailable: 12 * gigabyte}
	start := time.Now().UTC()
	raised := func(at time.Time) bool {
		t.Helper()
		detail, err := IdleTurnWake(h, plenty, at)
		if err != nil {
			t.Fatal(err)
		}
		return detail != ""
	}

	// Act and assert
	if !raised(start) {
		t.Fatal("the first idle turn end raised nothing")
	}
	if raised(start.Add(time.Minute)) || raised(start.Add(29*time.Minute)) {
		t.Fatal("the same next work woke the CFO again within the interval")
	}
	if !raised(start.Add(30 * time.Minute)) {
		t.Fatal("the same next work did not wake the CFO again after the interval")
	}
	pausedGoblin(t, h, "paused-task", "memory", "", start)
	if !raised(start.Add(31 * time.Minute)) {
		t.Fatal("new next work did not wake the CFO at once")
	}
}

// The CFO's Stop hook stays armed while queued work waits with no goblin in
// flight, so the wakes that work raises reach it.
func TestWorkWaitsWhileAQueuedTaskCouldStart(t *testing.T) {
	// Arrange
	h := bareHome(t)
	before := WorkWaits(h)
	queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins)", plainBrief)

	// Act
	after := WorkWaits(h)

	// Assert
	if before || !after {
		t.Fatalf("work waits before %v and after queueing %v, want false then true", before, after)
	}
}

// bareHome is a home with nothing in it: no goblin, no queue, no wake.
func bareHome(t *testing.T) home.Home {
	t.Helper()
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	for _, dir := range []string{h.State, h.Data} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return h
}
