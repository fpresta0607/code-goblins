package janitor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
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
			got := AtRestSince(stateDir, "g1")

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
	owners, notes := ReadOwners(t.Context(), h, &fleettree.Reader{Home: t.TempDir()})

	// Assert
	rest := map[string]time.Time{}
	for _, owner := range owners {
		rest[owner.ID] = owner.AtRestSince
	}
	if len(owners) != 2 || !rest["g1"].Equal(progress) || !rest["cfo"].IsZero() {
		t.Errorf("the reader read %v with notes %v, want task g1 at rest since %s and terminal cfo, which is no task's, at work", rest, notes, progress)
	}
}

// A done report is one pull request's, and a task can ship several under one
// id: a goblin that reported one done and went on to the next waits on its
// own work, as on a run it started in the machine's heavy line with cfo gate
// turn --detach, which uses no processor while it waits in line (#876). The
// sweep keeps what such a goblin left detached, and still ends what a goblin
// whose work is finished left: an idle detached dev server past the hour. A
// goblin whose tree cannot be read may wait on what was not read, and keeps
// what it left.
func TestTheSweepKeepsTheHeavyRunOfAGoblinThatShippedOneOfSeveralPullRequests(t *testing.T) {
	now := time.Now().UTC()
	launched := now.Add(-3 * time.Hour)
	mark := lifecycle.Mark{Terminal: "g1", ProofSum: host.ProofSum("proof-of-g1")}
	heavyRun := fleettree.Process{PID: 300, ParentPID: 904, Exe: "goblins.exe", Created: 30, Started: now.Add(-50 * time.Minute)}
	devServer := fleettree.Process{PID: 310, ParentPID: 905, Exe: "node.exe", Created: 31, Started: launched.Add(10 * time.Minute)}
	commands := map[int]string{
		300: `C:\dev\code-goblins\bin\goblins.exe gate turn --detach C:\scratch\test.log -- go test ./...`,
		310: `node node_modules/vite/bin/vite.js --port 5173`,
	}
	for _, test := range []struct {
		name         string
		left         fleettree.Process
		isUnreadable bool
		want         planned
	}{
		{name: "it waits on its run in the heavy line", left: heavyRun, want: planned{Watching: []int{300}}},
		{name: "its work is finished and its dev server sits idle", left: devServer, want: planned{Ending: []int{310}}},
		{name: "its processes cannot be read", left: devServer, isUnreadable: true, want: planned{Watching: []int{310}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			h := home.Home{Root: root, State: filepath.Join(root, "state")}
			if err := os.MkdirAll(filepath.Join(h.State, "hosts"), 0o755); err != nil {
				t.Fatal(err)
			}
			ledger := mark.ProofSum + " " + now.Format(time.RFC3339Nano) + "\n"
			if err := os.WriteFile(filepath.Join(h.State, "hosts", "g1.proofs"), []byte(ledger), 0o600); err != nil {
				t.Fatal(err)
			}
			// This test's own process stands in for the terminal's host, so
			// the terminal runs.
			record, err := json.Marshal(host.Record{ID: "g1", Pipe: "pipe", Token: "token", HostPID: os.Getpid(), ChildPID: 100, Started: now})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(h.State, "hosts", "g1.json"), record, 0o600); err != nil {
				t.Fatal(err)
			}
			meta := state.TaskMeta{ID: "g1", Backend: "native", Harness: "pi", Project: filepath.Join(root, "app"), Worktree: filepath.Join(root, "worktrees", "app", "g1"), TaskTmp: filepath.Join(h.State, "tasktmp", "g1")}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			if err := state.AppendStatus(h.State, "g1", "done: PR https://example.test/pull/1"); err != nil {
				t.Fatal(err)
			}
			observed(t, h.State, "g1", monitor.HealthIdle, monitor.None, launched.Add(time.Hour))
			tree := &fleettree.Reader{
				Home: t.TempDir(),
				Processes: func() ([]fleettree.Process, error) {
					if test.isUnreadable {
						return nil, errors.New("access denied")
					}
					return []fleettree.Process{{PID: 100, ParentPID: 1, Exe: "pi.exe", Created: 1, Started: launched}, test.left}, nil
				},
				Listeners:   func() (map[int][]int, error) { return map[int][]int{310: {5173}}, nil },
				CommandLine: func(pid int) (string, error) { return commands[pid], nil },
				OutputFile:  func(int, time.Time) (string, error) { return "", os.ErrNotExist },
				Owner:       func(int) string { return "g1" },
				Console:     func(int) (int, error) { return 0, errors.New("no console") },
				Now:         func() time.Time { return now },
			}
			left := Process{Process: lifecycle.Process{PID: test.left.PID, ParentPID: test.left.ParentPID, Name: test.left.Exe, Started: test.left.Started, Mark: mark}}
			// An earlier sweep saw it two hours ago, and it has done nothing
			// since.
			watched := []Watched{{PID: left.PID, Started: left.Started, Since: now.Add(-2 * time.Hour), Members: []WatchedMember{{PID: left.PID, Started: left.Started}}}}
			cfg := Config{
				Home:      h,
				Now:       now,
				Processes: func(context.Context) ([]Process, error) { return []Process{left}, nil },
				Owners:    func() ([]Owner, []string) { return ReadOwners(t.Context(), h, tree) },
				Watched:   watched,
			}

			// Act
			plan, err := cfg.PlanProcesses(t.Context())

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Owners) != 1 || plan.Owners[0].HostPID == 0 {
				t.Fatalf("the sweep read terminals %+v with notes %v, want g1 running", plan.Owners, plan.Notes)
			}
			got := planned{Ending: pidsOf(plan.Ending), Left: pidsOf(plan.Left)}
			for _, watch := range plan.Watching {
				got.Watching = append(got.Watching, watch.PID)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("plan = %+v, want %+v (g1 at rest since %s)", got, test.want, plan.Owners[0].AtRestSince)
			}
		})
	}
}
