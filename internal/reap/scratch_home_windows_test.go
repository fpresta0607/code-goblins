package reap

import "testing"

// boardScratch is the board goblin's Claude Code scratchpad, which Claude Code
// keeps under %TEMP%\claude on Windows.
const boardScratch = `C:\Users\op\AppData\Local\Temp\claude\C--dev-proj--worktrees-gb-board\5e1f\scratchpad`

// A goblins host run with another CEO home's --state whose home sits in a
// goblin's Claude Code scratchpad is that goblin's fixture while the goblin
// lives, and a dead goblin's leftover is reported against that goblin.
func TestAScratchHomeInAGoblinsScratchpadIsItsGoblinsFixture(t *testing.T) {
	for name, test := range map[string]struct {
		alive, reported bool
	}{
		"its home in a live goblin's scratchpad": {true, false},
		"its home in a dead goblin's scratchpad": {false, true},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			inv := withScratchHost(fleetWithGoblin(test.alive, "done: PR https://example.invalid/pull/1"), `C:\Windows\System32`, boardScratch+`\home\state`)

			// Act
			orphans := classOf(Classify(inv), OrphanProcess)

			// Assert
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
