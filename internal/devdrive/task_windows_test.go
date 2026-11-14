package devdrive

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// standIns take the place of the Storage cmdlets and Start-Sleep in a run of
// the attach script, since a function is found before a cmdlet of its name:
// $attached says Windows attached the file already, $letter is the
// partition's letter ([char]0 for none), and the first $failures mounts fail.
// Each says what it did on standard output.
const standIns = `$script:mounts = 0
function Get-DiskImage { param($ImagePath) [pscustomobject]@{ Attached = $script:isAttached; Number = 7 } }
function Mount-DiskImage {
    param($ImagePath)
    $script:mounts++
    Write-Host "mount $script:mounts"
    if ($script:mounts -le $script:failures) { throw 'The device is not ready.' }
    $script:isAttached = $true
}
function Get-Partition { param($DiskNumber) [pscustomobject]@{ Type = 'Basic'; DriveLetter = $script:partitionLetter } }
function Set-Partition {
    param([Parameter(ValueFromPipeline = $true)]$InputObject, $NewDriveLetter)
    process { $script:partitionLetter = [char]$NewDriveLetter; Write-Host "set letter $NewDriveLetter" }
}
function Start-Sleep { param($Seconds) Write-Host "sleep $Seconds" }
`

// The attach script runs in Windows PowerShell 5.1, which the startup task
// starts, against stand-ins for Get-DiskImage, Mount-DiskImage,
// Get-Partition, Set-Partition and Start-Sleep, and its output and result
// are read: it attaches what is not attached, gives back a lost letter,
// tries again within its bound and says what it did either way.
func TestTheAttachScriptRunsInWindowsPowerShell(t *testing.T) {
	for _, test := range []struct {
		name            string
		isAttached      bool
		letter          string
		failures        int
		exit            int
		tries           int
		isAttachedAfter bool
		mounts          int
		sleeps          int
		isLetterSet     bool
	}{
		{name: "a drive already attached", isAttached: true, letter: "'D'", exit: 0, tries: 1, isAttachedAfter: true},
		{name: "a drive attached with no letter", isAttached: true, letter: "0", exit: 0, tries: 1, isAttachedAfter: true, isLetterSet: true},
		{name: "a mount that fails twice then works", letter: "'D'", failures: 2, exit: 0, tries: 3, isAttachedAfter: true, mounts: 3, sleeps: 2},
		{name: "a mount that never works", letter: "'D'", failures: 99, exit: 1, tries: AttachTries, mounts: AttachTries, sleeps: AttachTries - 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			folder := t.TempDir()
			vhd := filepath.Join(folder, "CodeGoblins.vhdx")
			preamble := fmt.Sprintf("$script:isAttached = $%t\r\n$script:partitionLetter = [char]%s\r\n$script:failures = %d\r\n%s", test.isAttached, test.letter, test.failures, standIns)
			path := filepath.Join(folder, AttachScriptName)
			if err := os.WriteFile(path, append([]byte("\xef\xbb\xbf"), preamble+AttachScript(vhd, "D:")...), 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path).CombinedOutput()

			// Assert
			exit := 0
			if exitErr, ok := err.(*exec.ExitError); ok {
				exit = exitErr.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			said := string(output)
			if exit != test.exit {
				t.Fatalf("exit = %d, want %d\n%s", exit, test.exit, said)
			}
			if got := strings.Count(said, "mount "); got != test.mounts {
				t.Errorf("%d mounts, want %d\n%s", got, test.mounts, said)
			}
			if got := strings.Count(said, "sleep 10"); got != test.sleeps {
				t.Errorf("%d pauses of 10 seconds, want %d\n%s", got, test.sleeps, said)
			}
			if got := strings.Contains(said, "set letter D"); got != test.isLetterSet {
				t.Errorf("letter set = %v, want %v\n%s", got, test.isLetterSet, said)
			}
			result, isWritten, err := ReadAttachResult(vhd)
			if err != nil || !isWritten {
				t.Fatalf("result = %+v, %v, %v; want one written\n%s", result, isWritten, err, said)
			}
			if result.Attached != test.isAttachedAfter || result.Tries != test.tries || result.Letter != "D:" || result.Finished.Before(result.Started) || result.Started.IsZero() {
				t.Errorf("result = %+v, want attached %v in %d tries", result, test.isAttachedAfter, test.tries)
			}
			wantErrors := test.tries - 1
			if !test.isAttachedAfter {
				wantErrors = test.tries
			}
			if len(result.Errors) != wantErrors || wantErrors > 0 && !strings.Contains(result.Errors[len(result.Errors)-1], "The device is not ready.") {
				t.Errorf("errors = %q, want %d naming the mount's error", result.Errors, wantErrors)
			}
		})
	}
}

