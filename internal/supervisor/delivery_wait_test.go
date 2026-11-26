package supervisor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/project"
)

// approveDelivery gives the checkout's project fast checks under a
// no-mistakes posture and keeps the registered primary CEO's exception
// approving mode for task id, as goblins brief or spawn keeps it.
func approveDelivery(t *testing.T, h home.Home, id, checkout, mode string) {
	t.Helper()
	manifest := writeChecks(t, h, checkout, project.Command{"git", "status"})
	if err := project.RecordDeliveryException(h.Data, id, mode, "the Overlord approved it", "registration", manifest); err != nil {
		t.Fatal(err)
	}
}

func writeChecks(t *testing.T, h home.Home, checkout string, fast project.Command) project.Manifest {
	t.Helper()
	manifest := project.Manifest{Project: "demo", Delivery: project.DeliveryPolicy{Posture: "no-mistakes"}, Verification: project.Verification{Fast: []project.Command{fast}}}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	_, path, _ := project.LoadForCheckout(h.Data, checkout)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return manifest
}

// Issue 2146: a brief whose mode is narrower than its project's posture and
// assessment select starts automatically, or from the board, only under the
// exception the registered primary CEO approved for it. Without one the start
// waits and says how to approve it, so it never freezes no-mistakes ahead of
// the CEO's explicit spawn bringing the exception.
func TestAStartOfANarrowerModeWaitsForTheExceptionThePrimaryCEOApproved(t *testing.T) {
	for _, test := range []struct {
		name, approved string
		hasNewChecks   bool
		want           []string
	}{
		{name: "no exception kept", want: []string{"asks for direct-PR delivery and waits for its approval", "is not authorized", "goblins spawn next-task --project code-goblins --brief", "--delivery-exception"}},
		{name: "an exception for another mode", approved: "local-only", want: []string{"waits for its approval", "approves local-only, not direct-PR"}},
		{name: "project checks changed since approval", approved: "direct-PR", hasNewChecks: true, want: []string{"waits for its approval", "checks changed since the delivery exception kept for next-task was approved"}},
		{name: "an approved exception", approved: "direct-PR"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{output: "spawned next-task harness=claude kind=ship"}
			handler, h := startBoard(t, 5*gigabyte, spawner)
			queueBriefedTask(t, h, "- **next-task** - Ship it", strings.NewReplacer("C:\\dev\\code-goblins", "code-goblins", "mode: no-mistakes", "mode: direct-PR").Replace(plainBrief))
			writeChecks(t, h, "code-goblins", project.Command{"git", "status"})
			if test.approved != "" {
				approveDelivery(t, h, "next-task", "code-goblins", test.approved)
			}
			if test.hasNewChecks {
				writeChecks(t, h, "code-goblins", project.Command{"git", "status", "--short"})
			}

			// Act
			response := postStart(handler, `{"task":"next-task"}`, "board.local", "http://board.local", orderToken)

			// Assert
			if len(test.want) == 0 {
				if response.Code != 202 {
					t.Fatalf("start = %d %s, want 202", response.Code, response.Body)
				}
				waitStarted(t, handler, "next-task")
				if calls := spawner.recorded(); len(calls) != 1 || !slices.Contains(calls[0], "direct-PR") {
					t.Fatalf("goblins spawn ran %q, want once with direct-PR", calls)
				}
				return
			}
			if response.Code != 409 || len(spawner.recorded()) != 0 {
				t.Fatalf("start = %d %s with %d spawns, want 409 and none", response.Code, response.Body, len(spawner.recorded()))
			}
			for _, want := range test.want {
				if !strings.Contains(response.Body.String(), want) {
					t.Errorf("refusal %s; want %q", response.Body, want)
				}
			}
		})
	}
}

