package cleanup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// Issue 2157: cleanup retires a task that installed something and opened no
// pull request as stopped, which is true of its retirement and says nothing of
// whether its delivery was accepted.
func TestCleanupKeepsAnAcceptedDeliveryBesideItsStoppedRetirement(t *testing.T) {
	for _, isAccepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "report and receipt alone", true: "accepted before cleanup"}[isAccepted], func(t *testing.T) {
			// Arrange
			fixture := newCleanupFixture(t)
			data := t.TempDir()
			fixture.service.Data = data
			fixture.meta.SpawnGen = "s1"
			if err := state.WriteTaskMeta(fixture.stateDir, fixture.meta); err != nil {
				t.Fatal(err)
			}
			installed := filepath.Join(data, "g1", "tool.exe")
			digest := strings.Repeat("d", 64)
			receipt, err := json.Marshal(state.InstallationReceipt{Task: "g1", Generation: "s1", Image: state.InstalledImage{Path: installed, SHA256: digest}, Daemon: state.InstalledDaemon{PID: 7, Started: time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)}})
			if err != nil {
				t.Fatal(err)
			}
			for name, content := range map[string]string{"g1/report.md": "Installed and verified.\n", "g1/receipt.json": string(receipt), "backlog.md": "## Queued\n- [ ] g1 - Install the tool\n\n## Done\n"} {
				path := filepath.Join(data, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if isAccepted {
				request := fleet.Acceptance{ID: "g1", Generation: "s1", Report: filepath.Join(data, "g1", "report.md"), Receipt: filepath.Join(data, "g1", "receipt.json"), AcceptedBy: "registered-ceo"}
				installation := fleet.Installation{
					Hash:        func(string) (string, error) { return digest, nil },
					DaemonImage: func(int, time.Time) (string, error) { return installed, nil },
				}
				if _, err := fleet.AcceptDelivery(home.Home{State: fixture.stateDir, Data: data}, request, installation); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			_, err = fixture.service.Cleanup(context.Background(), "g1")

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			outcome, err := state.ReadOutcome(fixture.stateDir, "g1")
			if err != nil || outcome.Phase != "stopped" || outcome.Reason != "Worktree returned by cleanup" || outcome.Evidence != "" {
				t.Fatalf("outcome=%+v %v; want the truthful stopped retirement", outcome, err)
			}
			_, isDelivered, err := state.DeliveredAt(fixture.stateDir, "g1")
			if err != nil || isDelivered != isAccepted {
				t.Fatalf("delivered=%v %v, want %v", isDelivered, err, isAccepted)
			}
			ending := map[bool]string{false: " (retired ", true: " (done "}[isAccepted]
			if backlog, err := os.ReadFile(filepath.Join(data, "backlog.md")); err != nil || !strings.Contains(string(backlog), "- [x] g1 - Install the tool"+ending) {
				t.Fatalf("backlog=%s %v; want the row closed as%s", backlog, err, ending)
			}
		})
	}
}