// taskStandIns take the place of the Storage cmdlets, fsutil,
// Register-ScheduledTask and the Schedule.Service COM object in a run of the
// create script on a drive already attached, and each says what it was given
// on standard output. New-ScheduledTaskAction, New-ScheduledTaskTrigger and
// New-ScheduledTaskSettingsSet are Windows' own, which build objects and
// change nothing. Their module is imported first, so its own
// Register-ScheduledTask does not replace the stand-in.
const taskStandIns = `Import-Module ScheduledTasks
function Get-DiskImage { [CmdletBinding()] param($ImagePath) [pscustomobject]@{ Attached = $true; Number = 7 } }
function Get-Disk { param([Parameter(ValueFromPipeline = $true)]$InputObject) process { [pscustomobject]@{ Number = 7; PartitionStyle = 'GPT' } } }
function Get-Partition { param($DiskNumber) [pscustomobject]@{ Type = 'Basic'; DriveLetter = 'D' } }
function fsutil { Write-Host "fsutil $args" }
function Register-ScheduledTask {
    param($TaskName, $Trigger, $Action, $Settings, $User, $RunLevel, [switch]$Force)
    Write-Host "registered $TaskName"
    Write-Host "trigger $($Trigger.CimClass.CimClassName)"
    Write-Host "action $($Action.Execute) $($Action.Arguments)"
    Write-Host "settings $($Settings.DisallowStartIfOnBatteries) $($Settings.StopIfGoingOnBatteries) $($Settings.ExecutionTimeLimit) $($Settings.MultipleInstances)"
    Write-Host "runs as $User at $RunLevel"
}
function New-Object {
    param([string]$ComObject)
    Write-Host "com $ComObject"
    $task = [pscustomobject]@{}
    $task | Add-Member -MemberType ScriptMethod -Name SetSecurityDescriptor -Value { param($sddl, $flags) Write-Host "security $sddl $flags" }
    $folder = [pscustomobject]@{ Task = $task }
    $folder | Add-Member -MemberType ScriptMethod -Name GetTask -Value { param($name) Write-Host "security of $name"; $this.Task }
    $service = [pscustomobject]@{ Folder = $folder }
    $service | Add-Member -MemberType ScriptMethod -Name Connect -Value { }
    $service | Add-Member -MemberType ScriptMethod -Name GetFolder -Value { param($path) $this.Folder }
    $service
}
`

