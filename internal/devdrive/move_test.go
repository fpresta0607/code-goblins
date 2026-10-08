package devdrive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// devDriveAt is a machine whose drive holding dir is a Dev Drive with trust.
func devDriveAt(dir string, trust Trust) Machine {
	m := ready()
	m.Volumes = []Volume{{Root: filepath.VolumeName(dir) + `\`, FileSystem: "ReFS", Dev: true, Trust: trust, Free: 190 * gigabyte, Total: 200 * gigabyte}}
	return m
}

func TestMovePutsTheHeavyFoldersOnATrustedDevDrive(t *testing.T) {
	// Arrange
	h := home.Home{Root: t.TempDir()}
	folder := filepath.Join(t.TempDir(), "CodeGoblins")
	now := time.Date(2026, 10, 8, 2, 0, 0, 0, time.UTC)

	// Act
	said, err := Move(devDriveAt(folder, Trusted), h, folder, now)

	// Assert
	if err != nil || !strings.Contains(said, "moved") || !strings.Contains(said, "keep their folders") {
		t.Fatalf("Move = %q, %v; want it moved, saying started goblins keep their folders", said, err)
	}
	for _, sub := range []string{"worktrees", "scratch", "caches"} {
		if info, err := os.Stat(filepath.Join(folder, sub)); err != nil || !info.IsDir() {
			t.Errorf("%s was not made: %v", sub, err)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(folder, "scratch")); len(entries) != 0 {
		t.Errorf("the write probe was left behind: %v", entries)
	}
	config, err := home.ReadDevDriveConfig(h.Root)
	if err != nil || config.Root != folder || !config.MovedAt.Equal(now) {
		t.Errorf("config = %+v, %v; want %s moved at %s", config, err, folder, now)
	}

	// Run twice, it changes nothing.
	h.DevDrive = folder
	if said, err := Move(devDriveAt(folder, Trusted), h, folder, now.Add(time.Hour)); err != nil || !strings.Contains(said, "nothing changed") {
		t.Errorf("a second Move = %q, %v; want nothing changed", said, err)
	}
	if config, _ := home.ReadDevDriveConfig(h.Root); !config.MovedAt.Equal(now) {
		t.Errorf("a second Move rewrote when it moved: %s", config.MovedAt)
	}
}

func TestMoveRefusesWhatWouldNotBeFasterOrCouldLoseTrack(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(t.TempDir(), "CodeGoblins")
	plain := ready()
	plain.Volumes = []Volume{{Root: filepath.VolumeName(folder) + `\`, FileSystem: "NTFS"}}
	defenderOff := devDriveAt(folder, Trusted)
	defenderOff.Defender = false
	for name, test := range map[string]struct {
		m      Machine
		h      home.Home
		folder string
		says   string
	}{
		"a relative folder":            {devDriveAt(folder, Trusted), home.Home{Root: root}, "CodeGoblins", "not an absolute folder"},
		"a folder inside the home":     {devDriveAt(root, Trusted), home.Home{Root: root}, filepath.Join(root, "dd"), "inside the home"},
		"a drive that is no Dev Drive": {plain, home.Home{Root: root}, folder, "not a Dev Drive"},
		"an untrusted Dev Drive":       {devDriveAt(folder, Untrusted), home.Home{Root: root}, folder, "does not trust"},
		"Defender's protection off":    {defenderOff, home.Home{Root: root}, folder, "real-time protection is off"},
		"a second move elsewhere":      {devDriveAt(folder, Trusted), home.Home{Root: root, DevDrive: `E:\CodeGoblins`}, folder, "already on"},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			said, err := Move(test.m, test.h, test.folder, time.Now())

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.says) {
				t.Fatalf("Move = %q, %v; want a refusal saying %q", said, err, test.says)
			}
			if _, err := os.Stat(home.DevDriveConfigPath(root)); !os.IsNotExist(err) {
				t.Errorf("a refused move wrote the config: %v", err)
			}
		})
	}
}
