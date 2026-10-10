package reap

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
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

// unreadablePanes is a Herdr that cannot be read: not installed, or its
// server not answering.
type unreadablePanes struct{}

func (unreadablePanes) Snapshot(context.Context) (herdr.SessionSnapshot, error) {
	return herdr.SessionSnapshot{}, errors.New("herdr is not running")
}

func (unreadablePanes) PaneProcessInfo(context.Context, herdr.Target) (herdr.PaneProcessInfo, error) {
	return herdr.PaneProcessInfo{}, errors.New("herdr is not running")
}

// With no Herdr server for the fleet's session in the process table no pane
// can exist, so a machine that runs its goblins natively still sweeps for
// orphans, even past a test fixture's server for another session or a
// short-lived Herdr CLI call; the fleet session's server that runs but cannot
// be read could hide live goblins, so the sweep still refuses.
func TestTheSweepRunsWithoutHerdrOnlyWhenNoHerdrServerRuns(t *testing.T) {
	cases := []struct {
		name    string
		herdr   []Process
		wantErr bool
	}{
		{name: "no Herdr process", wantErr: false},
		{name: "the fleet session's server that cannot be read", herdr: []Process{process(300, 1, "herdr.exe", `herdr server`, fixtureStart)}, wantErr: true},
		{name: "the fleet session's server named by flag", herdr: []Process{process(300, 1, "herdr.exe", `herdr --session default server`, fixtureStart)}, wantErr: true},
		{name: "another session's server", herdr: []Process{process(300, 1, "herdr.exe", `herdr --session fx123 server`, fixtureStart)}, wantErr: false},
		{name: "a Herdr CLI call", herdr: []Process{process(300, 1, "herdr.exe", `herdr pane list`, fixtureStart)}, wantErr: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := nativeHome(t)
			processes := stubProcesses{
				process(400, 1, "cfo.exe", `cfo.exe host --id board`, fixtureStart),
				process(410, 400, "claude.exe", `claude --dangerously-skip-permissions`, fixtureLatest),
			}
			processes = append(processes, tc.herdr...)

			inv, notes, err := Collector{Home: h, Session: "default", Panes: unreadablePanes{}, Processes: processes}.Collect(context.Background())

			if tc.wantErr {
				if err == nil {
					t.Fatalf("Collect swept past a Herdr server it could not read: %+v", inv)
				}
				return
			}
			if err != nil {
				t.Fatalf("Collect refused a machine with no Herdr server for its session: %v", err)
			}
			if len(inv.NativeHosts) == 0 {
				t.Errorf("native hosts = %v, want the goblin's host read", inv.NativeHosts)
			}
			if !strings.Contains(strings.Join(notes, " "), "no Herdr server runs") {
				t.Errorf("notes = %q, want one saying Herdr is not running", notes)
			}
		})
	}
}

// stillRunning is a second reading of the machine that finds every one of
// processes still running, each as the sighting for its pid in whose says, and
// owned by nobody where whose names none.
func stillRunning(processes []Process, whose map[int]Sighting) func(context.Context) ([]Sighting, []string, error) {
	return func(context.Context) ([]Sighting, []string, error) {
		sightings := make([]Sighting, 0, len(processes))
		for _, process := range processes {
			sighting := whose[process.PID]
			sighting.PID, sighting.Started = process.PID, process.Start
			sightings = append(sightings, sighting)
		}
		return sightings, nil, nil
	}
}

// A goblin's own tests run their children in its terminal, but Git Bash runs
// env and timeout, MSYS programs, by replacing its own Windows process, so
// their chain of parents stops short of the goblin's host. Whose such a
// process is, is asked where the janitor's process sweep asks it: one a
// running terminal owns is that terminal's and never an orphan, and one that
// only carries the mark of a terminal whose host is gone, or that nobody
// owns, is reported. On 2026-10-07 the live sweep woke the CFO twice for a
// goblin's spawn.test.exe and its stand-in cmd.exe as unsupervised harnesses,
// and on 2026-10-10 twice more.
func TestAProcessARunningTerminalOwnsIsNoOrphan(t *testing.T) {
	for name, test := range map[string]struct {
		whose    Sighting
		hostRuns bool
		reported bool
	}{
		"owned by the terminal that runs":             {Sighting{Terminal: "board", Owner: "board"}, true, false},
		"owned by it with no mark of its own":         {Sighting{Owner: "board"}, true, false},
		"marked by a terminal whose host is gone":     {Sighting{Terminal: "board"}, false, true},
		"owned by nobody while another terminal runs": {Sighting{}, true, true},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			h, _ := nativeHome(t)
			standIn := `C:\WINDOWS\system32\cmd.exe /c codex --dangerously-bypass-approvals-and-sandbox`
			processes := stubProcesses{
				process(500, 9999, "go.exe", `go test ./internal/spawn/`, fixtureLatest),
				process(510, 500, "spawn.test.exe", `C:\tmp\spawn.test.exe -test.timeout=10m0s`, fixtureLatest),
				process(520, 510, "spawn.test.exe", `C:\tmp\spawn.test.exe native-spawn-host --state C:\tmp\state --id task-7 -- `+standIn, fixtureLatest),
				process(530, 520, "cmd.exe", standIn, fixtureLatest),
			}
			if test.hostRuns {
				processes = append(processes, process(400, 1, "cfo.exe", `cfo.exe host --id board`, fixtureStart), process(410, 400, "claude.exe", `claude --dangerously-skip-permissions`, fixtureLatest))
			}
			collector := Collector{Home: h, Session: "default", Processes: processes, Sightings: stillRunning(processes, map[int]Sighting{500: test.whose, 510: test.whose, 520: test.whose, 530: test.whose})}

			// Act
			inv, _, err := collector.Collect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			orphans := classOf(Classify(inv), OrphanProcess)

			// Assert
			for _, pid := range []int{520, 530} {
				if reported := reportsPID(orphans, pid); reported != test.reported {
					t.Errorf("the goblin's test host and stand-in: pid %d reported = %v, want %v: %v", pid, reported, test.reported, lines(orphans))
				}
			}
			for _, finding := range orphans {
				if finding.Shape != "" {
					t.Errorf("a harness the fleet launched is told once for its shape, want once for each process: %s", finding.Line())
				}
			}
		})
	}
}

