package supervisor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// acceptPackage saves the dependency's acceptance report and installation
// receipt and accepts its delivery against a synthetic installation.
func (fixture *preparedFixture) acceptPackage(t *testing.T) {
	t.Helper()
	directory := filepath.Join(fixture.home.Data, "package-delivery")
	installed := filepath.Join(fixture.home.Root, "tool.exe")
	digest := strings.Repeat("c", 64)
	started := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	receipt, err := json.Marshal(state.InstallationReceipt{Task: "package-delivery", Generation: "generation-package", Image: state.InstalledImage{Path: installed, SHA256: digest}, Daemon: state.InstalledDaemon{PID: 7, Started: started}})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, "report.md"), "Installed and verified.\n")
	writeFile(t, filepath.Join(directory, "receipt.json"), string(receipt))
	installation := fleet.Installation{
		Hash:        func(string) (string, error) { return digest, nil },
		DaemonImage: func(int, time.Time) (string, error) { return installed, nil },
	}
	if _, err := fleet.AcceptDelivery(fixture.home, fleet.Acceptance{ID: "package-delivery", Generation: "generation-package", Report: filepath.Join(directory, "report.md"), Receipt: filepath.Join(directory, "receipt.json"), AcceptedBy: "registered-ceo"}, installation); err != nil {
		t.Fatal(err)
	}
}

// retirePackage cleans the dependency up as goblins cleanup records one that
// opened no pull request.
func (fixture *preparedFixture) retirePackage(t *testing.T) state.Outcome {
	t.Helper()
	retired := state.Outcome{ID: "package-delivery", Generation: "generation-package", Phase: "stopped", Reason: "Worktree returned by cleanup", At: fixture.now}
	if err := state.WriteOutcome(fixture.home.State, retired); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(state.TaskMetaPath(fixture.home.State, "package-delivery")); err != nil {
		t.Fatal(err)
	}
	return retired
}

// Issue 2157: an installation task accepted as delivered, then retired by
// cleanup as stopped, left every task that waited on it paused for good.
func TestAnAcceptedDeliveryResumesAPausedDependentOnceAndKeepsItsStoppedRetirement(t *testing.T) {
	for _, isAcceptedBeforeCleanup := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted after cleanup", true: "accepted before cleanup"}[isAcceptedBeforeCleanup], func(t *testing.T) {
			// Arrange
			fixture := preparedPackage(t)
			fixture.reconcile(t)
			var retired state.Outcome
			schedule := func() {
				if _, err := fixture.service.schedule(t.Context(), fixture.now, &fleetWakes{}, Memory{Available: 8 * gigabyte, CommitAvailable: 8 * gigabyte}, Room{Floor: 4 * gigabyte}, ""); err != nil {
					t.Fatal(err)
				}
			}
			if isAcceptedBeforeCleanup {
				fixture.acceptPackage(t)
				retired = fixture.retirePackage(t)
			} else {
				retired = fixture.retirePackage(t)
				fixture.reconcile(t)
				schedule()
				if calls := fixture.spawner.recorded(); len(calls) != 1 || calls[0][0] != "pause" {
					t.Fatalf("a cleaned up dependency resumed before its delivery was accepted: %v", calls)
				}
				fixture.acceptPackage(t)
			}

			// Act
			for scan := 0; scan < 2; scan++ {
				schedule()
				awaitDispatch(t, fixture.service, fixture.spawner, 2)
			}

			// Assert
			if calls := fixture.spawner.recorded(); len(calls) != 2 || calls[1][0] != "resume" || valueAfter(calls[1], "--operation") != "resume-"+fixture.record.Operation {
				t.Fatalf("calls=%v", calls)
			}
			if outcome, err := state.ReadOutcome(fixture.home.State, "package-delivery"); err != nil || outcome != retired {
				t.Fatalf("acceptance rewrote the retirement fact: %+v %v", outcome, err)
			}
		})
	}
}

