package supervisor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// The Overlord, 2026-10-07, shown a toast that a goblin the CFO was retiring
// had failed: its pause missed the handoff deadline and its teardown ran out
// of time, and the lifecycle's report to the CFO began with the goblin's own
// "failed:" verb. The board read it as the goblin failing and waiting on the
// CFO, and the CFO-quiet notice counted it as a question. A pause or stop
// someone asked for never marks the goblin failed; the CFO still hears of it.
func TestAPauseThatDidNotFinishOnARetiredGoblinIsNeitherItsFailureNorAQuestion(t *testing.T) {
	for action, reason := range map[string]string{"pause": "overlord", "stop": "Requested by the operator"} {
		t.Run(action, func(t *testing.T) {
			// Arrange: a goblin whose work is done, retired by the CFO.
			store, h := testStore(t)
			meta := state.TaskMeta{ID: "task-1", Project: h.Root, Worktree: filepath.Join(h.Root, "work"), Harness: "claude", Mode: "direct-PR", Kind: "ship", Backend: "native", SpawnGen: "g1", TaskTmp: filepath.Join(h.State, "tasktmp", "task-1")}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			if err := state.AppendStatus(h.State, "task-1", "done: PR https://github.com/o/r/pull/7"); err != nil {
				t.Fatal(err)
			}
			retire := lifecycle.Service{StateDir: h.State, PrepareWait: 10 * time.Millisecond, PauseWait: 10 * time.Millisecond, Operations: lifecycle.Operations{
				// The goblin never writes its handoff before the deadline.
				Prepare: func(context.Context, state.TaskMeta, string) error { return nil },
				Stop: func(context.Context, state.TaskMeta, *state.Lifecycle) ([]string, error) {
					return nil, context.DeadlineExceeded
				},
				Notify: func(record state.Lifecycle) error { return lifecycle.Report(h.State, record) },
			}}
			record, err := retire.Run(t.Context(), lifecycle.Request{ID: "task-1", Generation: "g1", Operation: "op-1", Action: action, Reason: reason})
			if err == nil || record.Phase != "failed" {
				t.Fatalf("the %s finished as %q, %v; want it failed, as the incident's did", action, record.Phase, err)
			}

			// Act
			view, err := (&Service{Store: store}).Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			pending, err := wake.Pending(h.State)
			if err != nil {
				t.Fatal(err)
			}

			// Assert
			if len(view.Tasks) != 1 || view.Tasks[0].Phase == "failed" || view.Tasks[0].Phase == "blocked" || strings.HasPrefix(view.Tasks[0].Reason, "Waiting on the CFO") {
				t.Errorf("the goblin reads as %+v; want it neither failed nor waiting on the CFO", view.Tasks)
			}
			if notice := cfoQuietNotice(pending, time.Now().Add(time.Hour)); notice != nil {
				t.Errorf("an hour on, the report counts as %d questions left on the CFO; want none", notice.Count)
			}
			for _, wakeRecord := range pending {
				if verb, isQuestion := wake.BlockingNotify(wakeRecord); isQuestion {
					t.Errorf("the CFO's queue reads the report as the goblin's %s question: %+v", verb, wakeRecord)
				}
			}
			if len(view.Decisions) != 1 || !strings.Contains(view.Decisions[0].Detail, "context deadline exceeded") || action == "pause" && !strings.Contains(view.Decisions[0].Detail, "no new handoff was saved") {
				t.Errorf("the CFO hears %+v; want the report with what went wrong", view.Decisions)
			}
		})
	}
}
