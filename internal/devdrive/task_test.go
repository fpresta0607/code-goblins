package devdrive

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// The board waits for the startup task it started for every try and every
// pause the task takes and a minute more, which is the 3 minutes the board
// is told to wait at most before it offers the Attach item.
func TestTheBoardWaitsForTheStartupTaskThreeMinutesAtMost(t *testing.T) {
	if AttachBound != 3*time.Minute {
		t.Errorf("AttachBound = %s, want every try and pause and a minute: 3m", AttachBound)
	}
}

// What the startup task did is said against the machine's last boot, so a
// result from before the boot never reads as this boot's.
func TestDescribeAttachSaysWhatTheTaskDidSinceTheBoot(t *testing.T) {
	boot := time.Date(2026, 11, 13, 23, 1, 0, 0, time.UTC)
	for name, test := range map[string]struct {
		result    AttachResult
		isWritten bool
		says      string
	}{
		"none written": {says: "no result in"},
		"attached after the boot": {AttachResult{Finished: boot.Add(40 * time.Second), Attached: true, Letter: "D:", Tries: 3}, true,
			"since this boot at 2026-11-13T23:01:00Z it attached D: at 2026-11-13T23:01:40Z, on try 3 of 12"},
		"failed after the boot": {AttachResult{Finished: boot.Add(2 * time.Minute), Letter: "D:", Tries: 12, Errors: []string{"try 1: not ready", "try 12: The device is not ready."}}, true,
			"since this boot at 2026-11-13T23:01:00Z it did not attach D: in 12 tries, the last ending at 2026-11-13T23:03:00Z: try 12: The device is not ready."},
		"before the boot": {AttachResult{Finished: boot.Add(-time.Hour), Attached: true, Letter: "D:", Tries: 1}, true,
			"it has not run since this boot at 2026-11-13T23:01:00Z, and its last run attached D: at 2026-11-13T22:01:00Z"},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			got := DescribeAttach(`C:\DevDrives\CodeGoblins.vhdx`, test.result, test.isWritten, boot)

			// Assert
			if !strings.Contains(got, test.says) || strings.Contains(got, ";") {
				t.Errorf("DescribeAttach = %q, want it to say %q with no semicolon", got, test.says)
			}
		})
	}
}

// runnerFunc answers each command with what the test says.
type runnerFunc func(execx.Request) execx.Result

func (f runnerFunc) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	return f(request), nil
}

// Only the task this build registers is current: one this user may not
// read, as the one registered before is, or one that runs something else,
// is not. Starting a task Windows refuses says what schtasks said.
func TestTheStartupTaskIsCurrentOnlyWhenItRunsTheAttachScript(t *testing.T) {
	for name, test := range map[string]struct {
		answer    execx.Result
		isCurrent bool
	}{
		"this build's task":     {execx.Result{Stdout: []byte(`<Arguments>-NoProfile -File "C:\DevDrives\CodeGoblins-attach.ps1"</Arguments>`)}, true},
		"the task from before":  {execx.Result{Stdout: []byte(`<Arguments>-NoProfile -Command "Mount-DiskImage"</Arguments>`)}, false},
		"a task it cannot read": {execx.Result{ExitCode: 1, Stderr: []byte("ERROR: Access is denied.")}, false},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			var asked execx.Request
			runner := runnerFunc(func(request execx.Request) execx.Result { asked = request; return test.answer })

			// Act
			isCurrent, err := TaskCurrent(context.Background(), runner)

			// Assert
			if err != nil || isCurrent != test.isCurrent || asked.Name != "schtasks.exe" || strings.Join(asked.Args, " ") != "/Query /TN Code Goblins Dev Drive /XML" {
				t.Errorf("TaskCurrent = %v, %v after %+v; want %v", isCurrent, err, asked, test.isCurrent)
			}
		})
	}
	refused := runnerFunc(func(execx.Request) execx.Result {
		return execx.Result{ExitCode: 1, Stderr: []byte("ERROR: Access is denied.")}
	})
	if err := StartTask(context.Background(), refused); err == nil || !strings.Contains(err.Error(), "Access is denied.") {
		t.Errorf("StartTask refused = %v, want what schtasks said", err)
	}
}
