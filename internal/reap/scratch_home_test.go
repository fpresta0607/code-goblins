package reap

import (
	"testing"
)

var (
	fleetState  = systemPath(`C:\Users\op\AppData\Local\CodeGoblins\state`)
	scratchHome = systemPath(`C:\Users\op\AppData\Local\Temp\ceo-native-proof-1\home`)
)

// withScratchHost adds a goblins host run for a CEO home other than the fleet's,
// started in cwd with state as its --state, and the harness it runs.
func withScratchHost(inv Inventory, cwd, state string) Inventory {
	host := process(600, 1, "ceo-new.exe", systemPath(`"C:\tmp\ceo-new.exe" host --state "`)+state+`" --id ceo --dir "`+scratchHome+systemPath(`" -- "C:\Program Files\nodejs\node.exe" harness.js --dangerously-skip-permissions`), fixtureLater)
	host.Cwd = cwd
	harness := process(610, 600, "claude.exe", `claude.exe --dangerously-skip-permissions`, fixtureLatest)
	inv.StateDir = fleetState
	inv.Processes = append(inv.Processes, host, harness)
	return inv
}

// A goblins host run with another CEO home's --state is a goblin's test or proof,
// the native counterpart of a Herdr server of another session: its harness is
// that goblin's fixture while the goblin lives, tied to it by where the host
// was started; a dead goblin's leftover is reported against that goblin; and a
// scratch home
// no goblin can be tied to is never reported as this fleet's orphan. Reap
// used to report every one of them as an unsupervised harness, waking the
// CEO every few minutes.
func TestAScratchHomesHostIsItsGoblinsFixture(t *testing.T) {
	for name, test := range map[string]struct {
		alive      bool
		cwd, state string
		reported   bool
	}{
		"started in a live goblin's worktree":       {true, liveWorktree, scratchHome + systemPath(`\state`), false},
		"started in a dead goblin's worktree":       {false, liveWorktree, scratchHome + systemPath(`\state`), true},
		"tied to no goblin":                         {false, systemPath(`C:\Users\op\AppData\Local\Temp\ceo-native-proof-1`), scratchHome + systemPath(`\state`), false},
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

func TestCommandArgsKeepAQuotedPathWhole(t *testing.T) {
	got := commandArgs(`"C:\Program Files\goblins.exe" host --state "C:\a b\state" --id ceo`)
	want := []string{`C:\Program Files\goblins.exe`, "host", "--state", `C:\a b\state`, "--id", "ceo"}
	if len(got) != len(want) {
		t.Fatalf("commandArgs = %q, want %q", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("commandArgs = %q, want %q", got, want)
		}
	}
}
