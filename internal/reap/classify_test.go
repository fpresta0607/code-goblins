package reap

import (
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// base times every fixture off one instant so a parent always predates its
// child and the pid-reuse guard in descendants never fires by accident.
var (
	fixtureStart  = time.Date(2026, 9, 5, 7, 0, 0, 0, time.UTC)
	fixtureLater  = fixtureStart.Add(time.Minute)
	fixtureLatest = fixtureStart.Add(2 * time.Minute)
)

func process(pid, ppid int, name, command string, start time.Time) Process {
	return Process{PID: pid, ParentPID: ppid, Name: name, CommandLine: command, Start: start}
}

func task(id, worktree, pane, verb string) Task {
	return Task{
		ID:       id,
		Meta:     state.TaskMeta{ID: id, Worktree: worktree, Project: `C:\dev\proj`, HerdrPaneID: pane, Backend: "herdr"},
		Verb:     verb,
		Terminal: IsTerminal(verb),
	}
}

func classOf(findings []Finding, class Class) []Finding {
	var out []Finding
	for _, finding := range findings {
		if finding.Class == class {
			out = append(out, finding)
		}
	}
	return out
}

func TestClassify(t *testing.T) {
	const (
		herdrPID = 100
		shellPID = 200
		livePID  = 300
	)
	// Every case shares one live pane: a goblin doing its job must never be
	// classified, and a fixture with no live work would not prove that.
	livePane := Pane{ID: "pane-live", ShellPID: shellPID, ForegroundPID: livePID, HasAgent: true}
	liveProcesses := []Process{
		process(herdrPID, 1, "herdr.exe", `herdr server`, fixtureStart),
		process(shellPID, herdrPID, "powershell.exe", `powershell -NoExit`, fixtureLater),
		process(livePID, shellPID, "claude.exe", `claude --dangerously-skip-permissions --strict-mcp-config`, fixtureLatest),
	}

	cases := []struct {
		name      string
		inventory Inventory
		want      Class
		wantCount int
		assert    func(t *testing.T, findings []Finding)
	}{
		{
			name: "orphan process is a harness whose pane is gone",
			inventory: Inventory{
				Panes:         []Pane{livePane},
				FleetRootPIDs: []int{herdrPID},
				Processes: append(append([]Process{}, liveProcesses...),
					process(400, herdrPID, "powershell.exe", `powershell -NoExit -Command herdr-prompt-shim`, fixtureLater),
					process(31032, 400, "claude.exe", `claude --dangerously-skip-permissions --strict-mcp-config`, fixtureLatest),
				),
			},
			want:      OrphanProcess,
			wantCount: 1,
			assert: func(t *testing.T, findings []Finding) {
				if findings[0].PID != 31032 {
					t.Fatalf("orphan pid = %d, want 31032", findings[0].PID)
				}
				if findings[0].Hold != "" {
					t.Fatalf("a fleet-descended orphan must not be held at classification: %q", findings[0].Hold)
				}
			},
		},
		{
			name: "a harness outside the fleet is reported but held",
			inventory: Inventory{
				Panes:         []Pane{livePane},
				FleetRootPIDs: []int{herdrPID},
				Processes: append(append([]Process{}, liveProcesses...),
					process(900, 1, "claude.exe", `claude --dangerously-skip-permissions`, fixtureLatest),
				),
			},
			want:      OrphanProcess,
			wantCount: 1,
			assert: func(t *testing.T, findings []Finding) {
				if !strings.Contains(findings[0].Hold, "Herdr ancestry") {
					t.Fatalf("hold = %q, want an attribution refusal", findings[0].Hold)
				}
			},
		},
		{
			name: "the sweep never reports the session it runs inside",
			inventory: Inventory{
				SelfPIDs:      []int{31032, 400},
				FleetRootPIDs: []int{herdrPID},
				Processes: []Process{
					process(400, 1, "powershell.exe", `powershell`, fixtureLater),
					process(31032, 400, "claude.exe", `claude --dangerously-skip-permissions`, fixtureLatest),
				},
			},
			want:      OrphanProcess,
			wantCount: 0,
		},
		{
			name: "stale server is rooted in a finished task's worktree",
			inventory: Inventory{
				Panes:         []Pane{livePane},
				FleetRootPIDs: []int{herdrPID},
				Tasks:         []Task{task("pp-money", `C:\dev\pp\.worktrees\gb-pp-money`, "pane-gone", "done")},
				Worktrees:     []WorktreeDir{{Path: `C:\dev\pp\.worktrees\gb-pp-money`, Project: `C:\dev\pp`, TaskID: "pp-money"}},
				Processes: append(append([]Process{}, liveProcesses...),
					process(555, 1, "node.exe", `node C:\dev\pp\.worktrees\gb-pp-money\node_modules\next\dist\bin\next dev`, fixtureLatest),
				),
			},
			want:      StaleServer,
			wantCount: 1,
			assert: func(t *testing.T, findings []Finding) {
				if findings[0].PID != 555 || findings[0].TaskID != "pp-money" {
					t.Fatalf("finding = %+v, want pid 555 for pp-money", findings[0])
				}
			},
		},
		{
			name: "a server in a working task's worktree is doing its job",
			inventory: Inventory{
				Panes:         []Pane{livePane},
				FleetRootPIDs: []int{herdrPID},
				Tasks:         []Task{task("pp-money", `C:\dev\pp\.worktrees\gb-pp-money`, "pane-live", "working")},
				Worktrees:     []WorktreeDir{{Path: `C:\dev\pp\.worktrees\gb-pp-money`, Project: `C:\dev\pp`, TaskID: "pp-money"}},
				Processes: append(append([]Process{}, liveProcesses...),
					process(555, 1, "node.exe", `node C:\dev\pp\.worktrees\gb-pp-money\node_modules\vite\bin\vite.js`, fixtureLatest),
				),
			},
			want:      StaleServer,
			wantCount: 0,
		},
		{
			name: "orphan worktree has no live pane and no record",
			inventory: Inventory{
				Panes:     []Pane{livePane},
				Processes: liveProcesses,
				Worktrees: []WorktreeDir{{Path: `C:\dev\pd\.worktrees\gb-pdocs-help-docs`, Project: `C:\dev\pd`, TaskID: "pdocs-help-docs"}},
			},
			want:      OrphanWorktree,
			wantCount: 1,
			assert: func(t *testing.T, findings []Finding) {
				if !strings.Contains(findings[0].Detail, "no metadata record") {
					t.Fatalf("detail = %q, want the missing-record reason", findings[0].Detail)
				}
			},
		},
		{
			name: "a worktree whose task is still working is left alone",
			inventory: Inventory{
				Panes:     []Pane{livePane},
				Processes: liveProcesses,
				Tasks:     []Task{task("live", `C:\dev\pd\.worktrees\gb-live`, "pane-live", "working")},
				Worktrees: []WorktreeDir{{Path: `C:\dev\pd\.worktrees\gb-live`, Project: `C:\dev\pd`, TaskID: "live"}},
			},
			want:      OrphanWorktree,
			wantCount: 0,
		},
		{
			name: "orphan meta has no pane, no process and no worktree",
			inventory: Inventory{
				Panes:     []Pane{livePane},
				Processes: liveProcesses,
				Tasks:     []Task{task("utah", `C:\dev\pd\.worktrees\gb-utah`, "pane-gone", "done")},
			},
			want:      OrphanMeta,
			wantCount: 1,
			assert: func(t *testing.T, findings []Finding) {
				if findings[0].TaskID != "utah" || findings[0].Hold != "" {
					t.Fatalf("finding = %+v, want an unheld utah record", findings[0])
				}
			},
		},
		{
			name: "a meta whose worktree still exists belongs to the worktree class",
			inventory: Inventory{
				Panes:     []Pane{livePane},
				Processes: liveProcesses,
				Tasks:     []Task{task("utah", `C:\dev\pd\.worktrees\gb-utah`, "pane-gone", "done")},
				Worktrees: []WorktreeDir{{Path: `C:\dev\pd\.worktrees\gb-utah`, Project: `C:\dev\pd`, TaskID: "utah"}},
			},
			want:      OrphanMeta,
			wantCount: 0,
		},
		{
			name: "a non-terminal meta is reported but held",
			inventory: Inventory{
				Panes:     []Pane{livePane},
				Processes: liveProcesses,
				Tasks:     []Task{task("wedged", `C:\dev\pd\.worktrees\gb-wedged`, "pane-gone", "working")},
			},
			want:      OrphanMeta,
			wantCount: 1,
			assert: func(t *testing.T, findings []Finding) {
				if !strings.Contains(findings[0].Hold, "terminal status") {
					t.Fatalf("hold = %q, want the terminal-status refusal", findings[0].Hold)
				}
			},
		},
		{
			name:      "orphan status is a log with no meta",
			inventory: Inventory{OrphanStatusIDs: []string{"scout-old"}},
			want:      OrphanStatus,
			wantCount: 1,
			assert: func(t *testing.T, findings []Finding) {
				if findings[0].TaskID != "scout-old" {
					t.Fatalf("task = %q, want scout-old", findings[0].TaskID)
				}
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			findings := classOf(Classify(testCase.inventory), testCase.want)
			if len(findings) != testCase.wantCount {
				t.Fatalf("got %d %s findings, want %d: %+v", len(findings), testCase.want, testCase.wantCount, findings)
			}
			if testCase.assert != nil {
				testCase.assert(t, findings)
			}
		})
	}
}

// TestClassifyLiveFleetIsNeverReported is the whole point of the pane
// cross-reference: a goblin with a pane, an agent and a running harness must
// produce nothing at all, whatever else is on the machine.
func TestClassifyLiveFleetIsNeverReported(t *testing.T) {
	inventory := Inventory{
		Panes:         []Pane{{ID: "pane-live", ShellPID: 200, ForegroundPID: 300, HasAgent: true}},
		FleetRootPIDs: []int{100},
		Tasks:         []Task{task("live", `C:\dev\pd\.worktrees\gb-live`, "pane-live", "working")},
		Worktrees:     []WorktreeDir{{Path: `C:\dev\pd\.worktrees\gb-live`, Project: `C:\dev\pd`, TaskID: "live"}},
		Processes: []Process{
			process(100, 1, "herdr.exe", "herdr server", fixtureStart),
			process(200, 100, "powershell.exe", "powershell", fixtureLater),
			process(300, 200, "claude.exe", "claude --dangerously-skip-permissions", fixtureLatest),
			process(301, 300, "node.exe", `node C:\dev\pd\.worktrees\gb-live\node_modules\next\dist\bin\next dev`, fixtureLatest),
		},
	}
	if findings := Classify(inventory); len(findings) != 0 {
		t.Fatalf("a healthy fleet produced findings: %+v", findings)
	}
}

// TestDescendantsRejectsPIDReuse proves the downward walk will not adopt a
// process that predates its recorded parent. A reused pid is how a sweep would
// otherwise decide an unrelated process is fleet-supervised.
func TestDescendantsRejectsPIDReuse(t *testing.T) {
	processes := []Process{
		process(200, 100, "powershell.exe", "powershell", fixtureLatest),
		process(400, 200, "claude.exe", "claude --dangerously-skip-permissions", fixtureStart),
	}
	found := descendants(processes, []int{200})
	if found[400] {
		t.Fatal("a child that started before its recorded parent was adopted as a descendant")
	}
}

// TestHarnessSignaturesMatchAdapters keeps this package's launch signatures
// tied to the adapters that actually produce them. A flag renamed in
// internal/harness without a matching change here would silently blind the
// sweep to every goblin of that harness.
func TestHarnessSignaturesMatchAdapters(t *testing.T) {
	spec := harness.LaunchSpec{BriefPath: `C:\brief.md`, TaskTmp: `C:\tmp`}
	registry := harness.DefaultRegistry()
	for kind, signature := range map[harness.Kind]string{
		harness.Claude: "--dangerously-skip-permissions",
		harness.Codex:  "--dangerously-bypass-approvals-and-sandbox",
	} {
		adapter, err := registry.Get(kind)
		if err != nil {
			t.Fatalf("registry.Get(%s): %v", kind, err)
		}
		launch, err := adapter.Build(spec)
		if err != nil {
			t.Fatalf("build %s: %v", kind, err)
		}
		if !contains(launch.Args, signature) {
			t.Fatalf("%s launch args %q no longer carry the sweep signature %q", kind, launch.Args, signature)
		}
		if !contains(harnessSignatures, signature) {
			t.Fatalf("harnessSignatures is missing %q", signature)
		}
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestDecodeProcesses(t *testing.T) {
	t.Run("array", func(t *testing.T) {
		processes, err := decodeProcesses([]byte(`[{"pid":4,"ppid":1,"name":"a.exe","cmd":"a","start":"2026-09-05T07:14:51.0000000Z"}]`))
		if err != nil {
			t.Fatal(err)
		}
		if len(processes) != 1 || processes[0].PID != 4 || processes[0].Start.Minute() != 14 {
			t.Fatalf("decoded %+v", processes)
		}
	})
	t.Run("single object is wrapped", func(t *testing.T) {
		processes, err := decodeProcesses([]byte(`{"pid":4,"ppid":1,"name":"a.exe","cmd":"a","start":""}`))
		if err != nil {
			t.Fatal(err)
		}
		if len(processes) != 1 || !processes[0].Start.IsZero() {
			t.Fatalf("decoded %+v", processes)
		}
	})
	t.Run("empty output is an error", func(t *testing.T) {
		if _, err := decodeProcesses([]byte("  \n")); err == nil {
			t.Fatal("empty output decoded without error")
		}
	})
}

func TestFindingsDigestIsOrderIndependent(t *testing.T) {
	first := []Finding{{Class: OrphanProcess, PID: 2}, {Class: OrphanMeta, TaskID: "a"}}
	second := []Finding{{Class: OrphanMeta, TaskID: "a"}, {Class: OrphanProcess, PID: 2}}
	if FindingsDigest(first) != FindingsDigest(second) {
		t.Fatal("digest depends on finding order")
	}
	third := append([]Finding{}, first...)
	third = append(third, Finding{Class: OrphanProcess, PID: 3})
	if FindingsDigest(first) == FindingsDigest(third) {
		t.Fatal("a new finding did not change the digest")
	}
}

// TestUnresolvedPaneHoldsEveryProcessFinding: a pane that exists but cannot
// say what is running in it leaves a live goblin indistinguishable from an
// orphan, so nothing may be killed on that evidence.
func TestUnresolvedPaneHoldsEveryProcessFinding(t *testing.T) {
	inventory := Inventory{
		Panes:           []Pane{{ID: "pane-a", HasAgent: true}},
		UnresolvedPanes: []string{"pane-a"},
		FleetRootPIDs:   []int{100},
		Tasks:           []Task{task("old", `C:\dev\pd\.worktrees\gb-old`, "pane-b", "done")},
		Worktrees:       []WorktreeDir{{Path: `C:\dev\pd\.worktrees\gb-old`, Project: `C:\dev\pd`, TaskID: "old"}},
		Processes: []Process{
			process(100, 1, "herdr.exe", "herdr server", fixtureStart),
			process(400, 100, "powershell.exe", "powershell", fixtureLater),
			process(31032, 400, "claude.exe", "claude --dangerously-skip-permissions", fixtureLatest),
			process(555, 1, "node.exe", `node C:\dev\pd\.worktrees\gb-old\node_modules\vite\bin\vite.js`, fixtureLatest),
		},
	}
	findings := Classify(inventory)
	for _, class := range []Class{OrphanProcess, StaleServer} {
		matched := classOf(findings, class)
		if len(matched) != 1 {
			t.Fatalf("got %d %s findings, want 1", len(matched), class)
		}
		if !strings.Contains(matched[0].Hold, "process identity") {
			t.Fatalf("%s hold = %q, want the unresolved-pane refusal", class, matched[0].Hold)
		}
	}
}
