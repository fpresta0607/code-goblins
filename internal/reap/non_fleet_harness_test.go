package reap

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Where the Overlord's desktop applications run from, as this machine's
// process table and the applications' folders showed on 2026-10-09 and
// 2026-10-10: the Codex application, its app server and the node runtime
// beside it, the Claude Code the Claude application runs, and SIQshift, which
// starts the same codex.exe a Codex goblin runs.
const (
	codexPackaged   = `"C:\Program Files\WindowsApps\OpenAI.Codex_26.924.2738.0_x64__2p2nqsd0c76g0\app\ChatGPT.exe"`
	codexLocalBin   = `C:\Users\op\AppData\Local\OpenAI\Codex\bin\9691020b546a15b2\codex.exe`
	codexNodeRepl   = `C:\Users\op\AppData\Local\OpenAI\Codex\runtimes\cua_node\154806497bb51bae\bin\node_repl.exe`
	claudeLocalCode = `C:\Users\op\AppData\Roaming\Claude\claude-code\2.1.293\83cb0bd7fed4\claude.exe`
	siqshiftDesktop = `"C:\Users\op\AppData\Local\SIQshift\siqshift-desktop.exe"`
	goblinCodex     = `C:\Users\op\AppData\Roaming\npm\node_modules\@openai\codex\vendor\codex.exe`
)

// gonePID is a parent that has exited: no row of any fixture carries it.
const gonePID = 9999

