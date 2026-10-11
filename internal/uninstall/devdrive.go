package uninstall

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// devDriveTask is the startup task the Dev Drive's create step registers,
// which attaches the drive at every boot.
const devDriveTask = "Code Goblins Dev Drive"

// volumeFolders are what Windows itself keeps at a drive's root, which are
// no files of the person's.
var volumeFolders = []string{"System Volume Information", "$RECYCLE.BIN"}

// devDrive is the Dev Drive the home's heavy folders are on. One Code
// Goblins made, as the home's config\dev-drive.json records, is removed with
// one administrator consent: detached, its letter freed, its file deleted,
// its startup task removed and its folder too when that is empty. One that
// holds files of the person's, and one the person already had, keep
// everything but the home's own folder on it.
func (m Machine) devDrive(ctx context.Context) ([]Item, error) {
	config, err := home.ReadDevDriveConfig(m.Home.Root)
	if err != nil {
		return nil, err
	}
	var items []Item
	folder := config.Root
	letter := filepath.VolumeName(folder)
	if config.VHD == "" || letter == "" {
		if folder == "" {
			return nil, nil
		}
		isThere, err := there(folder)
		if err != nil || !isThere {
			return nil, err
		}
		return []Item{removal(GroupDevDrive, "the Code Goblins folder on your Dev Drive "+letter, folder)}, nil
	}
	attached, err := m.attached(ctx, config.VHD)
	if err != nil {
		return nil, err
	}
	if attached {
		others, err := othersOn(letter, folder)
		if err != nil {
			return nil, err
		}
		if len(others) > 0 {
			items = append(items, Item{Action: Leave, Group: GroupDevDrive, What: fmt.Sprintf("the Dev Drive %s, since it holds files that are not Code Goblins', such as %s", letter, strings.Join(others[:min(3, len(others))], ", ")), Where: config.VHD})
			if isThere, _ := there(folder); isThere {
				items = append(items, removal(GroupDevDrive, "the Code Goblins folder on the Dev Drive "+letter, folder))
			}
			return items, nil
		}
	}
	// An ordinary user cannot see the file, its folder or the task, which
	// only administrators may read, so each is in the plan while the home
	// names the drive, and the administrator's script reads each back.
	vhd := quote(config.VHD)
	items = append(items, Item{Action: Remove, Group: GroupDevDrive, Admin: true, What: "the Dev Drive " + letter, Where: config.VHD,
		Fix:    "restart the PC first if another program has the drive open, then run the uninstall again",
		Script: devDriveScript(vhd, quote(strings.TrimSuffix(letter, ":")))})
	items = append(items, Item{Action: Remove, Group: GroupDevDrive, Admin: true, What: "its startup task, " + devDriveTask, Where: `\` + devDriveTask,
		Script: "Unregister-ScheduledTask -TaskName " + quote(devDriveTask) + " -Confirm:$false -ErrorAction SilentlyContinue\r\n" +
			"if (Get-ScheduledTask -TaskName " + quote(devDriveTask) + " -ErrorAction SilentlyContinue) { $still += " + quote(`\`+devDriveTask) + " }"})
	parent := filepath.Dir(config.VHD)
	items = append(items, Item{Action: Remove, Group: GroupDevDrive, Admin: true, What: "its folder, once nothing else is in it", Where: parent,
		Script: "$folder = " + quote(parent) + "\r\n" +
			"if ((Test-Path -LiteralPath $folder) -and -not (Get-ChildItem -LiteralPath $folder -Force)) { Remove-Item -LiteralPath $folder -Force }\r\n" +
			"if ((Test-Path -LiteralPath $folder) -and -not (Get-ChildItem -LiteralPath $folder -Force)) { $still += $folder }"})
	return items, nil
}

// devDriveScript detaches the drive, frees its letter, so Windows gives it
// to no drive in its place, and deletes its file, then names the file in
// $still while it is there.
func devDriveScript(vhd, letter string) string {
	return "$vhd = " + vhd + "\r\n" +
		"$letter = " + letter + "\r\n" +
		"$image = Get-DiskImage -ImagePath $vhd -ErrorAction SilentlyContinue\r\n" +
		"if ($image -and $image.Attached) {\r\n" +
		"    $volume = Get-Volume -DriveLetter $letter -ErrorAction SilentlyContinue\r\n" +
		"    mountvol.exe \"$($letter):\\\" /D | Out-Null\r\n" +
		"    Dismount-DiskImage -ImagePath $vhd -ErrorAction SilentlyContinue | Out-Null\r\n" +
		"    if ($volume -and $volume.UniqueId -match '\\{[0-9A-Fa-f-]+\\}') { Remove-ItemProperty -Path 'HKLM:\\SYSTEM\\MountedDevices' -Name ('\\??\\Volume' + $Matches[0]) -ErrorAction SilentlyContinue }\r\n" +
		"}\r\n" +
		"Remove-Item -LiteralPath $vhd -Force -ErrorAction SilentlyContinue\r\n" +
		"if (Test-Path -LiteralPath $vhd) { $still += $vhd }"
}

// attached reports whether Windows has the virtual disk vhd attached, which
// an ordinary user can read.
func (m Machine) attached(ctx context.Context, vhd string) (bool, error) {
	result, err := m.Commands.Run(ctx, execx.Request{Name: "powershell.exe", Args: []string{"-NoProfile", "-NonInteractive", "-Command",
		"@(Get-Disk | Where-Object { $_.Location -eq " + quote(vhd) + " }).Count"}})
	if err != nil {
		return false, err
	}
	if result.ExitCode != 0 {
		return false, fmt.Errorf("uninstall: ask Windows which virtual disks are attached: %s", strings.TrimSpace(string(result.Stderr)))
	}
	return strings.TrimSpace(string(result.Stdout)) != "0", nil
}

// othersOn names what is at the root of the drive letter other than the
// home's folder and what Windows keeps there.
func othersOn(letter, folder string) ([]string, error) {
	entries, err := os.ReadDir(letter + `\`)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("uninstall: read what the Dev Drive %s holds: %w", letter, err)
	}
	var others []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.EqualFold(filepath.Join(letter+`\`, name), filepath.Clean(folder)) || slices.ContainsFunc(volumeFolders, func(own string) bool { return strings.EqualFold(own, name) }) {
			continue
		}
		others = append(others, name)
	}
	return others, nil
}

// quote is s as a single-quoted PowerShell string.
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
