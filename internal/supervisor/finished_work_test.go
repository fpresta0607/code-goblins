package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// Finished work never starts again by itself, whatever says it finished: its
// last report, live or archived, or its pull request merged, as the fleet's
// history knows it. The Overlord's Start is refused with the same reason, and
// the card says it.
func TestSchedulerNeverRestartsFinishedWork(t *testing.T) {
	const pullRequest = "https://github.com/fpresta0607/code-goblins/pull/9"
	cases := []struct {
		name      string
		arrange   func(t *testing.T, service *Service, h home.Home)
		wantStart bool
		evidence  string
	}{
		{
			name: "its live status log last reported done",
			arrange: func(t *testing.T, _ *Service, h home.Home) {
				writeFile(t, filepath.Join(h.State, "next-task.status"), "2026-10-06T10:00:00Z working: building\n2026-10-06T11:00:00Z done: PR "+pullRequest+"\n2026-10-06T11:05:00Z lifecycle-stopped: retired\n")
			},
			evidence: "its last report was done",
		},
		{
			name: "its archived status log last reported done",
			arrange: func(t *testing.T, _ *Service, h home.Home) {
				writeFile(t, filepath.Join(h.State, "archive", "next-task.status.20261001T090000Z"), "2026-10-01T08:00:00Z working: first try\n")
				writeFile(t, filepath.Join(h.State, "archive", "next-task.status.20261006T120000Z"), "2026-10-06T11:00:00Z working: building\n2026-10-06T11:59:00Z done: returned worktree C:\\w via cfo cleanup\n")
			},
			evidence: "its last report was done",
		},
		{
			name: "its stopped outcome's pull request merged",
			arrange: func(t *testing.T, service *Service, h home.Home) {
				writeOutcome(t, h, state.Outcome{ID: "next-task", Generation: "s1", Title: "Ship it", Project: h.Root, Phase: "stopped", Reason: "Stopped by the CFO", PR: pullRequest, At: time.Now().UTC().Add(-time.Hour)})
				service.Options.PullRequestState = func(context.Context, string) (PullRequestInfo, error) {
					return PullRequestInfo{State: "MERGED", Title: "Ship it"}, nil
				}
				if err := service.refreshHistory(t.Context(), time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			},
			evidence: "pull request merged",
		},
		{
			name: "a merged pull request of its branch",
			arrange: func(t *testing.T, service *Service, h home.Home) {
				writeOutcome(t, h, state.Outcome{ID: "next-task", Generation: "s1", Title: "Ship it", Project: h.Root, Branch: "feat/ship-it", Phase: "stopped", Reason: "Worktree returned by cleanup", At: time.Now().UTC().Add(-time.Hour)})
				service.Options.MergedPRs = func(context.Context, time.Time) ([]MergedPR, error) {
					return []MergedPR{{PR: pullRequest, Branch: "feat/ship-it", Project: h.Root, At: time.Now().Unix()}}, nil
				}
				if err := service.refreshHistory(t.Context(), time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			},
			evidence: "pull request merged",
		},
		{
			name: "it was stopped before it delivered",
			arrange: func(t *testing.T, service *Service, h home.Home) {
				writeFile(t, filepath.Join(h.State, "archive", "next-task.status.20261006T120000Z"), "2026-10-06T11:00:00Z done: PR "+pullRequest+"\n2026-10-06T11:30:00Z working: more to do\n2026-10-06T11:59:00Z stopped: returned worktree C:\\w via cfo cleanup\n")
				writeOutcome(t, h, state.Outcome{ID: "next-task", Generation: "s1", Title: "Ship it", Project: h.Root, Branch: "feat/ship-it", Phase: "stopped", Reason: "Worktree returned by cleanup", At: time.Now().UTC().Add(-time.Hour)})
				if err := service.refreshHistory(t.Context(), time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			},
			wantStart: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 8*gigabyte, spawner)
			queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins)", plainBrief)
			testCase.arrange(t, handler.Service, h)

			// Act
			if err := handler.Service.checkFleet(t.Context(), time.Now().UTC()); err != nil {
				t.Fatal(err)
			}

			// Assert
			if testCase.wantStart {
				if calls := awaitDispatch(t, handler.Service, spawner, 1); calls[0][0] != "spawn" || calls[0][1] != "next-task" {
					t.Fatalf("dispatches=%v, want next-task started", calls)
				}
				return
			}
			awaitDispatch(t, handler.Service, spawner, 0)
			if calls := spawner.recorded(); len(calls) != 0 {
				t.Fatalf("finished work started again: %v", calls)
			}
			card := queuedCard(t, handler, "next-task")
			if !strings.Contains(strings.ToLower(card.Finished), "already finished") || !strings.Contains(card.Finished, testCase.evidence) {
				t.Errorf("card says %q, want why it never starts again (%s)", card.Finished, testCase.evidence)
			}
			response := postStart(handler, `{"task":"next-task"}`, "board.local", "http://board.local", orderToken)
			if response.Code != 409 || !strings.Contains(strings.ToLower(response.Body.String()), "already finished") {
				t.Errorf("Start=%d %s, want it refused as finished", response.Code, response.Body)
			}
		})
	}
}

