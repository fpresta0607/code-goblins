package reap

import (
	"testing"
)

const (
	fleetState   = `C:\Users\op\AppData\Local\CodeGoblins\state`
	scratchHome  = `C:\Users\op\AppData\Local\Temp\cfo-native-proof-1\home`
	boardScratch = `C:\Users\op\AppData\Local\Temp\claude\C--dev-proj--worktrees-gb-board\5e1f\scratchpad`
)

// withScratchHost adds a cfo host run for a CFO home other than the fleet's,
// started in cwd with state as its --state, and the harness it runs.
func withScratchHost(inv Inventory, cwd, state string) Inventory {
	host := process(600, 1, "cfo-new.exe", `"C:\tmp\cfo-new.exe" host --state "`+state+`" --id cfo --dir "`+scratchHome+`" -- "C:\Program Files\nodejs\node.exe" harness.js --dangerously-skip-permissions`, fixtureLater)
	host.Cwd = cwd
	harness := process(610, 600, "claude.exe", `claude.exe --dangerously-skip-permissions`, fixtureLatest)
	inv.StateDir = fleetState
	inv.Processes = append(inv.Processes, host, harness)
	return inv
}

// A cfo host run with another CFO home's --state is a goblin's test or proof,
// the native counterpart of a Herdr server of another session: its harness is
// that goblin's fixture while the goblin lives, tied to it by where the host
// was started or by the goblin's Claude Code scratchpad the home sits in; a
// dead goblin's leftover is reported against that goblin; and a scratch home
// no goblin can be tied to is never reported as this fleet's orphan. Reap
// used to report every one of them as an unsupervised harness, waking the
// CFO every few minutes.
func TestAScratchHomesHostIsItsGoblinsFixture(t *testing.T) {
	for name, test := range map[string]struct {
		alive      bool
		cwd, state string
		reported   bool
	}{
		"started in a live goblin's worktree":       {true, liveWorktree, scratchHome + `\state`, false},
		"started in a dead goblin's worktree":       {false, liveWorktree, scratchHome + `\state`, true},
		"tied to no goblin":                         {false, `C:\Users\op\AppData\Local\Temp\cfo-native-proof-1`, scratchHome + `\state`, false},
		"its home in a live goblin's scratchpad":    {true, `C:\Windows\System32`, boardScratch + `\home\state`, false},
		"its home in a dead goblin's scratchpad":    {false, `C:\Windows\System32`, boardScratch + `\home\state`, true},
		"the fleet's own home, with no host record": {true, liveWorktree, fleetState, true},
	} {
		t.Run(name, func(t *testing.T) {
			inv := withScratchHost(fleetWithGoblin(test.alive, "done: PR https://example.invalid/pull/1"), test.cwd, test.state)

			orphans := classOf(Classify(inv), OrphanProcess)

			for _, pid := range []int{600, 610} {
				var found []Finding
				for _, finding := range orphans {
					if finding.PID == pid {
						found = append(found, finding)
					}
				}
				if reported := len(found) != 0; reported != test.reported {
					t.Fatalf("pid %d reported = %v, want %v: %v", pid, reported, test.reported, lines(orphans))
				}
				if test.reported && test.state != fleetState && found[0].TaskID != "board" {
					t.Errorf("the leftover of a dead goblin's scratch home is not reported against it: %v", lines(found))
				}
			}
		})
	}
}

// Claude Code names a scratchpad after its working directory with every
// character that is not an ASCII letter or digit turned into -, spaces
// included, so a goblin whose worktree path has a space still owns the
// scratch home in its scratchpad.
func TestAScratchHomeInASpacedWorktreesScratchpadIsItsGoblinsFixture(t *testing.T) {
	const (
		spacedWorktree = `C:\dev\Retire 91\.worktrees\gb-board`
		spacedScratch  = `C:\Users\op\AppData\Local\Temp\claude\C--dev-Retire-91--worktrees-gb-board\5e1f\scratchpad`
	)
	for name, test := range map[string]struct {
		alive, reported bool
	}{
		"a live goblin": {true, false},
		"a dead goblin": {false, true},
	} {
		t.Run(name, func(t *testing.T) {
			inv := fleetWithGoblin(test.alive, "done: PR https://example.invalid/pull/1")
			inv.Tasks[0].Meta.Worktree = spacedWorktree
			inv.Worktrees[0].Path = spacedWorktree
			if test.alive {
				inv.Panes[0].AgentCwd = spacedWorktree
			}
			inv = withScratchHost(inv, `C:\Windows\System32`, spacedScratch+`\home\state`)

			orphans := classOf(Classify(inv), OrphanProcess)

			for _, pid := range []int{600, 610} {
				var found []Finding
				for _, finding := range orphans {
					if finding.PID == pid {
						found = append(found, finding)
					}
				}
				if reported := len(found) != 0; reported != test.reported {
					t.Fatalf("pid %d reported = %v, want %v: %v", pid, reported, test.reported, lines(orphans))
				}
				if test.reported && found[0].TaskID != "board" {
					t.Errorf("the leftover of a dead goblin's scratch home is not reported against it: %v", lines(found))
				}
			}
		})
	}
}

func TestCommandArgsKeepAQuotedPathWhole(t *testing.T) {
	got := commandArgs(`"C:\Program Files\cfo.exe" host --state "C:\a b\state" --id cfo`)
	want := []string{`C:\Program Files\cfo.exe`, "host", "--state", `C:\a b\state`, "--id", "cfo"}
	if len(got) != len(want) {
		t.Fatalf("commandArgs = %q, want %q", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("commandArgs = %q, want %q", got, want)
		}
	}
}
