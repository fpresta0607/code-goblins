package watch

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/fpresta0607/code-goblins/internal/disk"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/janitor"
	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/services"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// The machine's heavy line is free for a prune only with one slot that no
// run holds or waits for, naming the run as goblins gate turns does, and a line
// of two slots is never free, since a turn in one leaves a build beside it.
func TestHeavyLineIsFreeOnlyWithOneSlotAndNoRunInIt(t *testing.T) {
	for name, row := range map[string]struct {
		occupy   func(t *testing.T, store string)
		wantSaid string
	}{
		"a free line": {},
		"a run that holds the line": {
			occupy: func(t *testing.T, store string) {
				turn, err := verify.Admission{Dir: store, Available: func() (uint64, error) { return 16 << 30, nil }, Who: "goblins gate test at 0123abcd", Limit: time.Minute, Poll: 10 * time.Millisecond}.Wait(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = turn.Release() })
			},
			wantSaid: fmt.Sprintf("goblins gate test at 0123abcd (pid %d) holds the machine's heavy line", os.Getpid()),
		},
		"a run that waits for memory": {
			occupy: func(t *testing.T, store string) {
				ctx, cancel := context.WithCancel(context.Background())
				ended := make(chan struct{})
				go func() {
					defer close(ended)
					_, _ = verify.Admission{Dir: store, Floor: 4 << 30, Available: func() (uint64, error) { return 1 << 30, nil }, Who: "goblins verify fast of task t1", Limit: verify.NoLimit, Poll: 10 * time.Millisecond}.Wait(ctx)
				}()
				t.Cleanup(func() { cancel(); <-ended })
				// A run stands in line a moment before its card names it.
				for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
					if _, waiting, err := verify.Line(store); err == nil && len(waiting) == 1 && waiting[0].Who == "goblins verify fast of task t1" {
						return
					}
				}
				t.Fatal("the run never joined the line")
			},
			wantSaid: fmt.Sprintf("goblins verify fast of task t1 (pid %d) waits for the machine's heavy line", os.Getpid()),
		},
		"a line of two slots": {
			occupy: func(t *testing.T, store string) {
				if err := os.MkdirAll(store, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(store, "capacity.json"), []byte(`{"capacity": 2}`), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantSaid: "the line has 2 slots, and a turn in one leaves a build beside it in another",
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			t.Setenv("GOBLINS_VERIFY_DIR", t.TempDir())
			store, err := verify.AdmissionDir()
			if err != nil {
				t.Fatal(err)
			}
			if row.occupy != nil {
				row.occupy(t, store)
			}

			// Act
			err = heavyLine{executable: `C:\cg\goblins.exe`}.Free()

			// Assert
			if row.wantSaid == "" && err != nil || row.wantSaid != "" && (err == nil || err.Error() != row.wantSaid) {
				t.Errorf("Free = %v; want %q", err, row.wantSaid)
			}
		})
	}
}

// A prune runs in a turn this build's goblins gate turn takes for it, named for
// whoever reads the line, and runs nothing if a run takes the line first and
// keeps it past the prune's short wait.
func TestHeavyLineWrapsAPruneInThisBuildsGateTurn(t *testing.T) {
	// Arrange
	line := heavyLine{executable: `C:\cg\goblins.exe`}

	// Act
	command := line.Wrap("the janitor, pruning the uv cache", []string{"uv", "cache", "prune"})

	// Assert
	if want := []string{`C:\cg\goblins.exe`, "gate", "turn", "--as", "the janitor, pruning the uv cache", "--wait", "30s", "--", "uv", "cache", "prune"}; !slices.Equal(command, want) {
		t.Errorf("Wrap = %q, want %q", command, want)
	}
}

