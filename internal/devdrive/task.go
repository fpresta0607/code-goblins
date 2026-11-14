package devdrive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// TaskName is the startup task that attaches the Dev Drive Code Goblins made
// at every boot, as SYSTEM.
const TaskName = "Code Goblins Dev Drive"

// The files beside the drive's own file that its startup task runs and writes.
// The task's script is open to administrators and Windows alone, as the
// folder is, and its result to anyone signed in, so the board reads what the
// last boot did without elevation.
const (
	AttachScriptName = "CodeGoblins-attach.ps1"
	AttachResultName = "CodeGoblins-attach.json"
)

// The startup task's script tries this many times, this far apart, since
// Windows' storage service may not answer yet when the task starts at boot.
const (
	AttachTries = 12
	AttachPause = 10 * time.Second
)

// AttachBound is how long the board waits for the startup task it started to
// write its result before it offers the Attach item: every try and every
// pause, and a minute for the tries themselves.
const AttachBound = AttachTries*AttachPause + time.Minute

// taskSecurity lets administrators and Windows change the task and anyone
// signed in read it and start it, never change it: the board starts it
// without elevation when the drive is missing.
const taskSecurity = "D:(A;;FA;;;BA)(A;;FA;;;SY)(A;;GRGX;;;AU)"

// taskSettings starts the task on battery power, never stops it for going on
// battery or for a time limit, its own tries being its bound, and runs one
// at a time.
const taskSettings = "New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit (New-TimeSpan) -MultipleInstances IgnoreNew"

// AttachScriptPath is the startup task's script for the drive in vhd.
func AttachScriptPath(vhd string) string {
	return filepath.Join(filepath.Dir(vhd), AttachScriptName)
}

// AttachResultPath is where the startup task for the drive in vhd writes what
// it did.
func AttachResultPath(vhd string) string {
	return filepath.Join(filepath.Dir(vhd), AttachResultName)
}

// AttachScript is what the startup task runs, as SYSTEM, at every boot and
// whenever the board starts it: it attaches vhd unless Windows already did,
// gives its volume back its letter, tries again up to AttachTries times, and
// writes what it did and every error to AttachResultPath, which anyone signed
// in may read. It exits 1 when the drive is not attached.
func AttachScript(vhd, letter string) string {
	return "# Code Goblins: attach the Dev Drive " + letter + " at boot, from the startup task " + TaskName + ".\r\n" +
		"# It tries " + fmt.Sprint(AttachTries) + " times, " + fmt.Sprint(int(AttachPause/time.Second)) + " seconds apart, and writes what it did to " + AttachResultName + " beside the drive's file.\r\n" +
		"$ErrorActionPreference = 'Stop'\r\n" +
		"$vhd = " + quote(vhd) + "\r\n" +
		"$letter = " + quote(strings.TrimSuffix(letter, ":")) + "\r\n" +
		"$result = Join-Path (Split-Path -Parent $vhd) " + quote(AttachResultName) + "\r\n" +
		"$tries = " + fmt.Sprint(AttachTries) + "\r\n" +
		"$started = (Get-Date).ToUniversalTime().ToString('o')\r\n" +
		"$errors = @()\r\n" +
		"$attached = $false\r\n" +
		"for ($try = 1; $try -le $tries; $try++) {\r\n" +
		"    try {\r\n" +
		"        $image = Get-DiskImage -ImagePath $vhd\r\n" +
		"        if (-not $image.Attached) {\r\n" +
		"            Mount-DiskImage -ImagePath $vhd | Out-Null\r\n" +
		"            $image = Get-DiskImage -ImagePath $vhd\r\n" +
		"        }\r\n" +
		"        $partition = Get-Partition -DiskNumber $image.Number | Where-Object { $_.Type -eq 'Basic' } | Select-Object -First 1\r\n" +
		"        if (-not $partition) { throw \"the drive's file holds no Basic partition\" }\r\n" +
		"        if ($partition.DriveLetter -ne $letter) { $partition | Set-Partition -NewDriveLetter $letter }\r\n" +
		"        $attached = $true\r\n" +
		"        break\r\n" +
		"    } catch {\r\n" +
		"        $errors += \"try $($try): $($_.Exception.Message)\"\r\n" +
		"        if ($try -lt $tries) { Start-Sleep -Seconds " + fmt.Sprint(int(AttachPause/time.Second)) + " }\r\n" +
		"    }\r\n" +
		"}\r\n" +
		"$record = [ordered]@{ started = $started; finished = (Get-Date).ToUniversalTime().ToString('o'); attached = $attached; letter = \"$($letter):\"; tries = [math]::Min($try, $tries); errors = $errors }\r\n" +
		"[System.IO.File]::WriteAllText($result, (ConvertTo-Json -InputObject $record))\r\n" +
		"icacls $result /grant '*S-1-5-11:R' | Out-Null\r\n" +
		"if (-not $attached) { exit 1 }\r\n"
}

