package fsx_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/standin"
)

// loadAsImage puts a program at path and maps it, until the test ends, as
// Windows maps a program or a DLL a process loads: as an image, though no
// code of it runs.
func loadAsImage(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	standin.Put(t, path)
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
			err := fsx.RemoveAllWithin(folder, 100*time.Millisecond)

			// Assert
			_, folderErr := os.Lstat(folder)
			if isGone := errors.Is(folderErr, os.ErrNotExist); isGone != test.isRemoved || (err == nil) != test.isRemoved {
				t.Errorf("RemoveAllWithin = %v, the folder gone %v; want it gone %v", err, isGone, test.isRemoved)
			}
			if !test.isRemoved && !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
				t.Errorf("RemoveAllWithin = %v; want it refused as access denied, which a caller reads as a hold", err)
			}
			if _, err := os.Stat(loaded); err != nil {
				t.Errorf("the loaded file = %v; want it in place", err)
			}
		})
	}
}
