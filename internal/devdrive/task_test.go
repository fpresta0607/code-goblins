package devdrive

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// The startup task the create and attach items register runs the attach
// script from the drive's own folder, on battery power too, with no time
// limit, and anyone signed in may start it, never change it. The task
// registered before ran Mount-DiskImage alone with its error hidden and the
// default settings, which stop it on battery power and after 72 hours.
func TestTheStartupTaskRunsTheAttachScriptOnBatteryWithNoTimeLimit(t *testing.T) {
	// Arrange
	vhd := `C:\DevDrives\CodeGoblins.vhdx`

	// Act
	script := CreateScript(vhd, "D:", 0)

	// Assert
	for _, says := range []string{
		"$attachScript = Join-Path $folder 'CodeGoblins-attach.ps1'",
		"Set-Content -LiteralPath $attachScript -Encoding UTF8 -Value @'\r\n" + AttachScript(vhd, "D:") + "'@\r\n",
		"-ExecutionPolicy Bypass -File `\"$attachScript`\"",
		"-Settings (New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit (New-TimeSpan) -MultipleInstances IgnoreNew)",
		"-User 'NT AUTHORITY\\SYSTEM' -RunLevel Highest -Force",
		".GetTask('Code Goblins Dev Drive').SetSecurityDescriptor('D:(A;;FA;;;BA)(A;;FA;;;SY)(A;;GRGX;;;AU)', 0)",
	} {
		if !strings.Contains(script, says) {
			t.Errorf("the create script lacks %q:\n%s", says, script)
		}
	}
	if strings.Contains(script, "-ErrorAction SilentlyContinue\"") {
		t.Errorf("the startup task still hides its error:\n%s", script)
	}
}

// The attach script's tries are bounded, and what it did reaches a file
// anyone signed in may read, beside the drive's own.
func TestTheAttachScriptIsBoundedAndWritesItsResult(t *testing.T) {
	// Act
	script := AttachScript(`C:\DevDrives\CodeGoblins.vhdx`, "D:")

	// Assert
	for _, says := range []string{
		"$vhd = 'C:\\DevDrives\\CodeGoblins.vhdx'",
		"$letter = 'D'",
		"$tries = 12",
		"Start-Sleep -Seconds 10",
		"Set-Partition -NewDriveLetter $letter",
		"$result = Join-Path (Split-Path -Parent $vhd) 'CodeGoblins-attach.json'",
		"icacls $result /grant '*S-1-5-11:R'",
		"if (-not $attached) { exit 1 }",
	} {
		if !strings.Contains(script, says) {
			t.Errorf("the attach script lacks %q:\n%s", says, script)
		}
	}
	if AttachBound != 3*time.Minute {
		t.Errorf("AttachBound = %s, want every try and pause and a minute: 3m", AttachBound)
	}
	if got := AttachResultPath(`C:\DevDrives\CodeGoblins.vhdx`); !strings.HasSuffix(got, "CodeGoblins-attach.json") {
		t.Errorf("AttachResultPath = %q", got)
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
