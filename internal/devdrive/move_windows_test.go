package devdrive

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/access/accesstest"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// The folder at a Dev Drive's root that goblins build in would let
// Authenticated Users modify it, as the drive's root does, so the move keeps
// it to the account it runs as, SYSTEM and Administrators.
func TestMoveKeepsTheFolderToTheOwnerSystemAndAdministrators(t *testing.T) {
	// Arrange
	h := home.Home{Root: t.TempDir()}
	folder := filepath.Join(accesstest.DriveRootLike(t), "CodeGoblins")

	// Act
	if _, err := Move(devDriveAt(folder, Trusted), h, folder, time.Now()); err != nil {
		t.Fatal(err)
	}

	// Assert
	want := accesstest.Narrowed(t)
	for _, path := range []string{folder, filepath.Join(folder, home.WorktreesDir), filepath.Join(folder, home.CachesDir)} {
		if sids, _ := accesstest.Allowed(t, path); !slices.Equal(sids, want) {
			t.Errorf("%s allows %v, want only %v", path, sids, want)
		}
	}
}