// TestStrayWakeFiresOnlyForAStrayNotReportedBefore: the janitor reports the
// same strays on every pass, and one going away is nothing the CEO must act
// on; only a stray it has not been told about wakes it.
func TestStrayWakeFiresOnlyForAStrayNotReportedBefore(t *testing.T) {
	strays := func(paths ...string) janitor.Record {
		var record janitor.Record
		for _, path := range paths {
			record.Strays = append(record.Strays, janitor.Item{Kind: "worktree", Path: path, Detail: "no task records it"})
		}
		return record
	}
	cases := []struct {
		name     string
		previous janitor.Record
		current  janitor.Record
		wantWake bool
	}{
		{"the first stray", strays(), strays(`C:\dev\a`), true},
		{"the same strays again", strays(`C:\dev\a`, `C:\dev\b`), strays(`C:\dev\b`, `C:\dev\a`), false},
		{"the same stray in another case", strays(`C:\dev\a`), strays(`c:\DEV\a`), false},
		{"one of two went away", strays(`C:\dev\a`, `C:\dev\b`), strays(`C:\dev\a`), false},
		{"a new one beside the old", strays(`C:\dev\a`), strays(`C:\dev\a`, `C:\dev\c`), true},
		{"none left", strays(`C:\dev\a`), strays(), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			detail := strayWake(c.previous, c.current)
			if (detail != "") != c.wantWake {
				t.Fatalf("strayWake = %q, want a wake %v", detail, c.wantWake)
			}
			for _, stray := range c.current.Strays {
				if c.wantWake && !strings.Contains(detail, stray.Path) {
					t.Errorf("strayWake = %q, want every stray named, %s among them", detail, stray.Path)
				}
			}
		})
	}
}

// The sweep leaves the same unproven processes on every pass, and the CEO is
// told of each once, in one wake for all of them that says what each costs:
// its pid, its age, its memory and its command.
func TestProcessWakeFiresOnceForAProcessTheSweepLeft(t *testing.T) {
	started := time.Date(2026, 10, 9, 14, 20, 0, 0, time.UTC)
	now := started.Add(2*time.Hour + 10*time.Minute)
	left := func(pids ...int) janitor.Record {
		var record janitor.Record
		for _, pid := range pids {
			record.Processes.Left = append(record.Processes.Left, janitor.ProcessItem{PID: pid, Started: started, Name: "node.exe", Memory: 2560 << 20, Command: "node bridge.js", Why: "a browser bridge nothing ties to an owner"})
		}
		return record
	}
	restarted := left(20872)
	restarted.Processes.Left[0].Started = started.Add(time.Hour)
	ended := janitor.Record{Processes: janitor.ProcessSweep{Ended: []janitor.ProcessItem{{PID: 7, Name: "node.exe", Owner: "g1"}}}}
	cases := []struct {
		name     string
		previous janitor.Record
		current  janitor.Record
		wantWake bool
	}{
		{"the first one left", left(), left(20872), true},
		{"the same ones again", left(20872, 28176), left(28176, 20872), false},
		{"one of two went away", left(20872, 28176), left(20872), false},
		{"a new one beside the old", left(20872), left(20872, 28176), true},
		{"another process under an old pid", left(20872), restarted, true},
		{"none left", left(20872), left(), false},
		{"what the sweep ended itself", left(), ended, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			detail := processWake(c.previous, c.current, now)
			if (detail != "") != c.wantWake {
				t.Fatalf("processWake = %q, want a wake %v", detail, c.wantWake)
			}
			if !c.wantWake {
				return
			}
			for _, item := range c.current.Processes.Left {
				if !strings.Contains(detail, fmt.Sprintf("pid %d node.exe", item.PID)) {
					t.Errorf("processWake = %q, want every process named, pid %d among them", detail, item.PID)
				}
			}
			for _, part := range []string{"2.5 GB", "node bridge.js", "a browser bridge nothing ties to an owner"} {
				if !strings.Contains(detail, part) {
					t.Errorf("processWake = %q, want %q in it", detail, part)
				}
			}
			if strings.ContainsAny(detail, ";\u2014") {
				t.Errorf("processWake = %q, want no semicolon and no long dash in what is shown", detail)
			}
		})
	}
	if detail := processWake(left(), left(20872), now); !strings.Contains(detail, "2h10m0s old") {
		t.Errorf("processWake = %q, want the process's age", detail)
	}
}

// dockerRunner answers docker as an engine running one stack and records
// every call.
type dockerRunner struct {
	calls []string
}

func (r *dockerRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	call := request.Name + " " + strings.Join(request.Args, " ")
	r.calls = append(r.calls, call)
	switch {
	case strings.HasPrefix(call, "docker info"):
		return execx.Result{Stdout: []byte("29.7.2\n")}, nil
	case strings.HasPrefix(call, "docker compose") && strings.Contains(call, " ps "):
		return execx.Result{Stdout: []byte("backend\nredis\n")}, nil
	}
	return execx.Result{}, nil
}

