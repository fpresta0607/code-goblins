package fsx

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// loadAsImage puts a program at path and maps it, until the test ends, as
// Windows maps a program or a DLL a process loads: as an image, though no
// code of it runs.
func loadAsImage(t *testing.T, path string) {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(program)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatal(err)
	}
	module, err := windows.LoadLibraryEx(path, 0, windows.LOAD_LIBRARY_AS_IMAGE_RESOURCE)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { windows.FreeLibrary(module) })
}

// #1226, 2026-10-20: uv links one file of its cache into every goblin's
// .venv, and Go's own removal refuses every name of a file a process has
// loaded, so one goblin's pytest kept the other goblins' worktrees of its
// project on disk. A name another link keeps the file behind goes. The only
// name of a loaded file, which a process runs from the folder itself, still
// holds the folder, as a hold the caller leaves for later.
func TestRemoveAllTakesANameOfALoadedImageOnlyWhileAnotherNameKeepsIt(t *testing.T) {
	budget := transientBudget
	transientBudget = 100 * time.Millisecond
	t.Cleanup(func() { transientBudget = budget })
	for name, test := range map[string]struct {
		isLoadedElsewhere bool
		isRemoved         bool
	}{
		"a name another link keeps": {isLoadedElsewhere: true, isRemoved: true},
		"the only name":             {},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			folder := filepath.Join(t.TempDir(), "worktree")
			module := filepath.Join(folder, ".venv", "Lib", "site-packages", "curl_cffi", "_wrapper.pyd")
			loaded := module
			if test.isLoadedElsewhere {
				loaded = filepath.Join(t.TempDir(), "uv-cache", "_wrapper.pyd")
			}
			loadAsImage(t, loaded)
			if test.isLoadedElsewhere {
				if err := os.MkdirAll(filepath.Dir(module), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(loaded, module); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			err := RemoveAll(folder)

			// Assert
			_, folderErr := os.Lstat(folder)
			if isGone := errors.Is(folderErr, os.ErrNotExist); isGone != test.isRemoved || (err == nil) != test.isRemoved {
				t.Errorf("RemoveAll = %v, the folder gone %v; want it gone %v", err, isGone, test.isRemoved)
			}
			if !test.isRemoved && !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
				t.Errorf("RemoveAll = %v; want it refused as access denied, which a caller reads as a hold", err)
			}
			if _, err := os.Stat(loaded); err != nil {
				t.Errorf("the loaded file = %v; want it in place", err)
			}
		})
	}
}

// RemoveAll is what removes a goblin's worktree, whose node_modules pnpm
// fills with junctions into a store every project on the machine shares. So
// it is held to removing a link as a link here, against the Go release this
// builds with: one that walked into a junction would delete the store.
func TestRemoveAllRemovesALinkAsALinkAndEveryFilePnpmMakes(t *testing.T) {
	// Arrange
	folder := filepath.Join(t.TempDir(), "worktree")
	pnpm := filepath.Join(folder, "node_modules", ".pnpm")
	store := filepath.Join(t.TempDir(), "store")
	stored := filepath.Join(store, "index.js")
	readOnly := filepath.Join(pnpm, "a@1.0.0", "index.js")
	long := filepath.Join(pnpm, "b@1.0.0", strings.Repeat("segment-of-a-deep-package-path"+string(filepath.Separator), 8), "index.js")
	if len(long) <= 260 {
		t.Fatalf("the long path is %d characters, want past 260", len(long))
	}
	for _, path := range []string{stored, readOnly, long} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("a file of the shared store"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(readOnly, 0o444); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{
		filepath.Join(folder, "node_modules", "linked"): store,
		filepath.Join(folder, "node_modules", "a"):      filepath.Join(pnpm, "a@1.0.0"),
	} {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
			t.Fatalf("mklink /J %s %s: %v: %s", link, target, err, out)
		}
	}

	// Act
	err := RemoveAll(folder)

	// Assert
	if err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if _, err := os.Lstat(folder); !os.IsNotExist(err) {
		t.Errorf("the folder is still there: %v", err)
	}
	if content, err := os.ReadFile(stored); err != nil || string(content) != "a file of the shared store" {
		t.Errorf("the store's file behind the junction is %q, %v, want it untouched", content, err)
	}
}
