package reap

import (
	"context"
	"testing"
	"time"
)

// goblinCodex is where a Codex goblin's codex.exe runs from.
var goblinCodex = systemPath(`C:\Users\op\AppData\Roaming\npm\node_modules\@openai\codex\vendor\codex.exe`)

// gonePID is a parent that has exited: no row of any fixture carries it.
const gonePID = 9999

// On 2026-10-27 the sweep raised an orphan wake for the claude.exe the browser's
// Claude extension starts as its native messaging host. Chromium starts a host
// through a cmd.exe that redirects it to its pipes, whose command line here is
// as this machine's process table showed it on 2026-10-30, and the .bat Claude
// Code writes starts claude.exe with that one flag. Neither a mention of the
// flag or the pipe nor a parent that came after the process places a harness
// in the browser.
func TestTheBrowsersNativeMessagingHostIsNotAFinding(t *testing.T) {
	const (
		chrome       = `"C:\Program Files\Google\Chrome\Application\chrome.exe"`
		launcher     = `C:\WINDOWS\system32\cmd.exe /d /s /c ""C:\Users\op\.claude\chrome\chrome-native-host.bat" chrome-extension://fcoeoabgfenejglbffodgkkbkcdhcgfn/ --parent-window=0" < \\.\pipe\chrome.nativeMessaging.in.50ee896f0378bdc8 > \\.\pipe\chrome.nativeMessaging.out.50ee896f0378bdc8`
		nativeHost   = `"C:\Users\op\.local\bin\claude.exe" "--chrome-native-host"`
		fleetHarness = `claude --dangerously-skip-permissions`
	)
	cases := map[string]struct {
		processes []Process
		harness   int
		reported  bool
	}{
		"the extension's claude.exe under the browser's launcher": {
			processes: []Process{
				process(33648, 9376, "chrome.exe", chrome, fixtureStart),
				process(16224, 33648, "cmd.exe", launcher, fixtureLater),
				process(12692, 16224, "claude.exe", nativeHost, fixtureLatest),
			},
			harness: 12692,
		},
		"the extension's claude.exe whose launcher has exited": {
			processes: []Process{
				process(12692, gonePID, "claude.exe", nativeHost, fixtureLatest),
			},
			harness: 12692,
		},
		"another program's harness under the browser's launcher": {
			processes: []Process{
				process(16224, gonePID, "cmd.exe", launcher, fixtureLater),
				process(12692, 16224, "codex.exe", `codex.exe app-server --listen stdio://`, fixtureLatest),
			},
			harness: 12692,
		},
		"a goblin whose prompt names the flag": {
			processes: []Process{
				process(31032, gonePID, "claude.exe", fleetHarness+` "stop reporting --chrome-native-host"`, fixtureLatest),
			},
			harness:  31032,
			reported: true,
		},
		"a goblin started with the flag among its own": {
			processes: []Process{
				process(31032, gonePID, "claude.exe", fleetHarness+` --chrome-native-host`, fixtureLatest),
			},
			harness:  31032,
			reported: true,
		},
		"a goblin under a cmd.exe that only names the pipe": {
			processes: []Process{
				process(16224, gonePID, "cmd.exe", `cmd.exe /d /s /c "echo \\.\pipe\chrome.nativeMessaging.in.1"`, fixtureLater),
				process(31032, 16224, "claude.exe", fleetHarness, fixtureLatest),
			},
			harness:  31032,
			reported: true,
		},
		"a goblin older than the launcher that took its parent's pid": {
			processes: []Process{
				process(16224, gonePID, "cmd.exe", launcher, fixtureLatest),
				process(31032, 16224, "claude.exe", fleetHarness, fixtureLater),
			},
			harness:  31032,
			reported: true,
		},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			// Act
			orphans := classOf(Classify(Inventory{Processes: test.processes}), OrphanProcess)

			// Assert
			if reported := reportsPID(orphans, test.harness); reported != test.reported {
				t.Errorf("pid %d reported = %v, want %v: %v", test.harness, reported, test.reported, lines(orphans))
			}
		})
	}
}

