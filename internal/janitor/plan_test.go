package janitor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
)

// A plan is asked for before a build that ends processes is installed, so it
// has to be the sweep's own decision and nothing more: it ends no process,
// writes nothing into the home, and lists to end exactly what a sweep with
// the same readers then ends. It also says why it leaves what it leaves,
// which the sweep keeps to itself.
func TestAPlanEndsNothingWritesNothingAndIsWhatTheSweepActsOn(t *testing.T) {
	// Arrange
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state")}
	task := filepath.Join(root, "scratch", "gb-live")
	elsewhere := filepath.Join(root, "elsewhere")
	began := time.Date(2026, 10, 10, 4, 0, 0, 0, time.UTC)
	at := func(seconds int) time.Time { return began.Add(time.Duration(seconds) * time.Second) }
	gone := lifecycle.Mark{Terminal: "gb-gone", ProofSum: "gone-digest"}
	live := lifecycle.Mark{Terminal: "gb-live", ProofSum: "live-digest"}
	process := func(pid, parent int, name string, start time.Time, from lifecycle.Process) Process {
		from.PID, from.ParentPID, from.Name, from.Started = pid, parent, name, start
		return Process{Process: from, Memory: 10 << 20}
	}
	processes := []Process{
		// What a terminal with no host left: ended.
		process(10, 900, "node.exe", at(0), lifecycle.Process{Directory: elsewhere, Mark: gone}),
		// A running terminal's host, its harness and a tool under it: kept.
		process(20, 1, "cfo.exe", at(0), lifecycle.Process{Directory: elsewhere}),
		process(21, 20, "claude.exe", at(1), lifecycle.Process{Directory: task, Mark: live}),
		process(22, 21, "go.exe", at(2), lifecycle.Process{Directory: task, Mark: live}),
		// A server the running terminal left detached: watched.
		process(30, 901, "node.exe", at(3), lifecycle.Process{Directory: elsewhere, Mark: live}),
		// A browser the goblin opened for the Overlord: his.
		process(40, 902, "chrome.exe", at(4), lifecycle.Process{Arguments: []string{`C:\Program Files\Google\Chrome\Application\chrome.exe`}, Mark: live}),
		// A gate's agent that carries the goblin's mark: the gate's. One
		// whose gate is gone is named for the CFO, and said once.
		process(50, 80, "codex.exe", at(9), lifecycle.Process{Directory: elsewhere, Mark: live, IsGateAgent: true}),
		process(51, 903, "codex.exe", at(5), lifecycle.Process{Directory: elsewhere, Mark: live, IsGateAgent: true}),
		// A test the CFO runs in the goblin's folder: the CFO's.
		process(60, 1, "pwsh.exe", at(0), lifecycle.Process{Directory: elsewhere}),
		process(61, 60, "go.exe", at(6), lifecycle.Process{Directory: task}),
		// A bridge nothing ties to an owner: named for the CFO.
		process(70, 904, "node.exe", at(7), lifecycle.Process{Directory: elsewhere, Arguments: []string{"node", `C:\npm\chrome-devtools-axi\dist\bin\chrome-devtools-axi-bridge.js`}}),
		// A program of nobody's: not judged.
		process(80, 1, "notepad.exe", at(8), lifecycle.Process{Directory: elsewhere}),
	}
	owners := []Owner{
		{ID: "gb-gone", Marks: []lifecycle.Mark{gone}},
		{ID: "gb-live", Marks: []lifecycle.Mark{live}, Directories: []string{task}, HostPID: 20},
	}
	var ended []int
	cfg := Config{
		Home:      h,
		Now:       at(600),
		Processes: func(context.Context) ([]Process, error) { return append([]Process(nil), processes...), nil },
		Owners:    func() ([]Owner, []string) { return owners, []string{"a terminal that could not be read"} },
		EndProcess: func(_ context.Context, item ProcessItem) error {
			ended = append(ended, item.PID)
			return nil
		},
	}

	// Act
	plan, err := cfg.PlanProcesses(t.Context())

	// Assert
	if err != nil {
		t.Fatalf("PlanProcesses: %v", err)
	}
	if len(ended) != 0 {
		t.Errorf("the plan ended %v, want nothing ended", ended)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Errorf("after the plan the home holds %v (%v), want nothing written", entries, err)
	}
	if got, want := pidsOf(plan.Ending), []int{10}; !reflect.DeepEqual(got, want) {
		t.Errorf("the plan would end %v, want %v", got, want)
	}
	if got, want := pidsOf(plan.Left), []int{51, 70}; !reflect.DeepEqual(got, want) {
		t.Errorf("the plan would name %v for the CFO, want %v", got, want)
	}
	if plan.Read != len(processes) || len(plan.Owners) != 2 || !reflect.DeepEqual(plan.Notes, []string{"a terminal that could not be read"}) {
		t.Errorf("the plan read %d processes for %d terminals with notes %v, want %d, 2 and the reader's note", plan.Read, len(plan.Owners), plan.Notes, len(processes))
	}
	why := map[int]string{}
	for _, item := range plan.Kept {
		why[item.PID] = item.Owner + ": " + item.Why
	}
	for pid, want := range map[int]string{
		21: "gb-live: terminal gb-live runs and its host reaches it",
		22: "gb-live: terminal gb-live runs and its host reaches it",
		30: "gb-live: detached from terminal gb-live, whose goblin works",
		40: "gb-live: a desktop program",
		50: "gb-live: a gate's agent",
		61: "gb-live: somebody else runs it in the task's folders",
	} {
		if !strings.HasPrefix(why[pid], want) {
			t.Errorf("pid %d is kept as %q, want a rule starting %q", pid, why[pid], want)
		}
	}
	for _, pid := range []int{10, 51, 70, 80} {
		if rule, isKept := why[pid]; isKept {
			t.Errorf("pid %d is among the kept as %q, want it ended, named or not judged", pid, rule)
		}
	}

	// The sweep, with the same readers, ends what the plan listed.
	var record Record
	cfg.sweepProcesses(t.Context(), &record)
	if !reflect.DeepEqual(ended, pidsOf(plan.Ending)) || !reflect.DeepEqual(pidsOf(record.Processes.Ended), pidsOf(plan.Ending)) {
		t.Errorf("the sweep ended %v and recorded %v, want what the plan listed, %v", ended, pidsOf(record.Processes.Ended), pidsOf(plan.Ending))
	}
	if !reflect.DeepEqual(pidsOf(record.Processes.Left), pidsOf(plan.Left)) {
		t.Errorf("the sweep named %v for the CFO, want what the plan listed, %v", pidsOf(record.Processes.Left), pidsOf(plan.Left))
	}
}

// A machine that cannot be read gives no plan, where a plan with nothing in
// it would read as nothing to end.
func TestAPlanOfAMachineThatCannotBeReadIsAnError(t *testing.T) {
	// Arrange
	cfg := Config{
		Processes: func(context.Context) ([]Process, error) { return nil, errors.New("access denied") },
		Owners:    func() ([]Owner, []string) { return nil, nil },
	}

	// Act
	plan, err := cfg.PlanProcesses(t.Context())

	// Assert
	if err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Errorf("PlanProcesses = %+v, %v, want the read's error", plan, err)
	}
}
