package main

import (
	"bytes"
	"path/filepath"
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/access/accesstest"
	"github.com/fpresta0607/code-goblins/internal/devdrive"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// The folder at a Dev Drive's root that goblins build in would let
// Authenticated Users modify it, as the drive's root does, so the move keeps
// it to the account it runs as, SYSTEM and Administrators.
func TestDevDriveMoveKeepsTheFolderToTheOwnerSystemAndAdministrators(t *testing.T) {
	// Arrange
	t.Setenv("CFO_HOME", t.TempDir())
	t.Setenv("CFO_STATE_OVERRIDE", "")
	folder := filepath.Join(accesstest.DriveRootLike(t), "CodeGoblins")
	runtime := runtimeOn(machineWith(devdrive.Volume{Root: filepath.VolumeName(folder) + `\`, FileSystem: "ReFS", Dev: true, Trust: devdrive.Trusted}))

	// Act
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"dev-drive", "move", "--to", folder}, &stdout, &stderr, runtime)

	// Assert
	if exit != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", exit, stdout.String(), stderr.String())
	}
	want := accesstest.Narrowed(t)
	for _, path := range []string{folder, filepath.Join(folder, home.WorktreesDir), filepath.Join(folder, home.CachesDir)} {
		if sids, _ := accesstest.Allowed(t, path); !slices.Equal(sids, want) {
			t.Errorf("%s allows %v, want only %v", path, sids, want)
		}
	}
}