func TestAQueuedRowWaitClearsOnlyOnAnAcceptedDelivery(t *testing.T) {
	// Arrange
	fixture := preparedPackage(t)
	fixture.retirePackage(t)
	writeFile(t, filepath.Join(fixture.home.Data, "package-delivery", "report.md"), "Installed and verified.\n")
	writeFile(t, filepath.Join(fixture.home.Data, "backlog.md"), "## Queued\n- [ ] next-task - Use the tool blocked-by: package-delivery\n\n## Done\n- [x] package-delivery - Installed and verified, delivery accepted (done 2026-11-26)\n")
	waits := func() []fleet.Blocker {
		backlog, err := fleet.ReadBacklog(fixture.home)
		if err != nil || len(backlog.Queued) != 1 {
			t.Fatalf("backlog=%+v %v", backlog.Queued, err)
		}
		return rowWaits(fixture.home, backlog, backlog.Queued[0], rowReading{now: fixture.now})
	}

	// Act
	before := waits()
	fixture.acceptPackage(t)
	after := waits()

	// Assert
	if len(before) != 1 || before[0].Problem != "package-delivery stopped without delivering" {
		t.Fatalf("a report, a Done row and cleanup cleared or hid the wait: %+v", before)
	}
	if len(after) != 0 {
		t.Fatalf("an accepted delivery left the row waiting: %+v", after)
	}
}

func TestAnUnreadableAcceptedDeliveryStaysVisibleAndClearsNothing(t *testing.T) {
	// Arrange
	fixture := preparedPackage(t)
	fixture.retirePackage(t)
	writeFile(t, filepath.Join(fixture.home.State, "accepted-deliveries", "package-delivery.json"), "{")
	writeFile(t, filepath.Join(fixture.home.Data, "backlog.md"), "## Queued\n- [ ] next-task - Use the tool blocked-by: package-delivery\n")
	backlog, err := fleet.ReadBacklog(fixture.home)
	if err != nil || len(backlog.Queued) != 1 {
		t.Fatalf("backlog=%+v %v", backlog.Queued, err)
	}

	// Act
	waits := rowWaits(fixture.home, backlog, backlog.Queued[0], rowReading{now: fixture.now})
	isCleared, pauseErr := pauseClearedHere(fixture.home.State, fixture.record.Condition, fixture.now)

	// Assert
	if len(waits) != 1 || !strings.Contains(waits[0].Problem, "package-delivery") || !strings.Contains(waits[0].Problem, "unexpected end of JSON input") {
		t.Fatalf("waits=%+v", waits)
	}
	if isCleared || pauseErr == nil {
		t.Fatalf("pause cleared=%v err=%v", isCleared, pauseErr)
	}
}

func TestAKeptQueuedTaskIsReadAgainOnceItsDeliveryIsAccepted(t *testing.T) {
	// Arrange
	fixture := preparedPackage(t)
	fixture.retirePackage(t)
	writeFile(t, filepath.Join(fixture.home.Data, "backlog.md"), "## Queued\n- [ ] package-delivery - Install the tool\n")
	before, beforeErr := fixture.service.queuedTask("package-delivery")
	fixture.acceptPackage(t)

	// Act
	_, err := fixture.service.queuedTask("package-delivery")

	// Assert
	if beforeErr != nil || before.Row.ID != "package-delivery" {
		t.Fatalf("cleanup alone took the row off the queue: %+v %v", before, beforeErr)
	}
	if !errors.Is(err, fleet.ErrNotQueued) {
		t.Fatalf("the board kept an accepted delivery's row queued: %v", err)
	}
}

func TestTheSupervisorClosesAnAcceptedDeliverysRowLeftUnderQueued(t *testing.T) {
	// Arrange
	fixture := preparedPackage(t)
	fixture.retirePackage(t)
	backlog := filepath.Join(fixture.home.Data, "backlog.md")
	writeFile(t, backlog, "## Queued\n- [ ] package-delivery - Install the tool\n\n## Done\n")
	fixture.acceptPackage(t)

	// Act
	err := fixture.service.closeDeliveredRows()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(backlog); err != nil || !strings.Contains(string(data), "## Done\n- [x] package-delivery - Install the tool (done ") {
		t.Fatalf("backlog=%s %v", data, err)
	}
}
