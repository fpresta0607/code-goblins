package reap

import (
	"strings"
	"testing"
)

const boardGoTmp = `C:\Users\op\AppData\Local\cfo\gotmp\0b49f5f9\board`

// boardTestDir is where go test runs the board goblin's spawn tests: the
// package's directory in its own worktree.
const boardTestDir = liveWorktree + `\internal\spawn`

// withGoTest adds what a Go test leaves running when it drives a native
// terminal: the test binary, built under goTmp and run from dir, serving as
// the terminal's host, and the stand-in harness it runs from the test's own
// temporary directory there.
func withGoTest(inv Inventory, goTmp, dir string) Inventory {
	testTmp := goTmp + `\TestANativeGoblinSwitchesInPlace1974485234`
	host := process(700, 1, "spawn.test.exe", `"`+goTmp+`\go-build2247352313\b001\spawn.test.exe" native-spawn-host --state "`+testTmp+`\001\state" --id board -- "`+testTmp+`\003\codex.exe" --dangerously-bypass-approvals-and-sandbox`, fixtureLater)
	host.Cwd = dir
	stub := process(710, 700, "codex.exe", `"`+testTmp+`\003\codex.exe" --dangerously-bypass-approvals-and-sandbox`, fixtureLatest)
	stub.Cwd = testTmp + `\001`
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
			inv := withGoTest(fleetWithGoblin(test.alive, "done: PR https://example.invalid/pull/1"), boardGoTmp, boardTestDir)

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
	inv := withGoTest(fleetWithGoblin(true, "done: PR https://example.invalid/pull/1"), `C:\Users\op\AppData\Local\cfo\gotmp\0b49f5f9\retired-task`, `C:\dev\proj\.worktrees\gb-retired-task\internal\spawn`)

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

// The shared no-mistakes daemon runs every goblin's gate with the Go temporary
// directory of whichever goblin started it, so on 2026-09-29 the test binaries
// of other goblins' gates ran from cg-native-desktop's. A Go temporary
// directory alone is no evidence of ownership: a test binary is a task's only
// when it also runs from that task's worktree or scratch directory. One run
// from a gate's worktree is charged to no task and held until it is
// identified, so nothing done about the goblin whose directory the daemon
// borrowed can reach another goblin's gate.
func TestAGoTestIsNotATasksOnItsGoTemporaryDirectoryAlone(t *testing.T) {
	// Arrange
	gateTestDir := `C:\Users\op\.no-mistakes\worktrees\540e35e237ee\01M3P21QWKPRZHZJ5HTMJMCPQ2\internal\spawn`
	inv := withGoTest(fleetWithGoblin(false, "done: PR https://example.invalid/pull/1"), boardGoTmp, gateTestDir)

	// Act
	orphans := classOf(Classify(inv), OrphanProcess)

	// Assert
	reported := map[int]Finding{}
	for _, finding := range orphans {
		reported[finding.PID] = finding
	}
	for _, pid := range []int{700, 710} {
		finding, ok := reported[pid]
		if !ok {
			t.Fatalf("pid %d, a gate's test leftover, was not reported at all: %v", pid, lines(orphans))
		}
		if finding.TaskID != "" || strings.Contains(finding.Detail, "task board") {
			t.Errorf("pid %d is charged to board on its Go temporary directory alone: %s", pid, finding.Line())
		}
		if !strings.Contains(finding.Hold(), unidentifiedHold) {
			t.Errorf("pid %d hold = %q, want it held until it is identified", pid, finding.Hold())
		}
	}
}