// The create script, run in Windows PowerShell 5.1 on a drive already
// attached, writes the attach script beside the drive's file, where it
// parses, and registers the startup task to run that file as SYSTEM at boot,
// with the settings Windows builds read back: it starts on battery power, is
// never stopped for going on battery or by a time limit, and never runs twice
// at once. Then it lets administrators and Windows change the task and
// Authenticated Users only read and start it. A task registered without the
// settings, or before the script is written, fails here.
func TestTheCreateScriptRegistersATaskThatRunsTheAttachScriptOnBatteryWithNoTimeLimit(t *testing.T) {
	// Arrange
	folder := t.TempDir()
	vhd := filepath.Join(folder, "CodeGoblins.vhdx")
	attachScript := filepath.Join(folder, AttachScriptName)
	postamble := "$parseErrors = $null\r\n[System.Management.Automation.Language.Parser]::ParseFile($attachScript, [ref]$null, [ref]$parseErrors) | Out-Null\r\nWrite-Host \"parse errors $($parseErrors.Count)\"\r\n"
	path := filepath.Join(t.TempDir(), "create.ps1")
	if err := os.WriteFile(path, append([]byte("\xef\xbb\xbf"), taskStandIns+CreateScript(vhd, "D:", 0)+postamble...), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path).CombinedOutput()

	// Assert
	said := string(output)
	if err != nil {
		t.Fatalf("the create script failed: %v\n%s", err, said)
	}
	var lines []string
	for _, line := range strings.Split(said, "\n") {
		lines = append(lines, strings.TrimSpace(line))
	}
	next := 0
	for _, want := range []string{
		"registered " + TaskName,
		"trigger MSFT_TaskBootTrigger",
		`action powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "` + attachScript + `"`,
		"settings False False PT0S IgnoreNew",
		`runs as NT AUTHORITY\SYSTEM at Highest`,
		"com Schedule.Service",
		"security of " + TaskName,
		"security D:(A;;FA;;;BA)(A;;FA;;;SY)(A;;GRGX;;;AU) 0",
		"parse errors 0",
	} {
		found := slices.Index(lines[next:], want)
		if found < 0 {
			t.Fatalf("the create script did not say %q after what came before:\n%s", want, said)
		}
		next += found + 1
	}
	written, err := os.ReadFile(attachScript)
	if err != nil || string(written) != "\xef\xbb\xbf"+AttachScript(vhd, "D:") {
		t.Errorf("%s = %q, %v; want the attach script the startup task runs", attachScript, written, err)
	}
}

// A drive whose startup task is from before gets one renew item, made from
// the create script, and only while the drive is attached: a missing drive
// gets the attach item, which registers the task as well.
func TestAnOldStartupTaskIsRenewedWhileTheDriveIsAttached(t *testing.T) {
	// Arrange
	m := ready()
	m.Volumes = append(m.Volumes, Volume{Root: `D:\`, FileSystem: "ReFS", Dev: true, Trust: Trusted})
	moved := home.Home{Root: `C:\home`, DevDrive: `D:\CodeGoblins`}
	config := home.DevDriveConfig{Root: `D:\CodeGoblins`, VHD: `C:\DevDrives\CodeGoblins.vhdx`}

	// Act
	plan, ok := Next(m, moved, config, false, true)

	// Assert
	if !ok || plan.Step != StepTask || !plan.Admin || plan.Script != CreateScript(config.VHD, "D:", 0) || !strings.Contains(plan.Title, "Renew the startup task of the Code Goblins Dev Drive D:") || strings.Contains(plan.Title, ";") {
		t.Fatalf("plan = %+v, %v; want the renew item for D:", plan, ok)
	}
	if plan, ok := Next(m, moved, config, false, false); ok {
		t.Errorf("a current task got %+v", plan)
	}
	m.Volumes = m.Volumes[:1]
	if plan, ok := Next(m, moved, config, false, true); !ok || plan.Step != StepAttach {
		t.Errorf("a missing drive got %+v, %v; want the attach item", plan, ok)
	}
}

// The line of a missing drive never names an item: it names what the board
// does for a drive Code Goblins made, and what the person does for another.
func TestAMissingDrivesLineNamesNoItem(t *testing.T) {
	m := ready()
	moved := home.Home{Root: `C:\home`, DevDrive: `D:\CodeGoblins`}
	for vhd, says := range map[string]string{`C:\DevDrives\CodeGoblins.vhdx`: AttachByBoard, "": AttachByHand} {
		report := Describe(m, moved, vhd)
		if report.State != StateMissing || !strings.HasSuffix(report.Line, says) || strings.Contains(report.Line, "item") || strings.Contains(report.Line, ";") {
			t.Errorf("vhd %q: line = %q, want it to end %q and name no item", vhd, report.Line, says)
		}
	}
}
