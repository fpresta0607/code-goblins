package fleet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

var (
	installedDigest = strings.Repeat("a", 64)
	daemonStart     = time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
)

// acceptanceFixture is a home with one task that installed a program, its
// saved acceptance report and installation receipt, and a synthetic machine on
// which the receipt holds.
type acceptanceFixture struct {
	home         home.Home
	receipt      state.InstallationReceipt
	request      Acceptance
	installation Installation
}

func newAcceptanceFixture(t *testing.T) *acceptanceFixture {
	t.Helper()
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	for _, directory := range []string{h.State, filepath.Join(h.Data, "install")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	installed := filepath.Join(root, "bin", "tool.exe")
	running := filepath.Join(root, "bin", "tool.exe.running")
	fixture := &acceptanceFixture{
		home: h,
		receipt: state.InstallationReceipt{
			Task:       "install",
			Generation: "s2",
			Image:      state.InstalledImage{Path: installed, SHA256: installedDigest},
			Daemon:     state.InstalledDaemon{PID: 4242, Started: daemonStart},
		},
		request: Acceptance{ID: "install", Generation: "s2", Report: filepath.Join(h.Data, "install", "report.md"), Receipt: filepath.Join(h.Data, "install", "receipt.json"), AcceptedBy: "registered-ceo"},
		installation: Installation{
			Hash: func(path string) (string, error) {
				if path == installed || path == running {
					return installedDigest, nil
				}
				return "", os.ErrNotExist
			},
			DaemonImage: func(pid int, started time.Time) (string, error) {
				if pid == 4242 && started.Equal(daemonStart) {
					return running, nil
				}
				return "", errors.New("no such process")
			},
		},
	}
	if err := os.WriteFile(fixture.request.Report, []byte("Installed and verified.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.writeReceipt(t)
	return fixture
}

func (fixture *acceptanceFixture) writeReceipt(t *testing.T) {
	t.Helper()
	data, err := json.Marshal(fixture.receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.request.Receipt, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (fixture *acceptanceFixture) retire(t *testing.T) state.Outcome {
	t.Helper()
	outcome := state.Outcome{ID: "install", Generation: "s2", Phase: "stopped", Reason: "Worktree returned by cleanup", At: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)}
	if err := state.WriteOutcome(fixture.home.State, outcome); err != nil {
		t.Fatal(err)
	}
	return outcome
}

func (fixture *acceptanceFixture) live(t *testing.T, generation string) {
	t.Helper()
	if err := state.WriteTaskMeta(fixture.home.State, state.TaskMeta{ID: "install", SpawnGen: generation}); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptDeliveryRecordsOneExactGenerationAndKeepsItsRetirement(t *testing.T) {
	for _, isRetired := range []bool{false, true} {
		t.Run(map[bool]string{false: "before cleanup", true: "after cleanup"}[isRetired], func(t *testing.T) {
			// Arrange
			fixture := newAcceptanceFixture(t)
			var retired state.Outcome
			if isRetired {
				retired = fixture.retire(t)
			} else {
				fixture.live(t, "s2")
			}
			if _, isDelivered, err := state.DeliveredAt(fixture.home.State, "install"); err != nil || isDelivered {
				t.Fatalf("report and receipt alone delivered the task: %v %v", isDelivered, err)
			}

			// Act
			isNew, err := AcceptDelivery(fixture.home, fixture.request, fixture.installation)

			// Assert
			if err != nil || !isNew {
				t.Fatalf("AcceptDelivery = %v, %v", isNew, err)
			}
			if at, isDelivered, err := state.DeliveredAt(fixture.home.State, "install"); err != nil || !isDelivered || at.IsZero() {
				t.Fatalf("accepted delivery reads %v %v %v", at, isDelivered, err)
			}
			digest := sha256.Sum256([]byte("Installed and verified.\n"))
			record, err := state.ReadAcceptedDelivery(fixture.home.State, "install")
			if err != nil || record.ReportSHA256 != hex.EncodeToString(digest[:]) || record.Receipt != fixture.receipt || record.AcceptedBy != "registered-ceo" {
				t.Fatalf("record=%+v %v", record, err)
			}
			outcome, err := state.ReadOutcome(fixture.home.State, "install")
			if isRetired && (err != nil || outcome != retired) || !isRetired && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("acceptance changed the retirement fact: %+v %v", outcome, err)
			}
		})
	}
}

func TestAcceptDeliveryRefusesEvidenceThatDoesNotHold(t *testing.T) {
	for _, test := range []struct {
		name, want string
		change     func(*testing.T, *acceptanceFixture)
	}{
		{"old generation", "is at generation s2", func(t *testing.T, f *acceptanceFixture) {
			f.request.Generation, f.receipt.Generation = "s1", "s1"
			f.writeReceipt(t)
		}},
		{"receipt of an old generation", "receipt names", func(t *testing.T, f *acceptanceFixture) {
			f.receipt.Generation = "s1"
			f.writeReceipt(t)
		}},
		{"receipt of another task", "receipt names", func(t *testing.T, f *acceptanceFixture) {
			f.receipt.Task = "other"
			f.writeReceipt(t)
		}},
		{"installed program differs", "installed program", func(t *testing.T, f *acceptanceFixture) {
			f.receipt.Image.SHA256 = strings.Repeat("b", 64)
			f.writeReceipt(t)
		}},
		{"installed program unreadable", "installed program", func(t *testing.T, f *acceptanceFixture) {
			f.receipt.Image.Path += ".missing"
			f.writeReceipt(t)
		}},
		{"daemon gone", "daemon", func(t *testing.T, f *acceptanceFixture) {
			f.receipt.Daemon.PID = 1
			f.writeReceipt(t)
		}},
		{"daemon runs another program", "daemon", func(t *testing.T, f *acceptanceFixture) {
			hash := f.installation.Hash
			f.installation.Hash = func(path string) (string, error) {
				if strings.HasSuffix(path, ".running") {
					return strings.Repeat("b", 64), nil
				}
				return hash(path)
			}
		}},
		{"receipt with an unknown field", "unknown field", func(t *testing.T, f *acceptanceFixture) {
			writeFileAt(t, f.request.Receipt, `{"task":"install","accepted":true}`)
		}},
		{"receipt missing its daemon", "daemon", func(t *testing.T, f *acceptanceFixture) {
			f.receipt.Daemon = state.InstalledDaemon{}
			f.writeReceipt(t)
		}},
		{"no report", "report", func(t *testing.T, f *acceptanceFixture) {
			if err := os.Remove(f.request.Report); err != nil {
				t.Fatal(err)
			}
		}},
		{"empty report", "report", func(t *testing.T, f *acceptanceFixture) {
			writeFileAt(t, f.request.Report, "")
		}},
		{"report outside the home's data", "data folder", func(t *testing.T, f *acceptanceFixture) {
			f.request.Report = filepath.Join(f.home.Root, "report.md")
			writeFileAt(t, f.request.Report, "Installed.")
		}},
		{"unknown task", "neither", func(t *testing.T, f *acceptanceFixture) {
			if err := os.Remove(state.TaskMetaPath(f.home.State, "install")); err != nil {
				t.Fatal(err)
			}
		}},
		{"unreadable retirement outcome", "outcome", func(t *testing.T, f *acceptanceFixture) {
			writeFileAt(t, filepath.Join(f.home.State, "outcomes", "install.json"), "{")
		}},
		{"no approver", "accepted", func(t *testing.T, f *acceptanceFixture) {
			f.request.AcceptedBy = ""
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			fixture := newAcceptanceFixture(t)
			fixture.live(t, "s2")
			test.change(t, fixture)

			// Act
			isNew, err := AcceptDelivery(fixture.home, fixture.request, fixture.installation)

			// Assert
			if err == nil || isNew || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("AcceptDelivery = %v, %v; want a refusal naming %q", isNew, err, test.want)
			}
			if _, err := state.ReadAcceptedDelivery(fixture.home.State, "install"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a refused acceptance left a record: %v", err)
			}
		})
	}
}

func TestAcceptDeliveryReplaysOnceAndKeepsContradictoryEvidenceVisible(t *testing.T) {
	// Arrange
	fixture := newAcceptanceFixture(t)
	fixture.retire(t)
	if _, err := AcceptDelivery(fixture.home, fixture.request, fixture.installation); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(fixture.home.State, "accepted-deliveries", "install.json")
	accepted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The machine has moved on since, as a later install and restart leave it.
	moved := Installation{
		Hash:        func(string) (string, error) { return "", errors.New("replaced") },
		DaemonImage: func(int, time.Time) (string, error) { return "", errors.New("restarted") },
	}

	// Act
	isNew, replayErr := AcceptDelivery(fixture.home, fixture.request, moved)
	writeFileAt(t, fixture.request.Report, "A different report.\n")
	_, reportErr := AcceptDelivery(fixture.home, fixture.request, fixture.installation)
	writeFileAt(t, fixture.request.Report, "Installed and verified.\n")
	fixture.receipt.Daemon.PID = 4243
	fixture.writeReceipt(t)
	_, receiptErr := AcceptDelivery(fixture.home, fixture.request, fixture.installation)

	// Assert
	if replayErr != nil || isNew {
		t.Fatalf("replay = %v, %v", isNew, replayErr)
	}
	for _, err := range []error{reportErr, receiptErr} {
		if err == nil || !strings.Contains(err.Error(), "contradicts") {
			t.Fatalf("contradictory evidence = %v", err)
		}
	}
	if after, err := os.ReadFile(path); err != nil || string(after) != string(accepted) {
		t.Fatalf("replay or contradiction rewrote the accepted delivery: %s %v", after, err)
	}
}

func TestAcceptDeliveryKeepsAnUnreadableRecordAsItIs(t *testing.T) {
	// Arrange
	fixture := newAcceptanceFixture(t)
	fixture.retire(t)
	path := filepath.Join(fixture.home.State, "accepted-deliveries", "install.json")
	writeFileAt(t, path, "{")

	// Act
	_, err := AcceptDelivery(fixture.home, fixture.request, fixture.installation)
	_, isDelivered, readErr := state.DeliveredAt(fixture.home.State, "install")

	// Assert
	if err == nil || readErr == nil || isDelivered {
		t.Fatalf("accept=%v read=%v delivered=%v; want both to report the unreadable record", err, readErr, isDelivered)
	}
	if after, err := os.ReadFile(path); err != nil || string(after) != "{" {
		t.Fatalf("the unreadable record changed: %s %v", after, err)
	}
}

func TestAnAcceptedDeliveryCountsOnlyForItsOwnGeneration(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*testing.T, *acceptanceFixture)
	}{
		{"a new session under the id", func(t *testing.T, f *acceptanceFixture) { f.live(t, "s3") }},
		{"a later retirement under the id", func(t *testing.T, f *acceptanceFixture) {
			if err := os.Remove(state.TaskMetaPath(f.home.State, "install")); err != nil {
				t.Fatal(err)
			}
			if err := state.WriteOutcome(f.home.State, state.Outcome{ID: "install", Generation: "s3", Phase: "stopped"}); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			fixture := newAcceptanceFixture(t)
			fixture.live(t, "s2")
			if _, err := AcceptDelivery(fixture.home, fixture.request, fixture.installation); err != nil {
				t.Fatal(err)
			}
			test.change(t, fixture)

			// Act
			_, isDelivered, err := state.DeliveredAt(fixture.home.State, "install")

			// Assert
			if err != nil || isDelivered {
				t.Fatalf("an old generation's acceptance delivered: %v %v", isDelivered, err)
			}
		})
	}
}

func TestCompleteQueuedTaskClosesAnAcceptedRetiredRow(t *testing.T) {
	// Arrange
	fixture := newAcceptanceFixture(t)
	retired := fixture.retire(t)
	writeFileAt(t, filepath.Join(fixture.home.Data, "backlog.md"), "## Queued\n- [ ] install - Install the tool\n\n## Done\n")
	if _, err := AcceptDelivery(fixture.home, fixture.request, fixture.installation); err != nil {
		t.Fatal(err)
	}
	accepted, err := state.ReadAcceptedDelivery(fixture.home.State, "install")
	if err != nil {
		t.Fatal(err)
	}

	// Act
	err = CompleteQueuedTask(fixture.home, "install")

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := "- [x] install - Install the tool (done " + accepted.At.Format("2006-01-02") + ")"
	if data, err := os.ReadFile(filepath.Join(fixture.home.Data, "backlog.md")); err != nil || !strings.Contains(string(data), "## Done\n"+want) {
		t.Fatalf("backlog = %s %v; want %q under Done", data, err, want)
	}
	if outcome, err := state.ReadOutcome(fixture.home.State, "install"); err != nil || outcome != retired {
		t.Fatalf("closing the row changed the retirement fact: %+v %v", outcome, err)
	}
}

func writeFileAt(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
