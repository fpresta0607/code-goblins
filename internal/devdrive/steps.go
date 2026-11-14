package devdrive

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// Step is one thing that puts a home's heavy folders and the gate's home on a
// Dev Drive: a person does each with one click in the Command Center, but
// the last, StepGate, which needs nobody and which the supervisor takes
// itself.
type Step string

const (
	// StepCreate makes the Dev Drive.
	StepCreate Step = "create"
	// StepAttach attaches the Dev Drive a home's heavy folders are on again,
	// when Windows did not.
	StepAttach Step = "attach"
	// StepTask registers the startup task again on a drive that is
	// attached, where the task was registered before it attached the drive
	// by itself and the board could start it.
	StepTask Step = "task"
	// StepTrust trusts a Dev Drive Windows does not trust.
	StepTrust Step = "trust"
	// StepMove moves the home's heavy folders onto it, and the gate's home
	// with them.
	StepMove Step = "move"
	// StepGate moves the gate's home onto the Dev Drive the home's heavy
	// folders are on, when it did not move with them: gate runs were in
	// flight all the while the move waited, or the heavy folders moved before
	// the move carried the gate's home. It needs no person and no
	// administrator, so it is never a Command Center item.
	StepGate Step = "gate"
)

// GateHomeFolder is the folder in a home's Dev Drive folder the gate's home
// moves to, beside the fleet's own there.
const GateHomeFolder = "no-mistakes"

// Plan is the next step for a home and what it runs: Script for a step that
// runs as administrator, Folder for the move and the gate step.
type Plan struct {
	Step Step
	// Title names the step's Command Center item; the gate step has none.
	Title  string
	Admin  bool
	Script string
	Folder string
	// VHD is the file a create step makes or attaches.
	VHD string
}

// Next is the step h needs next on machine m, or false when it needs none:
// its heavy folders and the gate's home are on a Dev Drive Windows does not
// distrust, or this machine can have no Dev Drive Code Goblins would use.
// config is the home's config\dev-drive.json, which names the file a create
// step made, shouldMoveGateHome says the gate's home is not yet in h's Dev
// Drive folder and a move would carry it there, and shouldRenewTask says the
// startup task of the drive config names is not the one this build registers.
func Next(m Machine, h home.Home, config home.DevDriveConfig, shouldMoveGateHome, shouldRenewTask bool) (Plan, bool) {
	if m.Unavailable() != "" {
		return Plan{}, false
	}
	if h.DevDrive != "" {
		v, found := m.VolumeOf(h.DevDrive)
		switch {
		case !found && config.VHD != "":
			return attach(config.VHD, filepath.VolumeName(h.DevDrive)), true
		case found && v.Dev && v.Trust == Untrusted:
			return trust(v), true
		case found && v.Dev && config.VHD != "" && shouldRenewTask:
			return renew(config.VHD, v.Letter()), true
		case found && shouldMoveGateHome:
			return Plan{Step: StepGate, Folder: h.DevDrive}, true
		}
		return Plan{}, false
	}
	if v, found := m.Reusable(); found {
		if v.Trust == Untrusted {
			return trust(v), true
		}
		folder := Folder(v)
		return Plan{Step: StepMove, Folder: folder, Title: fmt.Sprintf("Move Code Goblins' worktrees, scratch and package caches to %s on the Dev Drive %s: new goblins build there, goblins already started keep their folders, and the gate's home moves to %s once no gate run is in flight, copied, checked and named by NM_HOME", folder, v.Letter(), filepath.Join(folder, GateHomeFolder))}, true
	}
	if m.CannotCreate() != "" {
		return Plan{}, false
	}
	letter := m.FreeLetter()
	if letter == "" {
		return Plan{}, false
	}
	vhd := NewVHD(m)
	size := m.SizeGB()
	return Plan{
		Step:   StepCreate,
		Admin:  true,
		VHD:    vhd,
		Script: CreateScript(vhd, letter, size),
		Title:  fmt.Sprintf("Create the Code Goblins Dev Drive %s (administrator): a %d GB VHDX at %s that takes space only as files land on it, formatted as a Dev Drive, and a startup task that attaches it at every boot. Defender keeps scanning it, in performance mode; this adds no exclusion", letter, size, vhd),
	}, true
}

