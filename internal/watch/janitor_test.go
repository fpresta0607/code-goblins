package watch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/janitor"
	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/services"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// The machine's heavy line is free for a prune only with one slot that no
// run holds or waits for, naming the run as cfo gate turns does, and a line
// of two slots is never free, since a turn in one leaves a build beside it.
func TestHeavyLineIsFreeOnlyWithOneSlotAndNoRunInIt(t *testing.T) {
	for name, row := range map[string]struct {
		occupy   func(t *testing.T, store string)
		wantSaid string
	}{
		"a free line": {},
		"a run that holds the line": {
			occupy: func(t *testing.T, store string) {
				turn, err := verify.Admission{Dir: store, Available: func() (uint64, error) { return 16 << 30, nil }, Who: "cfo gate test at 0123abcd", Limit: time.Minute, Poll: 10 * time.Millisecond}.Wait(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = turn.Release() })
			},
			wantSaid: fmt.Sprintf("cfo gate test at 0123abcd (pid %d) holds the machine's heavy line", os.Getpid()),
		},
		"a run that waits for memory": {
			occupy: func(t *testing.T, store string) {
				ctx, cancel := context.WithCancel(context.Background())
				ended := make(chan struct{})
				go func() {
					defer close(ended)
					_, _ = verify.Admission{Dir: store, Floor: 4 << 30, Available: func() (uint64, error) { return 1 << 30, nil }, Who: "cfo verify fast of task t1", Limit: verify.NoLimit, Poll: 10 * time.Millisecond}.Wait(ctx)
				}()
				t.Cleanup(func() { cancel(); <-ended })
				for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
					if _, waiting, err := verify.Line(store); err == nil && len(waiting) == 1 {
						return
					}
				}
				t.Fatal("the run never joined the line")
			},
			wantSaid: fmt.Sprintf("cfo verify fast of task t1 (pid %d) waits for the machine's heavy line", os.Getpid()),
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
			t.Setenv("CFO_VERIFY_DIR", t.TempDir())
			store, err := verify.AdmissionDir()
			if err != nil {
				t.Fatal(err)
			}
			if row.occupy != nil {
				row.occupy(t, store)
			}

			// Act
			err = heavyLine{executable: `C:\cg\cfo.exe`}.Free()

			// Assert
			if row.wantSaid == "" && err != nil || row.wantSaid != "" && (err == nil || err.Error() != row.wantSaid) {
				t.Errorf("Free = %v; want %q", err, row.wantSaid)
			}
		})
	}
}

// A prune runs in a turn this build's cfo gate turn takes for it, named for
// whoever reads the line.
func TestHeavyLineWrapsAPruneInThisBuildsGateTurn(t *testing.T) {
	// Arrange
	line := heavyLine{executable: `C:\cg\cfo.exe`}

	// Act
	command := line.Wrap("the janitor, pruning the uv cache", []string{"uv", "cache", "prune"})

	// Assert
	if want := []string{`C:\cg\cfo.exe`, "gate", "turn", "--as", "the janitor, pruning the uv cache", "--", "uv", "cache", "prune"}; !slices.Equal(command, want) {
		t.Errorf("Wrap = %q, want %q", command, want)
	}
}

// TestStrayWakeFiresOnlyForAStrayNotReportedBefore: the janitor reports the
// same strays on every pass, and one going away is nothing the CFO must act
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

// The sweep leaves the same unproven processes on every pass, and the CFO is
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

// The watcher's janitor pass stops a stack cfo services started whose
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
