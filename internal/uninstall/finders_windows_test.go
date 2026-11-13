package uninstall

import (
	"context"
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
