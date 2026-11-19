package readcache

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// A status's sum passes over a folder the last status named ignored in
// another case, and a folder named .git in another case, exactly where the
// file system opens the one spelling by the other: a change inside it then
// leaves the sum as it was.
func TestStatusPassesOverIgnoredAndGitFoldersByTheFileSystemsCase(t *testing.T) {
	cases := []struct {
		name      string
		folder    string
		other     string
		porcelain string
	}{
		{name: "an ignored folder named in another case", folder: "build", other: "BUILD", porcelain: "!! app/BUILD/\x00"},
		{name: "a .git folder in another case", folder: ".GIT", other: ".git", porcelain: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			worktree := t.TempDir()
			folder := filepath.Join(worktree, "app", tc.folder)
			if err := os.MkdirAll(folder, 0o755); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(filepath.Join(worktree, "app", tc.other))
			isOnePath := err == nil
			repo := repository{worktree: worktree, gitDir: t.TempDir(), commonDir: t.TempDir()}
			previous := &execx.Result{Stdout: []byte(tc.porcelain)}
			now := time.Now().Add(time.Hour)
			sum := func() [32]byte {
				s := newStamper(now)
				status(s, repo, previous)
				value, _ := s.sum()
				return value
			}
			before := sum()
			if err := os.WriteFile(filepath.Join(folder, "made.txt"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			after := sum()

			// Assert
			if isPassedOver := before == after; isPassedOver != isOnePath {
				t.Fatalf("a change in %s left the sum as it was: %t, want %t, as the file system opens %s by %s: %t", folder, isPassedOver, isOnePath, tc.folder, tc.other, isOnePath)
			}
		})
	}
}
