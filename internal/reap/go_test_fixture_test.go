package reap

import (
	"testing"
)

const boardGoTmp = `C:\Users\op\AppData\Local\cfo\gotmp\0b49f5f9\board`

// withGoTest adds what a goblin's Go test leaves running when it drives a
// native terminal: the test binary, built under the goblin's Go temporary
// directory, serving as the terminal's host, and the stand-in harness it runs
// from the test's own temporary directory there.
func withGoTest(inv Inventory, goTmp string) Inventory {
	testTmp := goTmp + `\TestANativeGoblinSwitchesInPlace1974485234`
	host := process(700, 1, "spawn.test.exe", `"`+goTmp+`\go-build2247352313\b001\spawn.test.exe" native-spawn-host --state "`+testTmp+`\001\state" --id board -- "`+testTmp+`\003\codex.exe" --dangerously-bypass-approvals-and-sandbox`, fixtureLater)
	stub := process(710, 700, "codex.exe", `"`+testTmp+`\003\codex.exe" --dangerously-bypass-approvals-and-sandbox`, fixtureLatest)
	inv.Processes = append(inv.Processes, host, stub)
	return inv
}

// A goblin's Go tests run under its own Go temporary directory,
// %LOCALAPPDATA%\cfo\gotmp\<fleet>\<task id>, so a test binary serving as a
// native terminal host there, and the stand-in harness it runs, belong to the
// task that path names: skipped while that goblin lives, and reported against
// it once it is gone. Reap reported both as unsupervised harnesses of no task.
func TestAGoblinsGoTestStandInsAreItsFixture(t *testing.T) {
	for name, test := range map[string]struct {
		alive, reported bool
	}{
		"a live goblin": {true, false},
		"a dead goblin": {false, true},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			inv := withGoTest(fleetWithGoblin(test.alive, "done: PR https://example.invalid/pull/1"), boardGoTmp)

			// Act
			orphans := classOf(Classify(inv), OrphanProcess)

			// Assert
			for _, pid := range []int{700, 710} {
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
					t.Errorf("the leftover of a dead goblin's Go test is not reported against it: %v", lines(found))
				}
			}
		})
	}
}

// A Go temporary directory names its task, but a task this fleet does not
// know leaves nothing live to tie it to, so its leftover is reported as any
// unsupervised harness is.
func TestAGoTestStandInOfAnUnknownTaskIsReported(t *testing.T) {
	// Arrange
	inv := withGoTest(fleetWithGoblin(true, "done: PR https://example.invalid/pull/1"), `C:\Users\op\AppData\Local\cfo\gotmp\0b49f5f9\retired-task`)

	// Act
	orphans := classOf(Classify(inv), OrphanProcess)

	// Assert
	reported := map[int]bool{}
	for _, finding := range orphans {
		reported[finding.PID] = true
	}
	if !reported[700] || !reported[710] {
		t.Fatalf("the Go test stand-ins of a task this fleet does not know were not reported: %v", lines(orphans))
	}
}
