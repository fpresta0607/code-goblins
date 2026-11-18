//go:build linux || darwin

package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// A home others may open, and the folders the install makes in it, are
// kept to the installing account by mode 0700, and the next install reads
// the home as narrowed already.
func TestInstallKeepsTheHomeAndTheFoldersItMakesToTheOwner(t *testing.T) {
	// Arrange
	f := installedFixture(t, nil, minimalContract, minimalPolicy)
	f.service.Link = func(link, target string) error { return os.Symlink(target, link) }
	if err := os.Chmod(f.root, 0o755); err != nil {
		t.Fatal(err)
	}

	// Act
	output := f.install()

	// Assert
	for _, path := range []string{f.root, filepath.Join(f.root, home.WorktreesDir)} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Errorf("%s has mode %v, want 0700", path, info.Mode().Perm())
		}
	}
	if !strings.Contains(output, f.root+" now open only to you\n") {
		t.Errorf("the install does not say it narrowed the home:\n%s", output)
	}
	if again := f.install(); !strings.Contains(again, f.root+"'s mode admits only you\n") || strings.Contains(again, "now open only") {
		t.Errorf("a second install does not say the home was already narrowed:\n%s", again)
	}
}
