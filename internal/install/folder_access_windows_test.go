package install

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/access/accesstest"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// homeUnderADriveRoot is an install fixture whose home is made in a folder
// that lets Authenticated Users modify everything made in it, as a drive's
// root does.
func homeUnderADriveRoot(t *testing.T) *fixture {
	t.Helper()
	f := installedFixture(t, nil, minimalContract, minimalPolicy)
	f.root = filepath.Join(accesstest.DriveRootLike(t), "code-goblins")
	f.bin = filepath.Join(f.root, home.BinDir)
	f.service.Root = f.root
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(f.root))
	return f
}

// A home and the Dev Drive folder its worktrees, scratch and caches moved
// to, each made where Authenticated Users may modify them, are kept to the
// installing account, SYSTEM and Administrators, with what they already hold.
func TestInstallKeepsTheHomeAndItsDevDriveFolderToTheOwnerSystemAndAdministrators(t *testing.T) {
	// Arrange
	f := homeUnderADriveRoot(t)
	work := filepath.Join(f.root, home.WorktreesDir, "project", "task", "main.go")
	writeFile(t, work, "package main")
	devDrive := filepath.Join(accesstest.DriveRootLike(t), "CodeGoblins")
	cache := filepath.Join(devDrive, home.CachesDir, "go-mod", "module.zip")
	writeFile(t, cache, "module")
	if err := home.WriteDevDriveConfig(f.root, home.DevDriveConfig{Root: devDrive}); err != nil {
		t.Fatal(err)
	}
	want := accesstest.Narrowed(t)

	// Act
	output := f.install()

	// Assert
	for _, path := range []string{f.root, devDrive} {
		if sids, isProtected := accesstest.Allowed(t, path); !slices.Equal(sids, want) || !isProtected {
			t.Errorf("%s allows %v, protected %v; want only %v, taking nothing from its parent", path, sids, isProtected, want)
		}
	}
	for _, path := range []string{work, cache, filepath.Join(f.bin, "cfo.exe")} {
		if sids, _ := accesstest.Allowed(t, path); !slices.Equal(sids, want) {
			t.Errorf("%s allows %v, want only %v", path, sids, want)
		}
	}
	if !strings.Contains(output, "now open only to you, SYSTEM and Administrators") {
		t.Errorf("the install does not say it narrowed the folders:\n%s", output)
	}
	if again := f.install(); !strings.Contains(again, "open only to you, SYSTEM and Administrators") || strings.Contains(again, "now open only") {
		t.Errorf("a second install does not say the folders were already narrowed:\n%s", again)
	}
}

// A Dev Drive that is not attached leaves its folder unnarrowed and the
// install going, saying so.
func TestInstallWithItsDevDriveDetachedNarrowsTheHomeAndSaysSo(t *testing.T) {
	// Arrange
	f := homeUnderADriveRoot(t)
	detached := filepath.Join(t.TempDir(), "gone", "CodeGoblins")
	if err := home.WriteDevDriveConfig(f.root, home.DevDriveConfig{Root: detached}); err != nil {
		t.Fatal(err)
	}

	// Act
	output := f.install()

	// Assert
	if sids, _ := accesstest.Allowed(t, f.root); !slices.Equal(sids, accesstest.Narrowed(t)) {
		t.Errorf("the home allows %v, want only %v", sids, accesstest.Narrowed(t))
	}
	if !strings.Contains(output, detached+" is not there to narrow") {
		t.Errorf("the install does not say the Dev Drive folder was not there:\n%s", output)
	}
}