// AttachResult is what the startup task's last run wrote.
type AttachResult struct {
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	Attached bool      `json:"attached"`
	Letter   string    `json:"letter"`
	Tries    int       `json:"tries"`
	Errors   []string  `json:"errors"`
}

// ReadAttachResult reads what the startup task for the drive in vhd last
// wrote, false when it wrote nothing yet.
func ReadAttachResult(vhd string) (AttachResult, bool, error) {
	data, err := os.ReadFile(AttachResultPath(vhd))
	if errors.Is(err, fs.ErrNotExist) {
		return AttachResult{}, false, nil
	}
	if err != nil {
		return AttachResult{}, false, err
	}
	var result AttachResult
	if err := json.Unmarshal(data, &result); err != nil {
		return AttachResult{}, false, fmt.Errorf("devdrive: %s: %w", AttachResultPath(vhd), err)
	}
	return result, true, nil
}

// schtasksTimeout bounds one answer from schtasks, which reads the task
// scheduler and returns at once.
const schtasksTimeout = 30 * time.Second

// StartTask starts the startup task now, which anyone signed in may do once
// the create or attach item registered it. A task registered before that,
// which only administrators may start, is refused.
func StartTask(ctx context.Context, commands execx.Runner) error {
	ctx, cancel := context.WithTimeout(ctx, schtasksTimeout)
	defer cancel()
	result, err := commands.Run(ctx, execx.Request{Name: "schtasks.exe", Args: []string{"/Run", "/TN", TaskName}, NoConsole: true})
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("schtasks /Run: %s", strings.TrimSpace(string(result.Stderr)+string(result.Stdout)))
	}
	return nil
}

// TaskCurrent says whether the startup task is the one this build registers:
// readable without elevation and running AttachScriptName. One registered
// before, which only administrators may read and which ran Mount-DiskImage
// alone, or none at all, is not.
func TaskCurrent(ctx context.Context, commands execx.Runner) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, schtasksTimeout)
	defer cancel()
	result, err := commands.Run(ctx, execx.Request{Name: "schtasks.exe", Args: []string{"/Query", "/TN", TaskName, "/XML"}, NoConsole: true})
	if err != nil {
		return false, err
	}
	return result.ExitCode == 0 && strings.Contains(string(result.Stdout), AttachScriptName), nil
}

// DescribeAttach says what the startup task did, from the result it wrote,
// isWritten false when it wrote none, measured against boot, when the
// machine last started.
func DescribeAttach(vhd string, result AttachResult, isWritten bool, boot time.Time) string {
	stamp := func(at time.Time) string { return at.UTC().Format(time.RFC3339) }
	if !isWritten {
		return fmt.Sprintf("no result in %s yet: the task writes one each time it runs, and a task registered before it did writes none until an Attach or Renew item registers it again", AttachResultPath(vhd))
	}
	var outcome string
	if result.Attached {
		outcome = fmt.Sprintf("attached %s at %s, on try %d of %d", result.Letter, stamp(result.Finished), result.Tries, AttachTries)
	} else {
		outcome = fmt.Sprintf("did not attach %s in %d tries, the last ending at %s", result.Letter, result.Tries, stamp(result.Finished))
		if len(result.Errors) > 0 {
			outcome += ": " + result.Errors[len(result.Errors)-1]
		}
	}
	if result.Finished.Before(boot) {
		return fmt.Sprintf("it has not run since this boot at %s, and its last run %s", stamp(boot), outcome)
	}
	return fmt.Sprintf("since this boot at %s it %s", stamp(boot), outcome)
}
