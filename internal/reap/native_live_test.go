package reap

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// nativeHome is a home with one native goblin, board, that said done: its
// record, its status, its worktree, and its terminal's host record naming
// host pid 400, recorded at fixtureLater. It returns the home and the
// worktree.
func nativeHome(t *testing.T) (home.Home, string) {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "proj")
	worktree := filepath.Join(project, ".worktrees", "gb-board")
	stateDir := filepath.Join(root, "state")
	for _, dir := range []string{worktree, filepath.Join(stateDir, "hosts")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.WriteTaskMeta(stateDir, state.TaskMeta{ID: "board", Window: "native", Worktree: worktree, Project: project, Harness: "claude", Kind: "ship", Backend: "native"}); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(stateDir, "board", "done: PR https://example.invalid/pull/1"); err != nil {
		t.Fatal(err)
	}
	record, err := json.Marshal(map[string]any{"id": "board", "pipe": `\\.\pipe\cfo-host-board`, "token": "0123", "version": 1, "host_pid": 400, "child_pid": 410, "started": fixtureLater})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "hosts", "board.json"), record, 0o600); err != nil {
		t.Fatal(err)
	}
	return home.Home{Root: root, State: stateDir}, worktree
}

// A host record that cannot be read says nothing about whether its goblin
// still runs, so what rests on the goblin being gone is held, as for a task
// record that cannot be read, and the sweep says which record it could not
// read.
func TestAnUnreadableHostRecordHoldsItsGoblin(t *testing.T) {
	h, worktree := nativeHome(t)
	if err := os.WriteFile(filepath.Join(h.State, "hosts", "board.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	processes := stubProcesses{
		process(400, 1, "cfo.exe", `cfo.exe host --id board`, fixtureStart),
		process(410, 400, "claude.exe", `claude --dangerously-skip-permissions`, fixtureLatest),
		process(420, 410, "node.exe", `node `+worktree+`\node_modules\vite\bin\vite.js`, fixtureLatest.Add(time.Minute)),
	}

	inv, notes, err := Collector{Home: h, Session: "default", Processes: processes}.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	findings := Classify(inv)

	held := false
	for _, finding := range classOf(findings, StaleServer) {
		held = held || (finding.TaskID == "board" && heldOnHostRecord(finding))
	}
	if !held {
		t.Errorf("the dev server of a goblin whose host record cannot be read is not held on that record: %v", lines(findings))
	}
	if !slices.ContainsFunc(notes, func(note string) bool { return strings.Contains(note, `state/hosts/board.json: UNREADABLE`) }) {
		t.Errorf("notes %q do not name the unreadable host record", notes)
	}
}

// A host that ends removes its own record, so a record listed but gone by the
// time it is read belongs to a host that has just ended: nothing is unknown,
// and the dead goblin's dev server is reported rather than held.
func TestAHostRecordGoneSinceTheListingIsAnEndedHost(t *testing.T) {
	h, worktree := nativeHome(t)
	recordFile := filepath.Join(h.State, "hosts", "board.json")
	if err := os.Remove(recordFile); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", recordFile, filepath.Join(h.State, "ended")).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v: %s", err, out)
	}
	processes := stubProcesses{
		process(410, 400, "claude.exe", `claude --dangerously-skip-permissions`, fixtureLatest),
		process(420, 410, "node.exe", `node `+worktree+`\node_modules\vite\bin\vite.js`, fixtureLatest.Add(time.Minute)),
	}

	inv, notes, err := Collector{Home: h, Session: "default", Processes: processes}.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	findings := Classify(inv)

	if slices.ContainsFunc(notes, func(note string) bool { return strings.Contains(note, "state/hosts/board.json") }) {
		t.Errorf("notes %q name a record whose host has ended", notes)
	}
	reported := false
	for _, finding := range classOf(findings, StaleServer) {
		if finding.TaskID == "board" && heldOnHostRecord(finding) {
			t.Errorf("the dev server of a goblin whose host has ended is held on its host record: %v", lines(findings))
		}
		reported = reported || finding.TaskID == "board"
	}
	if !reported {
		t.Errorf("the dev server of a goblin whose host has ended was not reported: %v", lines(findings))
	}
}

// heldOnHostRecord reports whether a finding names the unreadable host record,
// and not the task record, as what it cannot establish.
func heldOnHostRecord(finding Finding) bool {
	if !strings.Contains(finding.Detail, "host record could not be read") {
		return false
	}
	for _, refusal := range finding.Holds {
		if strings.Contains(refusal.Reason, "host record could not be read") && refusal.Key == "board" {
			return true
		}
	}
	return false
}

// A task record whose worktree is gone is archived only once nothing could say
// its goblin still runs. A native goblin's host record is that evidence, so
// when it cannot be read the record is held on it rather than archived.
func TestAnUnreadableHostRecordHoldsTheTaskRecord(t *testing.T) {
	h, worktree := nativeHome(t)
	if err := os.RemoveAll(worktree); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.State, "hosts", "board.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	inv, _, err := Collector{Home: h, Session: "default", Processes: stubProcesses{}}.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	findings := Classify(inv)

	for _, finding := range classOf(findings, OrphanMeta) {
		if finding.TaskID == "board" && !heldOnHostRecord(finding) {
			t.Errorf("the record of a native goblin whose host record cannot be read is not held on that record: %v", lines(findings))
		}
	}
}

// A native goblin has no Herdr pane: its harness runs under its terminal's
// host. While that host runs, the goblin is alive for every class, exactly as
// a goblin whose pane holds an agent is, even after it said done: its harness
// is supervised, its dev server is doing its job, and its worktree is in use.
// A host that has ended, or a record whose pid now names a process that
// started after the host recorded itself, is no evidence of a live goblin.
// Reap used to read only Herdr panes, so a live native goblin's harness read
// as an unsupervised process and its dev server as stale.
func TestALiveNativeGoblinIsNotAnOrphan(t *testing.T) {
	for name, test := range map[string]struct {
		hostStart   time.Time
		hostRunning bool
		wantAlive   bool
	}{
		"its host runs":                        {fixtureStart, true, true},
		"its host has ended":                   {fixtureStart, false, false},
		"its host's pid names a later process": {fixtureLatest, true, false},
	} {
		t.Run(name, func(t *testing.T) {
			h, worktree := nativeHome(t)
			processes := stubProcesses{
				process(410, 400, "claude.exe", `claude --dangerously-skip-permissions`, fixtureLatest),
				process(420, 410, "node.exe", `node `+worktree+`\node_modules\vite\bin\vite.js`, fixtureLatest.Add(time.Minute)),
			}
			if test.hostRunning {
				processes = append(processes, process(400, 1, "cfo.exe", `cfo.exe host --id board`, test.hostStart))
			}
			inv, _, err := Collector{Home: h, Session: "default", Processes: processes}.Collect(context.Background())
			if err != nil {
				t.Fatal(err)
			}

			findings := Classify(inv)

			reported := map[Class]bool{}
			for _, finding := range findings {
				if finding.TaskID == "board" || finding.PID == 410 || finding.PID == 420 {
					reported[finding.Class] = true
				}
			}
			if test.wantAlive && len(reported) != 0 {
				t.Errorf("a live native goblin was reported: %v", lines(findings))
			}
			if !test.wantAlive && !reported[StaleServer] {
				t.Errorf("the dev server of a native goblin whose host is gone was not reported: %v", lines(findings))
			}
		})
	}
}
