package janitor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// observed writes the monitor's record of a task as it reads one at its
// prompt or in a turn, last seen making progress at progress.
func observed(t *testing.T, stateDir, id string, health monitor.Health, reason monitor.Reason, progress time.Time) {
	t.Helper()
	observation := monitor.Observation{
		Schema:          monitor.Schema,
		TaskID:          id,
		Endpoint:        ":",
		EndpointVerdict: monitor.ProbePresent,
		Digest:          "screen-digest",
		LastObserved:    progress.Add(2 * time.Hour),
		LastSeen:        progress.Add(2 * time.Hour),
		LastProgress:    progress,
		Health:          health,
		Reason:          reason,
	}
	if health == monitor.HealthStale {
		since, next := progress.Add(30*time.Minute), progress.Add(3*time.Hour)
		observation.StaleSince, observation.NextEscalation = &since, &next
	}
	if err := monitor.WriteObservation(stateDir, observation); err != nil {
		t.Fatalf("write the monitor's record: %v", err)
	}
}

// Nothing of a goblin that works is idle, so the sweep has to know a goblin
// that rests from one that works or waits, and err on the side of work. A
// goblin rests when the last outcome it reported is done or failed and the
// monitor reads it at its prompt with no turn in progress and nothing asked.
// Everything else, and everything that cannot be read, is a goblin at work.
func TestAGoblinRestsOnlyOnceItDeliveredAndSitsAtItsPrompt(t *testing.T) {
	progress := time.Date(2026, 10, 10, 3, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name    string
		reports []string
		health  monitor.Health
		reason  monitor.Reason
		want    time.Time
	}{
		{name: "it reported done and sits at its prompt", reports: []string{"working: the fix", "done: PR https://example.test/pull/1"}, health: monitor.HealthIdle, reason: monitor.None, want: progress},
		{name: "it reported failed and has sat long enough to be stale", reports: []string{"failed: the build broke"}, health: monitor.HealthStale, reason: monitor.AwaitingAnswer, want: progress},
		{name: "it reported done and nothing changed on its screen since", reports: []string{"done: PR https://example.test/pull/1"}, health: monitor.HealthStale, reason: monitor.UnchangedIdle, want: progress},
		{name: "the CFO wrote into its log after its done", reports: []string{"done: PR https://example.test/pull/1", "notify-handled: done"}, health: monitor.HealthIdle, reason: monitor.None, want: progress},
		{name: "it reported done and is in a turn again", reports: []string{"done: PR https://example.test/pull/1"}, health: monitor.HealthBusy, reason: monitor.None},
		{name: "it reported done and its turn has run very long", reports: []string{"done: PR https://example.test/pull/1"}, health: monitor.HealthStale, reason: monitor.BusyTurnOverAge},
		{name: "it reported done and asks the CFO something in prose", reports: []string{"done: PR https://example.test/pull/1"}, health: monitor.HealthStale, reason: monitor.GoblinAsks},
		{name: "it reported working and sits at its prompt", reports: []string{"working: waiting for a test run"}, health: monitor.HealthIdle, reason: monitor.None},
		{name: "it waits on a gate run", reports: []string{"done: PR https://example.test/pull/1", "waiting-on: run-42 the gate"}, health: monitor.HealthIdle, reason: monitor.None},
		{name: "it waits on the CFO's answer", reports: []string{"blocked: which base branch"}, health: monitor.HealthIdle, reason: monitor.None},
		{name: "it has reported nothing", health: monitor.HealthIdle, reason: monitor.None},
		{name: "the monitor has no record of it", reports: []string{"done: PR https://example.test/pull/1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			for _, report := range test.reports {
				if err := state.AppendStatus(stateDir, "g1", report); err != nil {
					t.Fatal(err)
				}
			}
			if test.health != "" {
				observed(t, stateDir, "g1", test.health, test.reason, progress)
			}

			// Act
			got := atRestSince(stateDir, "g1")

			// Assert
			if !got.Equal(test.want) {
				t.Errorf("at rest since %s, want %s (zero is a goblin at work)", got, test.want)
			}
		})
	}
}

// The sweep's reader takes a task's rest from its record in the home, and
// gives none to a terminal that is no task's, as the CFO's is: the CFO works
// for as long as its terminal runs.
func TestTheSweepsReaderGivesRestOnlyToATaskThatRests(t *testing.T) {
	// Arrange
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state")}
	progress := time.Date(2026, 10, 10, 3, 0, 0, 0, time.UTC)
	project := filepath.Join(root, "app")
	for _, id := range []string{"g1", "cfo"} {
		// A proof on its ledger is what makes a terminal one the sweep judges.
		if err := os.MkdirAll(filepath.Join(h.State, "hosts"), 0o755); err != nil {
			t.Fatal(err)
		}
		ledger := host.ProofSum("proof-of-"+id) + " " + time.Now().UTC().Format(time.RFC3339Nano) + "\n"
		if err := os.WriteFile(filepath.Join(h.State, "hosts", id+".proofs"), []byte(ledger), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := state.AppendStatus(h.State, id, "done: PR https://example.test/pull/1"); err != nil {
			t.Fatal(err)
		}
		observed(t, h.State, id, monitor.HealthIdle, monitor.None, progress)
	}
	meta := state.TaskMeta{ID: "g1", Backend: "native", Project: project, Worktree: filepath.Join(root, "worktrees", "app", "g1"), TaskTmp: filepath.Join(h.State, "tasktmp", "g1")}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}

	// Act
	owners, notes := ReadOwners(h)

	// Assert
	rest := map[string]time.Time{}
	for _, owner := range owners {
		rest[owner.ID] = owner.AtRestSince
	}
	if len(owners) != 2 || !rest["g1"].Equal(progress) || !rest["cfo"].IsZero() {
		t.Errorf("the reader read %v with notes %v, want task g1 at rest since %s and terminal cfo, which is no task's, at work", rest, notes, progress)
	}
}
