package reap

import "testing"

// A test binary under a goblin's Go temporary directory is that goblin's when
// it also runs from the goblin's Claude Code scratchpad, which Claude Code
// keeps under %TEMP%\claude on Windows.
func TestALiveGoblinsGoTestRunFromItsClaudeScratchpadIsItsFixture(t *testing.T) {
	// Arrange
	inv := fleetWithGoblin(true, "done: PR https://example.invalid/pull/1")
	inv.StateDir = fleetState
	inv.ScratchRoots = []string{devDriveScratch, fleetScratch}
	inv.Worktrees = append(inv.Worktrees, WorktreeDir{Path: `C:\dev\proj\.worktrees\gb-board-2`, Project: `C:\dev\proj`, TaskID: "board-2", Registration: RegistrationListed, Created: fixtureLater})
	inv = withGoTest(inv, boardGoTmp, `C:\Users\op\AppData\Local\Temp\claude\c--dev-proj--worktrees-gb-board\5f0c2e1a\scratchpad`)

	// Act
	orphans := classOf(Classify(inv), OrphanProcess)

	// Assert
	for _, finding := range orphans {
		if finding.PID == 700 || finding.PID == 710 {
			t.Fatalf("a live goblin's Go test run from its Claude scratchpad was reported: %v", lines(orphans))
		}
	}
}
