package pipeline

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

func TestWorktreeSourceRejectsAnUnmanagedDirectory(t *testing.T) {
	for _, name := range []string{"outside root", "invalid run identity"} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			root, project := t.TempDir(), t.TempDir()
			worktree := filepath.Join(root, "worktrees", "repo", "invalid run")
			if name == "outside root" {
				worktree = filepath.Join(t.TempDir(), "worktrees", "repo", "run")
			}
			if err := os.MkdirAll(worktree, 0o755); err != nil {
				t.Fatal(err)
			}
			sql := `CREATE TABLE repos(id TEXT,working_path TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,worktree_dir TEXT);
INSERT INTO repos VALUES('repo',` + sqlString(project) + `);
INSERT INTO runs VALUES(` + sqlString(filepath.Base(worktree)) + `,'repo','feat/task',` + sqlString(worktree) + `);`
			if output, err := exec.Command("sqlite3", filepath.Join(root, "state.sqlite"), sql).CombinedOutput(); err != nil {
				t.Fatalf("write unmanaged source: %s (%v)", output, err)
			}
			reader := Reader{Root: root, Commands: execx.OSRunner{}}

			// Act
			source, err := reader.WorktreeSource(context.Background(), worktree)

			// Assert
			if source != (RunSource{}) || err == nil {
				t.Fatalf("source=%+v, err=%v; want no run from an unmanaged directory", source, err)
			}
		})
	}
}