func attach(vhd, letter string) Plan {
	return Plan{
		Step:   StepAttach,
		Admin:  true,
		VHD:    vhd,
		Script: CreateScript(vhd, letter, 0),
		Title:  fmt.Sprintf("Attach the Code Goblins Dev Drive %s again (administrator): it attaches %s, gives it back its letter and renews its startup task, so goblins can start and the next restart attaches it by itself. Nothing else changes", letter, vhd),
	}
}

func renew(vhd, letter string) Plan {
	return Plan{
		Step:   StepTask,
		Admin:  true,
		VHD:    vhd,
		Script: CreateScript(vhd, letter, 0),
		Title:  fmt.Sprintf("Renew the startup task of the Code Goblins Dev Drive %s (administrator): it attaches %s if it is not attached, gives it back its letter and renews its startup task, which from the next restart attaches it by itself and tries again while Windows is not ready, and which the board can start with no click. Nothing else changes", letter, vhd),
	}
}

func trust(v Volume) Plan {
	return Plan{
		Step:   StepTrust,
		Admin:  true,
		Script: TrustScript(v.Letter()),
		Title:  fmt.Sprintf("Trust the Dev Drive %s (administrator): fsutil devdrv trust %s, which turns on Defender's performance mode there, then fsutil devdrv query %s to show it; nothing else changes", v.Letter(), v.Letter(), v.Letter()),
	}
}