// The work memory waits for, which memory_ready and the CFO's turn end name,
// leaves out work whose last report was done.
func TestMemoryWorkLeavesOutFinishedWork(t *testing.T) {
	// Arrange
	_, h := orderBoard(t)
	writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n- **done-task** - Shipped (repo: code-goblins)\n- **next-task** - Ship it (repo: code-goblins)\n")
	writeFile(t, filepath.Join(h.Data, "done-task", "brief.md"), strings.ReplaceAll(plainBrief, "next-task", "done-task"))
	writeFile(t, filepath.Join(h.Data, "next-task", "brief.md"), plainBrief)
	writeFile(t, filepath.Join(h.State, "archive", "done-task.status.20261006T120000Z"), "2026-10-06T11:59:00Z done: returned worktree C:\\w via cfo cleanup\n")

	// Act
	queued, _ := memoryWork(h, readFinishedWork(h, nil))

	// Assert
	if len(queued) != 1 || queued[0] != "next-task" {
		t.Fatalf("queued=%v, want only next-task", queued)
	}
}

func writeOutcome(t *testing.T, h home.Home, outcome state.Outcome) {
	t.Helper()
	if err := state.WriteOutcome(h.State, outcome); err != nil {
		t.Fatal(err)
	}
}

func queuedCard(t *testing.T, handler *HTTP, id string) Task {
	t.Helper()
	snapshot, err := handler.Service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range snapshot.Tasks {
		if task.ID == id && task.Phase == "queued" {
			return task
		}
	}
	t.Fatalf("no queued card for %s in %+v", id, snapshot.Tasks)
	return Task{}
}

// A task an older build retired, or one whose cleanup could not take the
// backlog's lock, leaves ## Queued once the supervisor sees its outcome says
// it delivered; one still live, or not delivered, keeps its row.
func TestSupervisorMovesDeliveredTasksFromQueuedToDone(t *testing.T) {
	// Arrange
	handler, h := orderBoard(t)
	writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n- **delivered** - Shipped\n  detail: kept\n- **live** - Running again\n- **stopped** - Stopped early\n\n## Done\n")
	writeOutcome(t, h, state.Outcome{ID: "delivered", Phase: "done", Evidence: "reported pull request", PR: "https://github.com/o/r/pull/1", At: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)})
	writeOutcome(t, h, state.Outcome{ID: "live", Phase: "done", Evidence: "reported pull request", At: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)})
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "live", SpawnGen: "s2"}); err != nil {
		t.Fatal(err)
	}
	writeOutcome(t, h, state.Outcome{ID: "stopped", Phase: "stopped", At: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)})

	// Act
	err := handler.Service.closeDeliveredRows()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(h.Data, "backlog.md"))
	if want := "## Queued\n- **live** - Running again\n- **stopped** - Stopped early\n\n## Done\n- [x] delivered - Shipped https://github.com/o/r/pull/1 (done 2026-10-06)\n  detail: kept\n"; string(got) != want {
		t.Errorf("backlog =\n%s\nwant\n%s", got, want)
	}
}
