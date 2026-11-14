package uninstall

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

func TestADevDriveCodeGoblinsMadeGoesAsAdministratorAndOneThePersonHadKeepsAllButItsFolder(t *testing.T) {
	t.Run("made by Code Goblins", func(t *testing.T) {
		// Arrange
		m := testMachine(t)
		m.Commands = &recordingRunner{out: "0"}
		if err := home.WriteDevDriveConfig(m.Home.Root, home.DevDriveConfig{Root: `Q:\CodeGoblins`, VHD: `C:\DevDrives\CodeGoblins.vhdx`}); err != nil {
			t.Fatal(err)
		}

		// Act
		items, err := m.devDrive(context.Background())

		// Assert
		if err != nil || len(items) != 3 {
			t.Fatalf("found\n%s\n%v; want the drive, its task and its folder", whats(items), err)
		}
		for _, item := range items {
			if !item.Admin || !strings.Contains(item.Script, "$still") {
				t.Errorf("%s is not an administrator's part that reads itself back: %q", item.What, item.Script)
			}
		}
		if !strings.Contains(items[0].Script, "Dismount-DiskImage") || !strings.Contains(items[0].Script, "mountvol.exe") || !strings.Contains(items[1].Script, "Unregister-ScheduledTask") {
			t.Errorf("the scripts do not detach the drive, free its letter and remove its task:\n%s\n%s", items[0].Script, items[1].Script)
		}
		for _, item := range items {
			if strings.Contains(item.Script, "Set-MpPreference") || strings.Contains(item.Script, "Add-MpPreference") {
				t.Errorf("%s changes a Defender setting", item.What)
			}
		}
	})
	t.Run("one the person had", func(t *testing.T) {
		// Arrange
		m := testMachine(t)
		folder := filepath.Join(t.TempDir(), "CodeGoblins")
		write(t, filepath.Join(folder, "worktrees", "x"), "x")
		if err := home.WriteDevDriveConfig(m.Home.Root, home.DevDriveConfig{Root: folder}); err != nil {
			t.Fatal(err)
		}

		// Act
		items, err := m.devDrive(context.Background())

		// Assert
		if err != nil || len(items) != 1 || items[0].Admin || items[0].Where != folder {
			t.Fatalf("found\n%s\n%v; want only the Code Goblins folder on it", whats(items), err)
		}
	})
}

// The startup task's part, run in Windows PowerShell 5.1 as the
// administrator's script runs it, with stand-ins for the task cmdlets,
// removes the task, the script it runs and the result it writes beside the
// drive's file, so the folder empties, and finds nothing still there.
func TestTheStartupTasksPartRemovesTheTaskItsScriptAndItsResult(t *testing.T) {
	// Arrange
	m := testMachine(t)
	m.Commands = &recordingRunner{out: "0"}
	folder := t.TempDir()
	vhd := filepath.Join(folder, "CodeGoblins.vhdx")
	files := []string{filepath.Join(folder, "CodeGoblins-attach.ps1"), filepath.Join(folder, "CodeGoblins-attach.json")}
	for _, file := range files {
		write(t, file, "x")
	}
	if err := home.WriteDevDriveConfig(m.Home.Root, home.DevDriveConfig{Root: `Q:\CodeGoblins`, VHD: vhd}); err != nil {
		t.Fatal(err)
	}
	items, err := m.devDrive(context.Background())
	if err != nil || len(items) != 3 {
		t.Fatalf("found\n%s\n%v; want the drive, its task and its folder", whats(items), err)
	}
	standIns := "$ErrorActionPreference = 'Continue'\r\n$still = @()\r\nImport-Module ScheduledTasks\r\n" +
		"function Unregister-ScheduledTask { [CmdletBinding(SupportsShouldProcess = $true)] param($TaskName) Write-Host \"unregistered $TaskName\" }\r\n" +
		"function Get-ScheduledTask { [CmdletBinding()] param($TaskName) }\r\n"
	path := filepath.Join(t.TempDir(), "remove.ps1")
	write(t, path, standIns+items[1].Script+"\r\nWrite-Host \"still $($still.Count)\"\r\n")

	// Act
	output, err := exec.Command(windowsPowerShell(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path).CombinedOutput()

	// Assert
	said := string(output)
	if err != nil || !strings.Contains(said, "unregistered Code Goblins Dev Drive") || !strings.Contains(said, "still 0") {
		t.Errorf("the task's part said %q, %v; want the task removed and nothing still there", said, err)
	}
	for _, file := range files {
		if _, err := os.Stat(file); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s is still there (%v), so the folder never empties", file, err)
		}
	}
}