// NewVHD is the file a new Dev Drive goes in: DevDrives\CodeGoblins.vhdx on
// the system drive, where Microsoft's own Settings put one by default.
func NewVHD(m Machine) string {
	return filepath.Join(m.SystemDrive+`\`, "DevDrives", "CodeGoblins.vhdx")
}

// FreeLetter is the first drive letter from D: that nothing uses, or "".
func (m Machine) FreeLetter() string {
	for index := 'D' - 'A'; index < 26; index++ {
		if m.Letters&(1<<index) == 0 {
			return string(rune('A'+index)) + ":"
		}
	}
	return ""
}

// quote is s as a single-quoted PowerShell string.
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// CreateScript makes the Dev Drive in vhd, sizeGB at most, as letter, or,
// once the file exists, attaches it and gives it back its letter. It uses
// diskpart and the Storage cmdlets every edition has, not the Hyper-V module,
// and registers a startup task that attaches the file at every boot, because
// Windows does not attach a VHD again after a restart: it writes
// AttachScript beside the file for the task to run, as SYSTEM, at startup,
// on battery power too and with no time limit, and lets anyone signed in
// start the task, never change it. The file's folder is
// left to administrators and Windows alone, since Windows attaches what is in
// it as SYSTEM. It never touches Defender. Run twice, it changes nothing.
func CreateScript(vhd, letter string, sizeGB int) string {
	drive := strings.TrimSuffix(letter, ":")
	return "# Code Goblins: create the Dev Drive " + letter + ", or attach it again. Safe to run twice.\r\n" +
		"$ErrorActionPreference = 'Stop'\r\n" +
		"$vhd = " + quote(vhd) + "\r\n" +
		"$letter = " + quote(drive) + "\r\n" +
		"$folder = Split-Path -Parent $vhd\r\n" +
		"$image = Get-DiskImage -ImagePath $vhd -ErrorAction SilentlyContinue\r\n" +
		"if (-not $image) {\r\n" +
		"    if (Get-Volume -DriveLetter $letter -ErrorAction SilentlyContinue) { throw \"$($letter): is in use now; press Set up again on the board for an item with a free letter.\" }\r\n" +
		"    New-Item -ItemType Directory -Force -Path $folder | Out-Null\r\n" +
		"    # Windows attaches the file in this folder as SYSTEM at every boot, so only administrators and Windows may change it.\r\n" +
		"    icacls $folder /inheritance:r /grant:r '*S-1-5-32-544:(OI)(CI)F' '*S-1-5-18:(OI)(CI)F' | Out-Null\r\n" +
		"    if ($LASTEXITCODE) { throw \"icacls could not protect $folder\" }\r\n" +
		"    $commands = Join-Path $env:TEMP 'code-goblins-dev-drive.txt'\r\n" +
		"    Set-Content -Path $commands -Encoding ASCII -Value \"create vdisk file=`\"$vhd`\" maximum=" + fmt.Sprint(sizeGB*1024) + " type=expandable\"\r\n" +
		"    diskpart /s $commands\r\n" +
		"    if ($LASTEXITCODE) { throw \"diskpart could not create $vhd\" }\r\n" +
		"    Remove-Item $commands\r\n" +
		"    $image = Get-DiskImage -ImagePath $vhd\r\n" +
		"}\r\n" +
		"if (-not $image.Attached) { $image = Mount-DiskImage -ImagePath $vhd -PassThru }\r\n" +
		"$disk = $image | Get-Disk\r\n" +
		"if ($disk.PartitionStyle -eq 'RAW') {\r\n" +
		"    Initialize-Disk -Number $disk.Number -PartitionStyle GPT\r\n" +
		"    $partition = New-Partition -DiskNumber $disk.Number -UseMaximumSize -DriveLetter $letter\r\n" +
		"    $partition | Format-Volume -DevDrive -NewFileSystemLabel 'CodeGoblins' -Confirm:$false -Force | Out-Null\r\n" +
		"} else {\r\n" +
		"    $partition = Get-Partition -DiskNumber $disk.Number | Where-Object { $_.Type -eq 'Basic' } | Select-Object -First 1\r\n" +
		"    if ($partition.DriveLetter -ne $letter) { $partition | Set-Partition -NewDriveLetter $letter }\r\n" +
		"}\r\n" +
		"# The startup task runs this script as SYSTEM at every boot, from the folder only administrators and Windows may change.\r\n" +
		"$attachScript = Join-Path $folder " + quote(AttachScriptName) + "\r\n" +
		"Set-Content -LiteralPath $attachScript -Encoding UTF8 -Value @'\r\n" + AttachScript(vhd, letter) + "'@\r\n" +
		"$attach = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument \"-NoProfile -NonInteractive -ExecutionPolicy Bypass -File `\"$attachScript`\"\"\r\n" +
		"Register-ScheduledTask -TaskName " + quote(TaskName) + " -Trigger (New-ScheduledTaskTrigger -AtStartup) -Action $attach -Settings (" + taskSettings + ") -User 'NT AUTHORITY\\SYSTEM' -RunLevel Highest -Force | Out-Null\r\n" +
		"# Anyone signed in may read the task and start it, never change it, so the board starts it without elevation when the drive is missing.\r\n" +
		"$scheduler = New-Object -ComObject Schedule.Service\r\n" +
		"$scheduler.Connect()\r\n" +
		"$scheduler.GetFolder('\\').GetTask(" + quote(TaskName) + ").SetSecurityDescriptor(" + quote(taskSecurity) + ", 0)\r\n" +
		"fsutil devdrv query \"$($letter):\"\r\n"
}

// TrustScript trusts the Dev Drive letter, which turns on Defender's
// performance mode there, and shows what Windows says of it. Trusting a
// trusted drive changes nothing. It never runs fsutil devdrv enable
// /disallowAv, which takes the antivirus filter off every Dev Drive.
func TrustScript(letter string) string {
	return "# Code Goblins: trust the Dev Drive " + letter + ". Safe to run twice.\r\n" +
		"$ErrorActionPreference = 'Stop'\r\n" +
		"fsutil devdrv trust " + letter + "\r\n" +
		"if ($LASTEXITCODE) { throw 'fsutil devdrv trust " + letter + " failed' }\r\n" +
		"fsutil devdrv query " + letter + "\r\n"
}