// no-mistakes sets NO_MISTAKES_GATE on every agent it starts, and the agent
// keeps it when the process that started it exits, which the walk up its
// parents does not survive. The second reading of the machine takes it from
// each process's environment, and a harness it finds without it is no gate
// agent.
func TestAGateAgentWhoseParentHasExitedIsKnownByItsEnvironment(t *testing.T) {
	for name, commandLine := range map[string]string{
		"codex.exe":  `codex exec --dangerously-bypass-approvals-and-sandbox`,
		"claude.exe": `claude --model opus -p --verbose --output-format stream-json`,
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			h, _ := nativeHome(t)
			processes := stubProcesses{
				process(5832, gonePID, name, commandLine, fixtureLatest),
				process(39684, gonePID, name, commandLine, fixtureLatest),
			}
			collector := Collector{Home: h, Session: "default", Processes: processes, Sightings: stillRunning(processes, map[int]Sighting{5832: {IsGateAgent: true}})}

			// Act
			inv, _, err := collector.Collect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			orphans := classOf(Classify(inv), OrphanProcess)

			// Assert
			if reportsPID(orphans, 5832) {
				t.Errorf("a gate agent was reported: %v", lines(orphans))
			}
			if !reportsPID(orphans, 39684) {
				t.Errorf("a harness that carries no gate variable was not reported: %v", lines(orphans))
			}
		})
	}
}

// A harness nothing ties to the fleet is the same finding as the last one of
// its shape: where it runs from, what started it and why it is held, whatever
// its pid and start. A harness that carries the fleet's launch flag, and a dev
// server, are each their own finding, one for each process.
func TestReportKeysNameAnUnidentifiedHarnessByItsShape(t *testing.T) {
	unidentified := func(pid int, image, parent string, start int) []Finding {
		return Actionable(Classify(Inventory{Processes: []Process{
			process(700, 1, parent, parent, fixtureStart),
			process(pid, 700, "codex.exe", image+` app-server`, fixtureLater.Add(time.Duration(start)*time.Minute)),
		}}))
	}
	key := func(t *testing.T, findings []Finding) string {
		t.Helper()
		if len(findings) != 1 {
			t.Fatalf("got %d running findings, want 1: %v", len(findings), lines(findings))
		}
		return ReportKeys(findings)[0]
	}
	first := key(t, unidentified(5832, goblinCodex, "node.exe", 0))

	if again := key(t, unidentified(39684, goblinCodex, "node.exe", 20)); again != first {
		t.Error("the same harness under a new pid and start is another finding")
	}
	if elsewhere := key(t, unidentified(39684, systemPath(`C:\tools\codex.exe`), "node.exe", 20)); elsewhere == first {
		t.Error("a harness run from another path is the same finding")
	}
	if otherParent := key(t, unidentified(39684, goblinCodex, "cmd.exe", 20)); otherParent == first {
		t.Error("a harness another program started is the same finding")
	}

	fleet := func(pid int) []Finding {
		return Actionable(Classify(Inventory{Processes: []Process{
			process(pid, gonePID, "codex.exe", goblinCodex+` --dangerously-bypass-approvals-and-sandbox`, fixtureLatest),
		}}))
	}
	if key(t, fleet(5832)) == key(t, fleet(39684)) {
		t.Error("two fleet harnesses with no terminal are one finding")
	}

	server := func(pid int) []Finding {
		return Actionable(Classify(Inventory{
			Tasks:     []Task{task("old", systemPath(`C:\dev\pd\.worktrees\gb-old`), "pane-gone", "done")},
			Worktrees: []WorktreeDir{{Path: systemPath(`C:\dev\pd\.worktrees\gb-old`), Project: systemPath(`C:\dev\pd`), Registration: RegistrationListed, TaskID: "old"}},
			Processes: []Process{process(pid, 1, "node.exe", systemPath(`node C:\dev\pd\.worktrees\gb-old\node_modules\vite\bin\vite.js`), fixtureLatest)},
		}))
	}
	if key(t, server(555)) == key(t, server(556)) {
		t.Error("two dev servers in a retired worktree are one finding")
	}
}

func reportsPID(findings []Finding, pid int) bool {
	for _, finding := range findings {
		if finding.PID == pid {
			return true
		}
	}
	return false
}