// scheduledWaiting runs one scheduling reading, waits for the spawns it
// starts, and returns the work it lists as waiting.
func scheduledWaiting(t *testing.T, handler *HTTP, spawner *spawnRecorder, spawns int) []WaitingWork {
	t.Helper()
	if err := handler.Service.checkFleet(t.Context(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	awaitDispatch(t, handler.Service, spawner, spawns)
	snapshot, err := handler.Service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Scheduling == nil {
		return nil
	}
	return snapshot.Scheduling.Waiting
}

// approvalCommand is the goblins spawn that approves next-task's direct-PR
// delivery on the engine its row names, as the CEO pastes it.
func approvalCommand(brief, engine string) string {
	return "goblins spawn next-task --project code-goblins --brief " + brief + " " + engine + " --mode direct-PR --delivery-exception \"<authority and reason>\""
}

// Issue 2146 on the path that failed it: the supervisor's own scheduling
// reading lists a narrower-mode task nothing approves as waiting, not held,
// with the spawn that approves it on the engine its row names, and starts it
// in that mode once the exception is kept.
func TestTheSchedulerListsANarrowerModeAsWaitingUntilItsExceptionIsKept(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{output: "spawned next-task harness=codex kind=ship"}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	queueBriefedTask(t, h, "- **next-task** - Ship it (harness: codex, model: gpt-6.1-sol, effort: medium)", strings.NewReplacer("C:\\dev\\code-goblins", "code-goblins", "mode: no-mistakes", "mode: direct-PR").Replace(plainBrief))
	writeChecks(t, h, "code-goblins", project.Command{"git", "status"})
	brief := filepath.Join(h.Data, "next-task", "brief.md")

	// Act
	waiting := scheduledWaiting(t, handler, spawner, 0)
	waitingSpawns := len(spawner.recorded())
	approveDelivery(t, h, "next-task", "code-goblins", "direct-PR")
	scheduledWaiting(t, handler, spawner, 1)

	// Assert
	if waitingSpawns != 0 || len(waiting) != 1 || waiting[0].ID != "next-task" {
		t.Fatalf("with no exception kept: %d spawns, waiting %+v; want none and next-task waiting", waitingSpawns, waiting)
	}
	want := approvalCommand(brief, "--harness codex --model gpt-6.1-sol --effort medium")
	if why := waiting[0].Why; !strings.Contains(why, "waits for its approval") || !strings.Contains(why, want) || strings.Contains(why, "--brief-revision") {
		t.Errorf("waiting because %q; want its approval named as %q", why, want)
	}
	calls := spawner.recorded()
	if len(calls) != 1 || !slices.Contains(calls[0], "direct-PR") || !slices.Contains(calls[0], "gpt-6.1-sol") {
		t.Fatalf("goblins spawn ran %q, want once with direct-PR on the row's engine", calls)
	}
}

// A queued row asking for a narrower mode with no brief gets its brief
// written from the row, as a real start writes it, and waits naming that
// brief and the spawn that approves it; once that approval is kept, it starts
// in the row's mode.
func TestARowOnlyNarrowerModeGetsItsBriefWrittenAndWaitsForItsApproval(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{output: "spawned next-task harness=codex kind=ship"}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n- **next-task** - Improve search (repo: code-goblins, harness: codex, mode: direct-PR)\n  Search by project name.\n")
	writeChecks(t, h, "code-goblins", project.Command{"git", "status"})
	brief := filepath.Join(h.Data, "next-task", "brief.md")

	// Act
	waiting := scheduledWaiting(t, handler, spawner, 0)
	waitingSpawns := len(spawner.recorded())
	written, err := os.ReadFile(brief)
	if err != nil {
		t.Fatalf("no brief written from the queued row: %v", err)
	}
	approveDelivery(t, h, "next-task", "code-goblins", "direct-PR")
	scheduledWaiting(t, handler, spawner, 1)

	// Assert
	if !strings.Contains(string(written), "Improve search") || !strings.Contains(string(written), "Search by project name.") || briefSettings(brief)["mode"] != "direct-PR" {
		t.Errorf("brief written:\n%s\nwant the row's title, detail and direct-PR", written)
	}
	if waitingSpawns != 0 || len(waiting) != 1 || waiting[0].ID != "next-task" {
		t.Fatalf("with no exception kept: %d spawns, waiting %+v; want none and next-task waiting", waitingSpawns, waiting)
	}
	if want := approvalCommand(brief, "--harness codex"); !strings.Contains(waiting[0].Why, want) {
		t.Errorf("waiting because %q; want its approval named as %q", waiting[0].Why, want)
	}
	calls := spawner.recorded()
	if len(calls) != 1 || !slices.Contains(calls[0], "direct-PR") || !slices.Contains(calls[0], brief) {
		t.Fatalf("goblins spawn ran %q, want once with direct-PR from the written brief", calls)
	}
}