// The listing costs a subprocess and can be seconds old before anything else
// about a process is read, and a spawn test's stand-ins last about ten
// seconds. On 2026-10-10 a sweep that began at 11:12:27Z recorded a
// spawn.test.exe and a cmd.exe born at 11:12:17Z and a codex.exe born at
// 11:12:19Z, each held as "could not determine what this process belongs
// to": all three had ended before their directory and environment were read.
// A listed process is a finding only when a second reading of the machine
// still finds it, by its pid and its start, and one that has ended is named
// in a note in place of a finding.
func TestAProcessThatEndedSinceTheListingIsNoFinding(t *testing.T) {
	// Arrange
	h, worktree := nativeHome(t)
	standIn := `C:\WINDOWS\system32\cmd.exe /c codex --dangerously-bypass-approvals-and-sandbox`
	host := process(400, 1, "cfo.exe", `cfo.exe host --id board`, fixtureStart)
	goblin := process(410, 400, "claude.exe", `claude --dangerously-skip-permissions`, fixtureLatest)
	ended := process(520, 9999, "spawn.test.exe", `C:\tmp\spawn.test.exe native-spawn-host --state C:\tmp\state --id task-7 -- `+standIn, fixtureLatest)
	replaced := process(530, 520, "cmd.exe", standIn, fixtureLatest)
	server := process(540, 9999, "node.exe", `node `+worktree+`-2\node_modules\vite\bin\vite.js`, fixtureLatest)
	orphan := process(550, 9999, "codex.exe", `codex --dangerously-bypass-approvals-and-sandbox`, fixtureLatest)
	unread := process(560, 9999, "codex.exe", `codex --dangerously-bypass-approvals-and-sandbox`, time.Time{})
	processes := stubProcesses{host, goblin, ended, replaced, server, orphan, unread}
	// CIM keeps a start to the microsecond and the system's own list to a
	// tenth of one, so two readings of one process differ by less than one.
	later := stillRunning([]Process{host, goblin, orphan, unread}, nil)
	sightings := func(ctx context.Context) ([]Sighting, []string, error) {
		found, _, _ := later(ctx)
		for index := range found {
			if found[index].PID == orphan.PID {
				found[index].Started = orphan.Start.Add(700 * time.Nanosecond)
			}
		}
		// The pid of the cmd that ended names a process started since.
		return append(found, Sighting{PID: replaced.PID, Started: replaced.Start.Add(3 * time.Second)}), []string{"a note of the second reading's own"}, nil
	}

	// Act
	inv, notes, err := Collector{Home: h, Session: "default", Processes: processes, Sightings: sightings}.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	findings := Classify(inv)

	// Assert
	var reported []int
	for _, finding := range Actionable(findings) {
		reported = append(reported, finding.PID)
	}
	slices.Sort(reported)
	if !slices.Equal(reported, []int{orphan.PID, unread.PID}) {
		t.Errorf("running findings name pids %v, want the two orphans that still run, %d and %d: %v", reported, orphan.PID, unread.PID, lines(findings))
	}
	note := "no finding, ended while the sweep read the machine (each had the shape of a harness or a dev server): spawn.test.exe pid 520, cmd.exe pid 530, node.exe pid 540"
	if !slices.Contains(notes, note) {
		t.Errorf("notes %q do not name the processes that ended, want %q", notes, note)
	}
	if !slices.Contains(notes, "a note of the second reading's own") {
		t.Errorf("notes %q dropped the second reading's own", notes)
	}
}

// A second reading that fails says nothing of whose any process is, and a
// sweep that went on would report every process of a goblin's own test as an
// orphan. It refuses, as it does when the listing itself cannot be read.
func TestTheSweepRefusesWhenTheSecondReadingFails(t *testing.T) {
	// Arrange
	h, _ := nativeHome(t)
	processes := stubProcesses{process(520, 9999, "codex.exe", `codex --dangerously-bypass-approvals-and-sandbox`, fixtureLatest)}
	unreadable := func(context.Context) ([]Sighting, []string, error) { return nil, nil, errors.New("access denied") }

	// Act
	_, _, err := Collector{Home: h, Session: "default", Processes: processes, Sightings: unreadable}.Collect(context.Background())

	// Assert
	if err == nil || !strings.Contains(err.Error(), "read whose each process is: access denied") {
		t.Fatalf("Collect = %v, want a refusal naming the reading that failed", err)
	}
}