// The watcher's janitor pass stops a stack goblins services started whose
// holders have all ended, through docker compose in the stack's checkout.
func TestTheJanitorPassStopsALocalServicesStackNoLiveTaskHolds(t *testing.T) {
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	if err := os.MkdirAll(h.State, 0o755); err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(root, "PrecisionDocs-AI")
	record := services.Record{Stacks: map[string]services.Stack{"PrecisionDocs-AI": {
		Project: "PrecisionDocs-AI", Checkout: checkout, Compose: "docker-compose.dev.yml", Owned: true,
		Holders: []services.Hold{{Task: "ended-task"}},
	}}}
	if err := services.WriteRecord(h.State, record); err != nil {
		t.Fatal(err)
	}
	runner := &dockerRunner{}

	runJanitor(Config{Home: h, Reap: &reap.Service{Commands: runner}, JanitorEvery: time.Hour}, reap.Inventory{})

	if !slices.Contains(runner.calls, "docker compose --file docker-compose.dev.yml down") {
		t.Errorf("docker calls = %q, want the stack taken down", runner.calls)
	}
	after, err := services.ReadRecord(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if stack := after.Stacks["PrecisionDocs-AI"]; stack.IsUp() || stack.Owned {
		t.Errorf("stack = %+v, want it released and stopped", stack)
	}
	swept, err := janitor.ReadRecord(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(swept.Removed) != 1 || swept.Removed[0].Kind != "services" {
		t.Errorf("janitor removed = %+v, want the stopped stack recorded", swept.Removed)
	}
}

// The watcher's janitor pass measures the gate's home where the gate runs it
// and on the Dev Drive, where a home moved by hand keeps its repositories
// behind junctions the measure does not follow, and measures one folder once
// the home has moved there whole.
func TestTheJanitorPassMeasuresTheGatesHomeOnBothDrives(t *testing.T) {
	for name, isMoved := range map[string]bool{"moved by hand": false, "moved whole": true} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data"), DevDrive: filepath.Join(root, "devdrive")}
			target := filepath.Join(h.DevDrive, "no-mistakes")
			gate := filepath.Join(root, "profile", ".no-mistakes")
			want := []string{gate, target}
			if isMoved {
				gate, want = target, []string{target}
			}
			for _, folder := range []string{h.State, gate, target} {
				if err := os.MkdirAll(folder, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("NM_HOME", gate)

			// Act
			runJanitor(Config{Home: h, Reap: &reap.Service{Commands: &dockerRunner{}}, JanitorEvery: time.Hour}, reap.Inventory{})

			// Assert
			swept, err := janitor.ReadRecord(h.State)
			if err != nil {
				t.Fatal(err)
			}
			if swept.Gate == nil || !slices.Equal(swept.Gate.Folders, want) {
				t.Errorf("gate = %+v, want %v measured", swept.Gate, want)
			}
		})
	}
}

// fleetSettings writes the home's config/fleet.json.
func fleetSettings(t *testing.T, h home.Home, settings string) {
	t.Helper()
	path := filepath.Join(h.Root, "config", "fleet.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A clean mark no drive is under, and one every drive is under.
const (
	markNoDriveIsUnder    = `{"disk_floor_gb":0,"disk_wake_gb":0,"disk_clean_gb":0}`
	markEveryDriveIsUnder = `{"disk_clean_gb":1000000000}`
)

// The orphan sweep starts the janitor's pass once an hour has passed since the
// last, and at once when free disk fell under the clean mark since the last
// pass read it at or above, rather than wait for the hour (#2761).
func TestTheJanitorPassRunsAtOnceWhenFreeDiskFallsUnderTheCleanMark(t *testing.T) {
	for _, c := range []struct {
		name      string
		settings  string
		lastAgo   time.Duration
		lastFree  uint64
		isStarted bool
	}{
		{name: "under the mark, the last pass read above it", settings: markEveryDriveIsUnder, lastAgo: 10 * time.Minute, lastFree: 1 << 62, isStarted: true},
		{name: "under the mark, the last pass read under it too", settings: markEveryDriveIsUnder, lastAgo: 10 * time.Minute, lastFree: 1 << 30},
		{name: "above the mark", settings: markNoDriveIsUnder, lastAgo: 10 * time.Minute, lastFree: 1 << 30},
		{name: "above the mark, an hour past the last pass", settings: markNoDriveIsUnder, lastAgo: 2 * time.Hour, lastFree: 1 << 30, isStarted: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
			if err := os.MkdirAll(h.State, 0o755); err != nil {
				t.Fatal(err)
			}
			fleetSettings(t, h, c.settings)
			last := time.Now().Add(-c.lastAgo).UTC()
			if err := janitor.WriteRecord(h.State, janitor.Record{Time: last, Disk: disk.Reading{Drive: "C:", Free: c.lastFree}}); err != nil {
				t.Fatal(err)
			}
			t.Setenv("NM_HOME", filepath.Join(root, "no-gate"))

			// Act
			tidyHome(Config{Home: h, Reap: &reap.Service{Commands: &dockerRunner{}}, JanitorEvery: time.Hour}, reap.Inventory{})
			janitorRunning.Lock()
			janitorRunning.Unlock()

			// Assert
			swept, err := janitor.ReadRecord(h.State)
			if err != nil {
				t.Fatal(err)
			}
			if isStarted := !swept.Time.Equal(last); isStarted != c.isStarted {
				t.Fatalf("pass started = %v, want %v", isStarted, c.isStarted)
			}
		})
	}
}

// The watcher's janitor pass frees what is safe at once under the clean mark:
// a finished task's scratch folder unwritten for two hours goes, which above
// the mark waits its day (#2761).
func TestTheJanitorPassUnderTheCleanMarkTakesAFinishedTasksScratch(t *testing.T) {
	for _, c := range []struct {
		name     string
		settings string
		isGone   bool
	}{
		{name: "under the mark", settings: markEveryDriveIsUnder, isGone: true},
		{name: "above the mark", settings: markNoDriveIsUnder},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			temp := t.TempDir()
			t.Setenv("TMP", temp)
			t.Setenv("TEMP", temp)
			root := t.TempDir()
			h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
			if err := os.MkdirAll(h.State, 0o755); err != nil {
				t.Fatal(err)
			}
			fleetSettings(t, h, c.settings)
			t.Setenv("NM_HOME", filepath.Join(root, "no-gate"))
			scratch := filepath.Join(h.Scratch(), "gone-task")
			if err := os.MkdirAll(scratch, 0o755); err != nil {
				t.Fatal(err)
			}
			written := time.Now().Add(-2 * time.Hour)
			if err := os.Chtimes(scratch, written, written); err != nil {
				t.Fatal(err)
			}
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			inventory := reap.Inventory{Processes: []reap.Process{{PID: os.Getpid(), CommandLine: `"` + self + `" -test.run=TheJanitorPass`}}}

			// Act
			runJanitor(Config{Home: h, Reap: &reap.Service{Commands: &dockerRunner{}}, JanitorEvery: time.Hour}, inventory)

			// Assert
			_, err = os.Stat(scratch)
			if isGone := os.IsNotExist(err); isGone != c.isGone {
				swept, _ := janitor.ReadRecord(h.State)
				t.Fatalf("scratch gone = %v, want %v; removed %+v, notes %q", isGone, c.isGone, swept.Removed, swept.Notes)
			}
		})
	}
}

// The watcher's janitor pass reads the gate's home the gate runs from for
// completed runs' checkouts kept past its retention, which the supervisor's
// gate_growth wake names (#2761).
func TestTheJanitorPassCountsTheGatesCompletedRunsPastItsRetention(t *testing.T) {
	// Arrange
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	gate := filepath.Join(root, "profile", ".no-mistakes")
	checkout := filepath.Join(gate, "worktrees", "repo1", "run-1")
	for _, folder := range []string{h.State, checkout} {
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(gate, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE runs (id TEXT PRIMARY KEY, status TEXT); INSERT INTO runs VALUES ('run-1', 'completed')"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	written := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(checkout, written, written); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NM_HOME", gate)

	// Act
	runJanitor(Config{Home: h, Reap: &reap.Service{Commands: &dockerRunner{}}, JanitorEvery: time.Hour}, reap.Inventory{})

	// Assert
	swept, err := janitor.ReadRecord(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if swept.Gate == nil || swept.Gate.PastRetention == nil || swept.Gate.PastRetention.Unlinked.Count != 1 {
		t.Fatalf("gate = %+v, want one completed run's checkout past the retention; notes %q", swept.Gate, swept.Notes)
	}
}