// On 2026-10-09 the sweep woke the CFO about every 20 minutes for a
// short-lived codex.exe that was never the fleet's. A harness is the desktop
// application's by where it runs from or by a living ancestor that runs from
// there, and a gate agent's by a living no-mistakes above it, and neither is
// a finding. The image name decides nothing: a goblin's codex.exe with no
// terminal left is an orphan still, whatever its arguments name.
func TestAHarnessOfADesktopApplicationOrAGateIsNotAFinding(t *testing.T) {
	cases := map[string]struct {
		processes []Process
		harness   int
		reported  bool
	}{
		// The process table of 2026-10-10 at 00:14:53Z: pid 34208, the fourth
		// in five minutes, each gone within the minute.
		"a codex.exe his SIQshift application starts": {
			processes: []Process{
				process(10428, 10352, "explorer.exe", `C:\Windows\Explorer.EXE`, fixtureStart),
				process(10692, 10428, "siqshift-desktop.exe", siqshiftDesktop, fixtureLater),
				process(34208, 10692, "codex.exe", goblinCodex+` app-server --listen stdio://`, fixtureLatest),
			},
			harness: 34208,
		},
		"a codex.exe under ChatGPT.exe": {
			processes: []Process{
				process(750, 9376, "ChatGPT.exe", codexPackaged+" ", fixtureStart),
				process(752, 750, "codex.exe", `codex.exe app-server --analytics-default-enabled`, fixtureLater),
			},
			harness: 752,
		},
		"a codex.exe under the application's node_repl.exe": {
			processes: []Process{
				process(760, 9376, "chrome.exe", `"C:\Program Files\Google\Chrome\Application\chrome.exe"`, fixtureStart),
				process(761, 760, "node_repl.exe", codexNodeRepl, fixtureLater),
				process(762, 761, "codex.exe", `codex.exe app-server --listen stdio://`, fixtureLatest),
			},
			harness: 762,
		},
		"a codex.exe run from the application's folder whose parent has exited": {
			processes: []Process{
				process(5832, gonePID, "codex.exe", codexLocalBin+` app-server --listen stdio://`, fixtureLatest),
			},
			harness: 5832,
		},
		"a codex.exe under no-mistakes.exe": {
			processes: []Process{
				process(800, 1, "no-mistakes.exe", `C:\Users\op\AppData\Local\no-mistakes\no-mistakes.exe daemon run`, fixtureStart),
				process(801, 800, "codex.exe", `codex exec --dangerously-bypass-approvals-and-sandbox`, fixtureLater),
			},
			harness: 801,
		},
		"a claude.exe the Claude application runs whose parent has exited": {
			processes: []Process{
				process(34900, gonePID, "claude.exe", claudeLocalCode+` --output-format stream-json`, fixtureLatest),
			},
			harness: 34900,
		},
		"a goblin's codex.exe with no terminal": {
			processes: []Process{
				process(39684, gonePID, "codex.exe", goblinCodex+` --dangerously-bypass-approvals-and-sandbox`, fixtureLatest),
			},
			harness:  39684,
			reported: true,
		},
		"a goblin's codex.exe whose argument names the application's folder": {
			processes: []Process{
				process(39684, gonePID, "codex.exe", goblinCodex+` --dangerously-bypass-approvals-and-sandbox --add-dir C:\Users\op\AppData\Local\OpenAI\Codex\bin`, fixtureLatest),
			},
			harness:  39684,
			reported: true,
		},
		"a codex.exe under a node_repl.exe that is not the application's": {
			processes: []Process{
				process(761, gonePID, "node_repl.exe", `C:\tools\node_repl.exe`, fixtureLater),
				process(762, 761, "codex.exe", `codex.exe app-server --listen stdio://`, fixtureLatest),
			},
			harness:  762,
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

// The listing reads where each process's program runs from, because a command
// line need not say: the application starts its app server by a bare name.
func TestAHarnessIsPlacedByItsImagePathWhenItsCommandLineNamesNone(t *testing.T) {
	// Arrange
	listing := `[
		{"pid":5832,"ppid":9999,"name":"codex.exe","cmd":"codex.exe app-server --listen stdio://","path":"C:\\Users\\op\\AppData\\Local\\OpenAI\\Codex\\bin\\9691020b546a15b2\\codex.exe","start":"2026-10-09T19:40:00.0000000Z"},
		{"pid":39684,"ppid":9999,"name":"codex.exe","cmd":"codex.exe app-server --listen stdio://","path":"C:\\Users\\op\\AppData\\Roaming\\npm\\node_modules\\@openai\\codex\\vendor\\codex.exe","start":"2026-10-09T20:00:00.0000000Z"}
	]`
	processes, err := decodeProcesses([]byte(listing))
	if err != nil {
		t.Fatal(err)
	}

	// Act
	orphans := classOf(Classify(Inventory{Processes: processes}), OrphanProcess)

	// Assert
	if reportsPID(orphans, 5832) {
		t.Errorf("the application's app server was reported: %v", lines(orphans))
	}
	if !reportsPID(orphans, 39684) {
		t.Errorf("a codex.exe run from anywhere else was not reported: %v", lines(orphans))
	}
}

// no-mistakes sets NO_MISTAKES_GATE on every agent it starts, and the agent
// keeps it when the process that started it exits, which the walk up its
// parents does not survive. A harness that does not carry it is no gate agent.
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
			environments := map[int][]string{
				5832:  {`PATH=C:\Windows`, "NO_MISTAKES_GATE=1"},
				39684: {`PATH=C:\Windows`},
			}
			collector := Collector{Home: h, Session: "default", Processes: processes, Environment: func(pid int) ([]string, error) {
				if env, ok := environments[pid]; ok {
					return env, nil
				}
				return nil, errors.New("process environment unavailable")
			}}

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
	if elsewhere := key(t, unidentified(39684, `C:\tools\codex.exe`, "node.exe", 20)); elsewhere == first {
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
			Tasks:     []Task{task("old", `C:\dev\pd\.worktrees\gb-old`, "pane-gone", "done")},
			Worktrees: []WorktreeDir{{Path: `C:\dev\pd\.worktrees\gb-old`, Project: `C:\dev\pd`, Registration: RegistrationListed, TaskID: "old"}},
			Processes: []Process{process(pid, 1, "node.exe", `node C:\dev\pd\.worktrees\gb-old\node_modules\vite\bin\vite.js`, fixtureLatest)},
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
