package reap

import (
	"strings"
	"testing"
	"time"
)

// The grep of 2026-11-11: a goblin's tool call started it from Git Bash with
// the drive root among its paths, the shell that started it ended, and it
// went on for 1 hour 50 minutes, 87 minutes of it on the processor, holding
// 6,680,532 handles.
const strayCommand = "grep -rn -A8 pattern / internal/proc/job_windows.go"

var listedAt = fixtureStart.Add(110 * time.Minute)

// strayGrep is that grep as the listing reads it: its shell, pid 400, is
// gone, and its terminal task-1 runs and owns it.
func strayGrep() Process {
	grep := process(500, 400, "grep.exe", strayCommand, fixtureStart)
	grep.Owner, grep.Handles, grep.Processor = "task-1", 6680532, 87*time.Minute
	return grep
}

func strayInventory(processes ...Process) Inventory {
	return Inventory{At: listedAt, Processes: processes}
}

// An orphaned child of a tool call is listed with its age, its processor
// time and its handles, against the terminal that started it.
func TestADetachedChildOfAToolCallIsListedWithWhatItCosts(t *testing.T) {
	// Arrange
	inventory := strayInventory(strayGrep())

	// Act
	findings := classOf(Classify(inventory), DetachedProcess)

	// Assert
	if len(findings) != 1 {
		t.Fatalf("got %d detached findings, want one: %+v", len(findings), findings)
	}
	finding := findings[0]
	if finding.PID != 500 || finding.TaskID != "task-1" || finding.Running != 110*time.Minute || finding.Processor != 87*time.Minute || finding.Handles != 6680532 {
		t.Fatalf("finding = %+v, want pid 500 of task-1, running 1h50m, 87m of processor time, 6680532 handles", finding)
	}
	if !strings.Contains(finding.Detail, strayCommand) {
		t.Errorf("detail %q does not show its command %q", finding.Detail, strayCommand)
	}
	if line := finding.Line(); !strings.Contains(line, "running 1h50m0s") || !strings.Contains(line, "1h27m0s of processor time") || !strings.Contains(line, "6680532 handles") {
		t.Errorf("line %q does not show what the process costs", line)
	}
	if hold := finding.Hold(); !strings.Contains(hold, "Name 500 with --force") {
		t.Errorf("hold %q does not ask for its pid", hold)
	}
}

// Only a process of the fleet's own trees whose parent has exited is listed:
// what a terminal of this home runs or marked, and not its host, a machine
// service, a gate's agent or the sweep's own session.
func TestOnlyADetachedProcessOfTheFleetIsListed(t *testing.T) {
	cases := []struct {
		name       string
		inventory  func() Inventory
		isDetached bool
	}{
		{"one its terminal owns", func() Inventory { return strayInventory(strayGrep()) }, true},
		{"one its gone terminal marked", func() Inventory {
			grep := strayGrep()
			grep.Owner, grep.Terminal = "", "task-1"
			return strayInventory(grep)
		}, true},
		{"one whose parent's pid a later process took", func() Inventory {
			return strayInventory(process(400, 1, "node.exe", "node server.js", fixtureLater), strayGrep())
		}, true},
		{"one whose parent still runs", func() Inventory {
			return strayInventory(process(400, 1, "bash.exe", "bash", fixtureStart.Add(-time.Minute)), strayGrep())
		}, false},
		{"one nothing ties to the fleet", func() Inventory {
			grep := strayGrep()
			grep.Owner = ""
			return strayInventory(grep)
		}, false},
		{"a gate's agent", func() Inventory {
			grep := strayGrep()
			grep.IsGateAgent = true
			return strayInventory(grep)
		}, false},
		{"one under a verification controller's custody", func() Inventory {
			grep := strayGrep()
			grep.HasExecutionCustody = true
			return strayInventory(grep)
		}, false},
		{"a machine service a goblin started", func() Inventory {
			daemon := strayGrep()
			daemon.Name, daemon.CommandLine = "no-mistakes.exe", "no-mistakes daemon"
			return strayInventory(daemon)
		}, false},
		{"the fleet's own program", func() Inventory {
			serve := strayGrep()
			serve.Name, serve.CommandLine = "goblins.exe", "goblins serve --listen 127.0.0.1:4310"
			return strayInventory(serve)
		}, false},
		{"a harness", func() Inventory {
			harness := strayGrep()
			harness.Name, harness.CommandLine = "claude.exe", "claude --dangerously-skip-permissions"
			return strayInventory(harness)
		}, false},
		{"its terminal's host", func() Inventory {
			inventory := strayInventory(strayGrep())
			inventory.NativeHosts = []NativeHost{{ID: "task-1", HostPID: 500, Started: fixtureStart}}
			return inventory
		}, false},
		{"the sweep's own session", func() Inventory {
			inventory := strayInventory(strayGrep())
			inventory.SelfPIDs = []int{500}
			return inventory
		}, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			inventory := test.inventory()

			// Act
			findings := classOf(Classify(inventory), DetachedProcess)

			// Assert
			if isDetached := len(findings) == 1 && findings[0].PID == 500; isDetached != test.isDetached || len(findings) > 1 {
				t.Fatalf("detached findings = %+v, want pid 500 listed: %v", findings, test.isDetached)
			}
		})
	}
}

// --apply alone never ends a detached process: naming its pid with --force
// does, by that pid.
func TestADetachedProcessIsEndedOnlyByNamingItsPid(t *testing.T) {
	cases := []struct {
		name   string
		force  map[string]bool
		killed []int
	}{
		{"apply alone", nil, nil},
		{"its task named", map[string]bool{"task-1": true}, nil},
		{"its pid named", map[string]bool{"500": true}, []int{500}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			runner := &gitRunner{}
			service := newService(t, testHome(t), strayInventory(strayGrep()), runner)

			// Act
			result, err := service.Apply(t.Context(), Options{Force: test.force})

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if len(runner.killed) != len(test.killed) || len(test.killed) > 0 && runner.killed[0] != test.killed[0] {
				t.Fatalf("killed %v, want %v", runner.killed, test.killed)
			}
			if finding := onlyFinding(t, result, DetachedProcess); finding.Held() == (len(test.killed) > 0) {
				t.Fatalf("finding held %v after killing %v: %+v", finding.Held(), runner.killed, finding)
			}
		})
	}
}

// A detached process is one finding from one sweep to the next while what it
// costs grows, so the CEO is told of it once.
func TestADetachedProcessKeepsItsIdentityAsItsCostsGrow(t *testing.T) {
	// Arrange
	earlier := strayInventory(strayGrep())
	later := strayInventory(strayGrep())
	later.At = later.At.Add(10 * time.Minute)
	later.Processes[0].Handles += 500000
	later.Processes[0].Processor += 8 * time.Minute

	// Act
	before := ReportKeys(classOf(Classify(earlier), DetachedProcess))
	after := ReportKeys(classOf(Classify(later), DetachedProcess))

	// Assert
	if len(before) != 1 || len(after) != 1 || before[0] != after[0] {
		t.Fatalf("report keys %v then %v, want one key that stays", before, after)
	}
}
