package reap

import (
	"bytes"
	"os/exec"
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
				// The one thing holding a fleet-descended orphan is the
				// authorisation its kill needs, carried from the moment it is
				// classified so the line states it even when something else
				// holds the finding too.
				want := killNeedsItsOwnPID + ". Name 31032 with --force to take responsibility for it"
				if findings[0].Hold() != want {
					t.Fatalf("hold = %q, want only the kill authorisation keyed to the pid: %q", findings[0].Hold(), want)
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
				if !strings.Contains(findings[0].Hold(), "Herdr ancestry") {
					t.Fatalf("hold = %q, want an attribution refusal", findings[0].Hold())
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
				Worktrees:     []WorktreeDir{{Path: `C:\dev\pp\.worktrees\gb-pp-money`, Project: `C:\dev\pp`, Registration: RegistrationListed, TaskID: "pp-money"}},
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
				Worktrees:     []WorktreeDir{{Path: `C:\dev\pp\.worktrees\gb-pp-money`, Project: `C:\dev\pp`, Registration: RegistrationListed, TaskID: "pp-money"}},
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
				Worktrees: []WorktreeDir{{Path: `C:\dev\pd\.worktrees\gb-pdocs-help-docs`, Project: `C:\dev\pd`, Registration: RegistrationListed, TaskID: "pdocs-help-docs"}},
			},
			want:      OrphanWorktree,
			wantCount: 1,
			assert: func(t *testing.T, findings []Finding) {
				// There was no record to ask about, which is the whole of what
				// this sweep established, said once.
				if findings[0].Detail != "no task record to ask about" {
					t.Fatalf("detail = %q, want the missing-record reason and nothing restated", findings[0].Detail)
				}
			},
		},
		{
			name: "a worktree whose task is still working is left alone",
			inventory: Inventory{
				Panes:     []Pane{livePane},
				Processes: liveProcesses,
				Tasks:     []Task{task("live", `C:\dev\pd\.worktrees\gb-live`, "pane-live", "working")},
				Worktrees: []WorktreeDir{{Path: `C:\dev\pd\.worktrees\gb-live`, Project: `C:\dev\pd`, Registration: RegistrationListed, TaskID: "live"}},
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
				if findings[0].TaskID != "utah" || findings[0].Hold() != "" {
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
				Worktrees: []WorktreeDir{{Path: `C:\dev\pd\.worktrees\gb-utah`, Project: `C:\dev\pd`, Registration: RegistrationListed, TaskID: "utah"}},
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
				if !strings.Contains(findings[0].Hold(), "terminal status") {
					t.Fatalf("hold = %q, want the terminal-status refusal", findings[0].Hold())
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
		Worktrees:     []WorktreeDir{{Path: `C:\dev\pd\.worktrees\gb-live`, Project: `C:\dev\pd`, Registration: RegistrationListed, TaskID: "live"}},
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
	spec := harness.LaunchSpec{BriefPath: `C:\brief.md`, TaskTmp: `C:\tmp`, GoTmp: `C:\gotmp\task`}
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

// The failure this guards: Go spawns powershell with a pipe, PowerShell encodes
// stdout with the OEM code page, and a command line holding a character outside
// it comes back mangled. U+00A7 SECTION SIGN becomes byte 0x15 under IBM437,
// which encoding/json rejects, failing the whole listing and the orphan sweep.
// The prelude is the fix; escaping the byte instead would decode but would
// report a command line the process does not have, and reap decides what may be
// killed by reading command lines.
func TestProcessListingSurvivesANonASCIICommandLine(t *testing.T) {
	if _, err := exec.LookPath("powershell"); err != nil {
		t.Skip("powershell is required to exercise the child output encoding")
	}
	const row = `[pscustomobject]@{ pid = 4; ppid = 1; name = "a.exe"; cmd = "serve " + [char]0x00A7 + " end"; start = "" } | ConvertTo-Json -Compress`

	t.Run("without the prelude the listing is undecodable", func(t *testing.T) {
		out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", row).Output()
		if err != nil {
			t.Fatalf("powershell: %v", err)
		}
		if !bytes.Contains(out, []byte{0x15}) {
			t.Skipf("this host does not mangle U+00A7 into 0x15 (output %q); the prelude is still correct", out)
		}
		if _, err := decodeProcesses(out); err == nil {
			t.Fatal("a mangled listing decoded without error; a wrong command line must not be reported as the real one")
		}
	})

	t.Run("with the prelude the character survives", func(t *testing.T) {
		out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", utf8OutputPrelude+row).Output()
		if err != nil {
			t.Fatalf("powershell: %v", err)
		}
		processes, err := decodeProcesses(out)
		if err != nil {
			t.Fatalf("decodeProcesses: %v", err)
		}
		if len(processes) != 1 || processes[0].CommandLine != "serve § end" {
			t.Fatalf("decoded %+v, want the section sign preserved", processes)
		}
	})
}

// A raw control byte still means the child output encoding regressed, so the
// decoder must fail loudly rather than guess at what the byte used to be:
// 0x15 could be a mangled U+00A7 or a genuine NAK, and reap cannot tell.
func TestDecodeProcessesRefusesARawControlByte(t *testing.T) {
	raw := []byte(`[{"pid":4,"ppid":1,"name":"a.exe","cmd":"serve X end","start":""}]`)
	raw[bytes.IndexByte(raw, 'X')] = 0x15
	if _, err := decodeProcesses(raw); err == nil {
		t.Fatal("a raw 0x15 decoded without error; want the listing refused so the encoding regression is visible")
	} else if !strings.Contains(err.Error(), `\x15`) {
		t.Errorf("error = %v, want it to name the offending byte", err)
	}
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
		Worktrees:       []WorktreeDir{{Path: `C:\dev\pd\.worktrees\gb-old`, Project: `C:\dev\pd`, Registration: RegistrationListed, TaskID: "old"}},
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
		if !strings.Contains(matched[0].Hold(), "process identity") {
			t.Fatalf("%s hold = %q, want the unresolved-pane refusal", class, matched[0].Hold())
		}
	}
	// The remedy here is fixing Herdr, and the refusal says so in its own
	// words. What the rendered line must not do is tell the operator that
	// something still stands after the --force it names, because this refusal
	// answers to the same pid the kill does and that force clears both.
	for _, finding := range classOf(findings, OrphanProcess) {
		if strings.Contains(finding.Hold(), "resolve it rather than overriding it") {
			t.Errorf("hold = %q, claims evidence survives a --force that clears every refusal on the line", finding.Hold())
		}
	}
}

// TestClassifyProcessPopulations is the fix for four identical sweeps in
// ninety minutes, fourteen findings each, every one a false positive. The
// image name says nothing on this machine: claude.exe is the Overlord's
// desktop application a dozen times over and a reviewer of a round in
// progress, as often as it is a fleet harness. The parent process and the
// command line are what tell them apart, and only the last case here is
// something the sweep has any business reporting.
func TestClassifyProcessPopulations(t *testing.T) {
	const (
		herdrPID   = 100
		desktopPID = 700
		gatePID    = 800
	)
	const desktopExe = `"C:\Program Files\WindowsApps\Claude_2.2553.1.0_x64__pzs8sxrjxfjjc\app\claude.exe"`
	// One machine, all four populations at once, because that is how they
	// actually appear and a fixture with one at a time would not prove the
	// classifier keeps them apart.
	processes := []Process{
		process(herdrPID, 1, "herdr.exe", "herdr server", fixtureStart),
		// The desktop application: one parent under sihost, then the Chromium
		// children it spawns, each carrying a --type= switch.
		process(desktopPID, 7996, "claude.exe", desktopExe+" ", fixtureStart),
		process(701, desktopPID, "claude.exe", desktopExe+" --type=renderer --user-data-dir=...", fixtureLater),
		process(702, desktopPID, "claude.exe", desktopExe+" --type=gpu-process --gpu-preferences=...", fixtureLater),
		process(703, desktopPID, "claude.exe", desktopExe+" --type=crashpad-handler --user-data-dir=...", fixtureLater),
		// A no-mistakes review round: the daemon and the reviewer it launched.
		process(gatePID, 1, "no-mistakes.exe", `no-mistakes.exe daemon run --root C:\Users\x\.no-mistakes`, fixtureStart),
		process(801, gatePID, "claude.exe", `claude --model opus --effort high -p --verbose --output-format stream-json --json-schema "{}"`, fixtureLater),
		// The real thing: a harness that descends from the Herdr server and
		// has no pane left.
		process(400, herdrPID, "powershell.exe", "powershell -NoExit", fixtureLater),
		process(31032, 400, "claude.exe", `claude --dangerously-skip-permissions --strict-mcp-config`, fixtureLatest),
	}
	findings := classOf(Classify(Inventory{FleetRootPIDs: []int{herdrPID}, Processes: processes}), OrphanProcess)

	reported := make(map[int]Finding, len(findings))
	for _, finding := range findings {
		reported[finding.PID] = finding
	}
	for _, unwanted := range []struct {
		pid        int
		population string
	}{
		{desktopPID, "the desktop application the Overlord is using"},
		{701, "a desktop application renderer child"},
		{702, "a desktop application gpu-process child"},
		{703, "a desktop application crashpad-handler child"},
		{801, "a gate agent reviewing for a goblin that is working"},
	} {
		if finding, ok := reported[unwanted.pid]; ok {
			t.Errorf("pid %d (%s) was reported: %s", unwanted.pid, unwanted.population, finding.Line())
		}
	}
	orphan, ok := reported[31032]
	if !ok {
		t.Fatalf("the genuine orphan was not reported; findings: %+v", findings)
	}
	if want := killNeedsItsOwnPID + ". Name 31032 with --force to take responsibility for it"; orphan.Hold() != want {
		t.Errorf("hold = %q, want the genuine orphan held by nothing but its kill authorisation: %q", orphan.Hold(), want)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d orphan_process findings, want only the genuine orphan: %+v", len(findings), findings)
	}
}

// TestUnidentifiedHarnessSaysWhatCouldNotBeDetermined: the fourteen false
// positives all carried a HELD line that read as a judgement to override, and
// pid 900 here is the population behind them, the Overlord's application or a
// live review agent. Every process finding now names the pid its kill answers
// to, so the line cannot stop mentioning --force; the refusal carries its own
// remedy in its own words, and the line may not claim that identifying the
// process still stands after a force that in fact ends it.
func TestUnidentifiedHarnessSaysWhatCouldNotBeDetermined(t *testing.T) {
	inventory := Inventory{
		FleetRootPIDs: []int{100},
		Processes: []Process{
			process(100, 1, "herdr.exe", "herdr server", fixtureStart),
			process(900, 1, "claude.exe", `claude --dangerously-skip-permissions`, fixtureLatest),
		},
	}
	finding := classOf(Classify(inventory), OrphanProcess)[0]
	if !strings.Contains(finding.Hold(), "identify it before anything acts on it") {
		t.Errorf("hold = %q, want it to say what could not be determined and what answers that", finding.Hold())
	}
	if strings.Contains(finding.Hold(), "resolve it rather than overriding it") {
		t.Errorf("hold = %q, claims the identification survives --force 900, which clears every refusal on the line and kills the tree", finding.Hold())
	}
}

// TestUnregisteredDirectoryIsNotAWorktree: a directory under .worktrees/ that
// the project does not register is a shell a dead task left behind. Calling it
// a worktree is what made the sweep ask git questions inside it, and git
// answers those from the enclosing repository.
func TestUnregisteredDirectoryIsNotAWorktree(t *testing.T) {
	inventory := Inventory{
		Worktrees: []WorktreeDir{{Path: `C:\dev\pd\.worktrees\gb-dead`, Project: `C:\dev\pd`, Registration: RegistrationUnlisted, TaskID: "dead"}},
	}
	findings := Classify(inventory)
	if worktrees := classOf(findings, OrphanWorktree); len(worktrees) != 0 {
		t.Fatalf("an unregistered directory was classified as a worktree: %+v", worktrees)
	}
	directories := classOf(findings, OrphanDirectory)
	if len(directories) != 1 {
		t.Fatalf("got %d orphan_directory findings, want 1: %+v", len(directories), findings)
	}
	if !strings.Contains(directories[0].Detail, "git worktree list") {
		t.Errorf("detail = %q, want it to name the premise that failed", directories[0].Detail)
	}
	if !strings.Contains(directories[0].Detail, "working directory is not readable") {
		t.Errorf("detail = %q, want it to say what it could not determine about who holds the directory", directories[0].Detail)
	}
}

// TestUnregisteredDirectoryNamesTheProcessHoldingIt: when a command line does
// name the shell, that process is the leak the sweep exists to catch, and the
// directory is held because it is the process that has to be dealt with.
func TestUnregisteredDirectoryNamesTheProcessHoldingIt(t *testing.T) {
	inventory := Inventory{
		Worktrees: []WorktreeDir{{Path: `C:\dev\pd\.worktrees\gb-dead`, Project: `C:\dev\pd`, Registration: RegistrationUnlisted, TaskID: "dead"}},
		Processes: []Process{
			process(4242, 1, "node.exe", `node C:\dev\pd\.worktrees\gb-dead\scripts\watch.js`, fixtureLatest),
		},
	}
	finding := classOf(Classify(inventory), OrphanDirectory)[0]
	if finding.PID != 4242 {
		t.Fatalf("finding = %+v, want pid 4242 named", finding)
	}
	if !strings.Contains(finding.Hold(), "that process is the leak") {
		t.Errorf("hold = %q, want the process named as the leak", finding.Hold())
	}
}

// TestDirectoryIsNotBlamedOnANeighbourWithALongerName: task ids are names the
// operator chooses, so one is routinely a prefix of another. A process working
// in gb-old-2 is not the process holding gb-old open, and saying it is names
// the wrong pid as the leak and leaves a removable shell in place.
func TestDirectoryIsNotBlamedOnANeighbourWithALongerName(t *testing.T) {
	inventory := Inventory{
		Worktrees: []WorktreeDir{
			{Path: `C:\dev\pd\.worktrees\gb-old`, Project: `C:\dev\pd`, Registration: RegistrationUnlisted, TaskID: "old"},
			{Path: `C:\dev\pd\.worktrees\gb-old-2`, Project: `C:\dev\pd`, Registration: RegistrationUnlisted, TaskID: "old-2"},
		},
		Processes: []Process{
			process(4242, 1, "node.exe", `node C:\dev\pd\.worktrees\gb-old-2\node_modules\vite\bin\vite.js`, fixtureLatest),
		},
	}
	findings := classOf(Classify(inventory), OrphanDirectory)
	byTask := make(map[string]Finding, len(findings))
	for _, finding := range findings {
		byTask[finding.TaskID] = finding
	}
	if got := byTask["old"]; got.PID != 0 {
		t.Errorf("gb-old was blamed on pid %d, which is working in gb-old-2: %s", got.PID, got.Line())
	}
	if got := byTask["old-2"]; got.PID != 4242 {
		t.Errorf("gb-old-2 finding = %+v, want pid 4242 named", got)
	}
	// The same unanchored match decides which worktree a stale server belongs
	// to, so the neighbour must not collect the server either.
	for _, server := range classOf(Classify(inventory), StaleServer) {
		if server.TaskID == "old" {
			t.Errorf("a server running in gb-old-2 was attributed to gb-old: %s", server.Line())
		}
	}
}

// TestLiveGoblinOutranksUnconfirmedRegistration: git worktree list can fail
// for reasons that say nothing about the directory (git missing, an index
// lock, a root that is not a repository), and every directory under that root
// then has an unknown registration. A pane holding an agent right now is
// harder evidence than that, so it wins.
func TestLiveGoblinOutranksUnconfirmedRegistration(t *testing.T) {
	inventory := Inventory{
		Panes: []Pane{{ID: "pane-live", ShellPID: 200, ForegroundPID: 300, HasAgent: true}},
		Tasks: []Task{task("live", `C:\dev\pd\.worktrees\gb-live`, "pane-live", "working")},
		// The zero value: the repository could not be asked, so nothing about
		// this directory was established.
		Worktrees: []WorktreeDir{{Path: `C:\dev\pd\.worktrees\gb-live`, Project: `C:\dev\pd`, TaskID: "live"}},
	}
	if findings := Classify(inventory); len(findings) != 0 {
		t.Fatalf("a live goblin's worktree was reported: %+v", findings)
	}
}

// TestUnknownRegistrationIsAWorktreeNotARemovableDirectory: a sweep that could
// not ask the repository has established nothing, and must not tell the
// operator that a worktree holding real work is a directory a dead task left
// behind. The unknown case therefore takes the gated class, which is the one
// the work gate protects.
func TestUnknownRegistrationIsAWorktreeNotARemovableDirectory(t *testing.T) {
	inventory := Inventory{
		Tasks:     []Task{task("utah", `C:\dev\pd\.worktrees\gb-utah`, "pane-gone", "done")},
		Worktrees: []WorktreeDir{{Path: `C:\dev\pd\.worktrees\gb-utah`, Project: `C:\dev\pd`, TaskID: "utah"}},
	}
	findings := Classify(inventory)
	if directories := classOf(findings, OrphanDirectory); len(directories) != 0 {
		t.Fatalf("an unconfirmable directory was offered up for removal: %+v", directories)
	}
	worktrees := classOf(findings, OrphanWorktree)
	if len(worktrees) != 1 || worktrees[0].Action != "return the worktree through cfo cleanup" {
		t.Fatalf("orphan_worktree findings = %+v, want one routed through cleanup", worktrees)
	}
	// The record must not be force-archived out from under a directory that
	// is still there and was never established to be anything.
	if metas := classOf(findings, OrphanMeta); len(metas) != 0 {
		t.Fatalf("the task record was reported alongside its directory: %+v", metas)
	}
}

// TestOneDeadTaskReportsOneResourceAtATime: a shell and the record behind it
// are one leak, not two. Reporting both in one sweep made --apply remove the
// directory and then archive a record whose worktree path no longer resolves,
// which fails and recurs forever. The record waits for the sweep after the
// directory is gone.
func TestOneDeadTaskReportsOneResourceAtATime(t *testing.T) {
	const shell = `C:\dev\pd\.worktrees\gb-dead`
	inventory := Inventory{
		Tasks:     []Task{task("dead", shell, "pane-gone", "done")},
		Worktrees: []WorktreeDir{{Path: shell, Project: `C:\dev\pd`, Registration: RegistrationUnlisted, TaskID: "dead"}},
	}
	findings := Classify(inventory)
	if len(findings) != 1 || findings[0].Class != OrphanDirectory || findings[0].Path != shell {
		t.Fatalf("findings = %+v, want only the orphan_directory at %q", findings, shell)
	}

	// The next sweep, with the shell removed.
	inventory.Worktrees = nil
	findings = Classify(inventory)
	if len(findings) != 1 || findings[0].Class != OrphanMeta || findings[0].TaskID != "dead" {
		t.Fatalf("findings = %+v, want only the orphan_meta for dead", findings)
	}
}

// TestUnlistedShellOfAnUnfinishedTaskIsHeld: --apply never reaps a task that
// has not finished, and a shell is a task resource like any other.
func TestUnlistedShellOfAnUnfinishedTaskIsHeld(t *testing.T) {
	const shell = `C:\dev\pd\.worktrees\gb-wedged`
	inventory := Inventory{
		Tasks:     []Task{task("wedged", shell, "pane-gone", "working")},
		Worktrees: []WorktreeDir{{Path: shell, Project: `C:\dev\pd`, Registration: RegistrationUnlisted, TaskID: "wedged"}},
	}
	finding := classOf(Classify(inventory), OrphanDirectory)[0]
	if !strings.Contains(finding.Hold(), "terminal status") {
		t.Fatalf("hold = %q, want the terminal-status refusal", finding.Hold())
	}
	if !strings.Contains(finding.Hold(), "working") {
		t.Errorf("hold = %q, want it to name the latest verb", finding.Hold())
	}
}

// TestAnUnreadableTaskRecordHoldsItsDirectory is the same class of defect this
// branch exists to remove, found by auditing every error branch in the package
// rather than by a report. A meta that cannot be read is dropped from the task
// list, and a directory whose task is absent classifies as "has no metadata
// record", which carries no hold at all. A record the sweep could not read
// therefore produced a more actionable finding than one it read and found
// unfinished. Whether that task finished is exactly what is unknown, so it is
// gated.
func TestAnUnreadableTaskRecordHoldsItsDirectory(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		registration Registration
		class        Class
		// The worktree detail states the task outcome, so it must not call an
		// unreadable record an absent one. The directory detail is about the
		// directory, and carries the record state in its hold instead.
		detailNamesTheRecord bool
	}{
		{"a registered worktree", RegistrationListed, OrphanWorktree, true},
		{"an unlisted directory", RegistrationUnlisted, OrphanDirectory, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			inventory := Inventory{
				UnreadableTasks: []string{"broken"},
				Worktrees: []WorktreeDir{{
					Path:         `C:\dev\pd\.worktrees\gb-broken`,
					Project:      `C:\dev\pd`,
					TaskID:       "broken",
					Registration: testCase.registration,
				}},
			}
			findings := classOf(Classify(inventory), testCase.class)
			if len(findings) != 1 {
				t.Fatalf("got %d %s findings, want 1: %+v", len(findings), testCase.class, findings)
			}
			if !strings.Contains(findings[0].Hold(), "could not be read") {
				t.Fatalf("hold = %q, want a refusal naming the unreadable record", findings[0].Hold())
			}
			if testCase.detailNamesTheRecord && !strings.Contains(findings[0].Detail, "could not be read") {
				t.Fatalf("detail = %q, want it to say the record was unreadable rather than absent", findings[0].Detail)
			}
		})
	}
}

// TestAnUnreadableTaskRecordHoldsItsServer: killing a dev server says the task
// behind it is over, and an unreadable record is the one thing that cannot say
// so. Without this, the server of a task whose meta went unreadable is more
// killable than one the sweep read and found still working.
func TestAnUnreadableTaskRecordHoldsItsServer(t *testing.T) {
	inventory := Inventory{
		UnreadableTasks: []string{"broken"},
		Worktrees: []WorktreeDir{{
			Path:         `C:\dev\pd\.worktrees\gb-broken`,
			Project:      `C:\dev\pd`,
			TaskID:       "broken",
			Registration: RegistrationListed,
		}},
		Processes: []Process{
			process(555, 1, "node.exe", `node C:\dev\pd\.worktrees\gb-broken\node_modules\vite\bin\vite.js`, fixtureLatest),
		},
	}
	findings := classOf(Classify(inventory), StaleServer)
	if len(findings) != 1 {
		t.Fatalf("got %d stale_server findings, want 1: %+v", len(findings), findings)
	}
	if !strings.Contains(findings[0].Hold(), "could not be read") {
		t.Fatalf("hold = %q, want a refusal naming the unreadable record", findings[0].Hold())
	}
}

// TestAHoldNamesWhatActuallyClearsIt is the defect this whole sweep exists to
// stop reporting, appearing inside its own fix: the reaper's HELD text named a
// --force that would have closed the desktop application and killed three live
// review agents, and a hold that needs two keys while telling the operator one
// will do is the same lie in a new place. The line is derived from the
// refusals rather than written at each site, so it cannot drift from them.
func TestAHoldNamesWhatActuallyClearsIt(t *testing.T) {
	t.Run("one key is named", func(t *testing.T) {
		finding := Finding{Class: OrphanWorktree, TaskID: "wedged"}
		finding.refuseUnlessForced("task has not reached a terminal status", "wedged")
		if !strings.Contains(finding.Hold(), "Name wedged with --force") {
			t.Fatalf("hold = %q, want the one key named", finding.Hold())
		}
	})

	t.Run("every key is named when a hold carries more than one refusal", func(t *testing.T) {
		finding := Finding{Class: OrphanDirectory, TaskID: "wedged", PID: 4242}
		finding.refuseUnlessForced("pid 4242 is still using this directory", "4242")
		finding.refuseUnlessForced("task has not reached a terminal status", "wedged")
		for _, key := range []string{"4242", "wedged"} {
			if !strings.Contains(finding.Hold(), key) {
				t.Fatalf("hold = %q, want it to name %q, which --force must also name", finding.Hold(), key)
			}
		}
		if !strings.Contains(finding.Hold(), "every one of") {
			t.Fatalf("hold = %q, want it to say that naming one is not enough", finding.Hold())
		}
	})

	t.Run("a refusal whose remedy is not force does not propose one", func(t *testing.T) {
		finding := Finding{Class: OrphanProcess, PID: 900}
		finding.refuseUntilEstablished(unidentifiedHold, "900")
		if strings.Contains(finding.Hold(), "--force") {
			t.Fatalf("hold = %q, want no --force proposed for something the sweep could not identify", finding.Hold())
		}
	})

	t.Run("a hold whose every refusal answers to a named key promises nothing beyond them", func(t *testing.T) {
		// Both refusals here answer to the pid, so the one --force the line
		// names clears the whole hold and ends the tree. Telling the operator
		// that the identification still stands would be the command describing
		// an outcome it will not produce.
		finding := Finding{Class: OrphanProcess, PID: 900}
		finding.refuseUntilEstablished(unidentifiedHold, "900")
		finding.refuseUnlessForced(killNeedsItsOwnPID, "900")
		hold := finding.Hold()
		if !strings.Contains(hold, "Name 900 with --force to take responsibility for it") {
			t.Fatalf("hold = %q, want the one key that clears every refusal on the line", hold)
		}
		if strings.Contains(hold, "resolve it rather than overriding it") {
			t.Fatalf("hold = %q, claims something survives a --force naming 900, which clears both refusals", hold)
		}
	})

	t.Run("a mixed hold names no outcome the force cannot deliver", func(t *testing.T) {
		// The unplaced agent answers to its pane and to neither key the line
		// names, so here something really does stand behind the --force.
		const worktree = `C:\dev\pd\.worktrees\gb-broken`
		inventory := Inventory{
			Panes:           []Pane{{ID: "pane-b", HasAgent: true}},
			UnplacedAgents:  []string{"pane-b"},
			UnreadableTasks: []string{"broken"},
			Worktrees:       []WorktreeDir{{Path: worktree, Project: `C:\dev\pd`, Registration: RegistrationListed, TaskID: "broken"}},
			Processes:       []Process{process(555, 1, "node.exe", `node `+worktree+`\node_modules\vite\bin\vite.js`, fixtureLatest)},
		}
		hold := classOf(Classify(inventory), StaleServer)[0].Hold()
		if !strings.Contains(hold, "Name every one of broken and 555 with --force") {
			t.Fatalf("hold = %q, want both keys a --force does answer: the task and the pid its kill needs", hold)
		}
		if strings.Contains(hold, "act on it") {
			t.Fatalf("hold = %q, want no promise that the sweep acts, because the agent refusal stands whatever is forced", hold)
		}
		if !strings.Contains(hold, "resolve it rather than overriding it") {
			t.Fatalf("hold = %q, want it to say the evidence has to be resolved rather than forced", hold)
		}
		// The correction that matters: an unestablished refusal IS cleared by
		// naming its key, so a line saying otherwise would be this command
		// describing what it wishes were true, which is the habit the branch
		// exists to break.
		if strings.Contains(hold, "answers to no --force") {
			t.Fatalf("hold = %q, want no claim that the evidence refusal cannot be forced, because naming its key does clear it", hold)
		}
	})

	t.Run("an absolute refusal says so", func(t *testing.T) {
		finding := Finding{Class: OrphanWorktree, TaskID: "old"}
		finding.refuseAbsolutely("worktree has 2 commit(s) on no remote")
		if !strings.Contains(finding.Hold(), "No --force clears this") {
			t.Fatalf("hold = %q, want it to say no force clears it", finding.Hold())
		}
	})
}

// TestARefusalCannotBeReplacedBySite is the root cause of three rounds of
// findings: refusals were composed by hand at each call site, so one that ran
// later silently replaced one already recorded. Adding is now the only way to
// record one.
func TestARefusalCannotBeReplacedBySite(t *testing.T) {
	finding := Finding{Class: StaleServer, TaskID: "broken", PID: 555}
	finding.refuseUntilEstablished("1 pane(s) could not report their process identity", "555")
	finding.refuseUnlessForced("its task record could not be read", "broken")
	if len(finding.Holds) != 2 {
		t.Fatalf("holds = %+v, want both refusals kept", finding.Holds)
	}
	for _, want := range []string{"could not report their process identity", "task record could not be read"} {
		if !strings.Contains(finding.Hold(), want) {
			t.Fatalf("hold = %q, want it to carry %q", finding.Hold(), want)
		}
	}
}

// TestALiveGoblinsServerIsNeverStale is the incident of 19 September 2026,
// after PR #27 landed. The sweep offered to kill pid 35012, the vite server of
// pd-1229-1187-landing, whose status log read done. The goblin was running
// browser tests against that exact server at the time. It had notified done
// for one pull request and carried on to the next under the same id, which is
// what a landing task does, so the record said a pull request had finished and
// the sweep read it as the goblin having finished.
func TestALiveGoblinsServerIsNeverStale(t *testing.T) {
	const worktree = `C:\dev\pd\.worktrees\gb-pd-landing`
	server := process(35012, 1, "node.exe", `node `+worktree+`\frontend\node_modules\vite\bin\vite.js`, fixtureLatest)
	landing := task("pd-landing", worktree, "w9:p8Y", "done")
	worktrees := []WorktreeDir{{Path: worktree, Project: `C:\dev\pd`, Registration: RegistrationListed, TaskID: "pd-landing"}}

	t.Run("a pane holding an agent says the goblin is working", func(t *testing.T) {
		inventory := Inventory{
			// The pane the task record names, with an agent on it: exactly
			// what herdr reported while the sweep offered up the server.
			Panes:     []Pane{{ID: "w9:p8Y", ShellPID: 900, ForegroundPID: 901, HasAgent: true}},
			Tasks:     []Task{landing},
			Worktrees: worktrees,
			Processes: []Process{server},
		}
		if findings := classOf(Classify(inventory), StaleServer); len(findings) != 0 {
			t.Fatalf("a live goblin's dev server was offered up: %+v", findings)
		}
	})

	t.Run("an agent working in the worktree says so too", func(t *testing.T) {
		inventory := Inventory{
			// The record's pane id no longer matches the pane the goblin is
			// in, so the only thing left to ask is where the agents are
			// working. One of them is working under this worktree.
			Panes: []Pane{
				{ID: "w9:p0", ShellPID: 800, HasAgent: true, AgentCwd: `C:\dev\code-goblins`},
				{ID: "w9:pMoved", ShellPID: 900, HasAgent: true, AgentCwd: worktree + `\frontend`},
			},
			Tasks:     []Task{landing},
			Worktrees: worktrees,
			Processes: []Process{server},
		}
		if findings := classOf(Classify(inventory), StaleServer); len(findings) != 0 {
			t.Fatalf("a server was offered up while an agent is working in its worktree: %+v", findings)
		}
	})

	t.Run("and the worktree it is working in is not an orphan either", func(t *testing.T) {
		inventory := Inventory{
			// The same drift, one class over, and the worse outcome: this
			// finding proposes returning the checkout the goblin is working
			// in, and cleanup's own guard cannot stop it, because that counts
			// panes matching the recorded pane id too.
			Panes:     []Pane{{ID: "w9:pMoved", ShellPID: 900, HasAgent: true, AgentCwd: worktree + `\frontend`}},
			Tasks:     []Task{landing},
			Worktrees: worktrees,
			Processes: []Process{server},
		}
		if findings := classOf(Classify(inventory), OrphanWorktree); len(findings) != 0 {
			t.Fatalf("a worktree an agent is working in was offered up for return: %+v", findings)
		}
	})

	t.Run("a neighbouring worktree with a longer name is not that agent's", func(t *testing.T) {
		inventory := Inventory{
			Panes:     []Pane{{ID: "w9:pOther", ShellPID: 900, HasAgent: true, AgentCwd: worktree + `-two`}},
			Tasks:     []Task{landing},
			Worktrees: worktrees,
			Processes: []Process{server},
		}
		if findings := classOf(Classify(inventory), StaleServer); len(findings) != 1 {
			t.Fatalf("got %d findings, want the abandoned server reported: %+v", len(findings), findings)
		}
	})

	t.Run("with the goblin gone it is reported, and done is not what establishes that", func(t *testing.T) {
		inventory := Inventory{
			Panes:     []Pane{{ID: "w9:pOther", ShellPID: 900, HasAgent: true}},
			Tasks:     []Task{landing},
			Worktrees: worktrees,
			Processes: []Process{server},
		}
		findings := classOf(Classify(inventory), StaleServer)
		if len(findings) != 1 {
			t.Fatalf("got %d stale_server findings, want the genuinely abandoned server: %+v", len(findings), findings)
		}
		want := killNeedsItsOwnPID + ". Name 35012 with --force to take responsibility for it"
		if findings[0].Hold() != want {
			t.Fatalf("hold = %q, want an abandoned server of a finished task held by nothing but its kill authorisation: %q", findings[0].Hold(), want)
		}
		if !strings.Contains(findings[0].Detail, "no pane holding an agent working there") {
			t.Fatalf("detail = %q, want it to state the evidence that established this", findings[0].Detail)
		}
	})

	t.Run("an agent that reported no working directory holds both classes", func(t *testing.T) {
		// Herdr declares an agent's working directory nullable, so one agent
		// answering with nothing is a state the fleet reaches without anything
		// being broken. With the record's pane id drifted, that agent is the
		// only thing that could still place this goblin, and it might be it.
		inventory := Inventory{
			Panes:          []Pane{{ID: "w9:pMoved", ShellPID: 900, HasAgent: true}},
			UnplacedAgents: []string{"w9:pMoved"},
			Tasks:          []Task{landing},
			Worktrees:      worktrees,
			Processes:      []Process{server},
		}
		findings := Classify(inventory)
		for _, class := range []Class{StaleServer, OrphanWorktree} {
			found := classOf(findings, class)
			if len(found) != 1 {
				t.Fatalf("got %d %s findings, want one: %+v", len(found), class, findings)
			}
			if !strings.Contains(found[0].Hold(), "reported no working directory") {
				t.Errorf("%s hold = %q, want it held because an agent could not be placed", class, found[0].Hold())
			}
			if strings.Contains(found[0].Detail, "no pane holding an agent working there") {
				t.Errorf("%s detail = %q, claims the evidence its own hold says could not be gathered", class, found[0].Detail)
			}
		}
	})

	t.Run("a task that never said done is reported but held", func(t *testing.T) {
		working := task("pd-landing", worktree, "w9:pGone", "working")
		inventory := Inventory{
			Tasks:     []Task{working},
			Worktrees: worktrees,
			Processes: []Process{server},
		}
		findings := classOf(Classify(inventory), StaleServer)
		if len(findings) != 1 {
			t.Fatalf("got %d findings, want the leak reported even though the task never said done: %+v", len(findings), findings)
		}
		if !strings.Contains(findings[0].Hold(), "terminal status") {
			t.Fatalf("hold = %q, want it held because the task never finished", findings[0].Hold())
		}
	})
}

// TestAnUnplacedAgentRefusalAnswersToThatAgent: a refusal about an agent that
// did not say where it is running is not answered by naming the process beside
// it or the task it belongs to. Keying it to either let one refusal be cleared
// by the key for another, which is what the refusal model exists to stop:
// naming a pid says nothing about where an unrelated agent is working, and
// naming a task id says its work is over, not that nobody else is in its
// directory.
func TestAnUnplacedAgentRefusalAnswersToThatAgent(t *testing.T) {
	const worktree = `C:\dev\pd\.worktrees\gb-pd-landing`
	inventory := Inventory{
		Panes:          []Pane{{ID: "w9:pMoved", ShellPID: 900, HasAgent: true}},
		UnplacedAgents: []string{"w9:pMoved"},
		Tasks:          []Task{task("pd-landing", worktree, "w9:p8Y", "working")},
		Worktrees:      []WorktreeDir{{Path: worktree, Project: `C:\dev\pd`, Registration: RegistrationListed, TaskID: "pd-landing"}},
		Processes: []Process{
			process(35012, 1, "node.exe", `node `+worktree+`\node_modules\vite\bin\vite.js`, fixtureLatest),
		},
	}

	for _, testCase := range []struct {
		name  string
		class Class
		force map[string]bool
	}{
		{"a task force does not answer it on a worktree", OrphanWorktree, map[string]bool{"pd-landing": true}},
		{"a pid force does not answer it on a server", StaleServer, map[string]bool{"35012": true}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			findings := classOf(Classify(inventory), testCase.class)
			if len(findings) != 1 {
				t.Fatalf("got %d %s findings, want 1: %+v", len(findings), testCase.class, findings)
			}
			finding := findings[0]
			finding.clearForced(testCase.force)
			if !strings.Contains(finding.Hold(), "no working directory") {
				t.Fatalf("hold = %q, want the unplaced-agent refusal to survive a key that does not answer it", finding.Hold())
			}
		})
	}

	t.Run("naming the pane that could not be placed is what answers it", func(t *testing.T) {
		finding := classOf(Classify(inventory), StaleServer)[0]
		finding.clearForced(map[string]bool{"w9:pMoved": true})
		if strings.Contains(finding.Hold(), "no working directory") {
			t.Fatalf("hold = %q, want the refusal answered by the agent it is about", finding.Hold())
		}
	})
}
