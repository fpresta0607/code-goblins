package pipeline

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

func TestCommandSourceVerifiesManagedProvenanceBeforeEitherBinding(t *testing.T) {
	for _, location := range []string{"managed", "base clone"} {
		for _, scenario := range []string{"valid", "non-git managed directory", "replacement checkout", "wrong managed origin", "invalid base", "missing base", "base is a tree", "wrong clone origin", "wrong clone head"} {
			t.Run(location+"/"+scenario, func(t *testing.T) {
				root, project := t.TempDir(), t.TempDir()
				runPipelineGit(t, project, "init", "--quiet", "--initial-branch=main")
				runPipelineGit(t, project, "-c", "user.name=test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "--quiet", "--allow-empty", "-m", "base")
				runPipelineGit(t, project, "remote", "add", "origin", "https://example.invalid/project.git")
				baseCommit := runPipelineGit(t, project, "rev-parse", "HEAD")
				runPipelineGit(t, project, "-c", "user.name=test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "--quiet", "--allow-empty", "-m", "head")
				bare := filepath.Join(root, "repos", "repo.git")
				managed := filepath.Join(root, "worktrees", "repo", "run")
				runPipelineGit(t, project, "clone", "--quiet", "--bare", project, bare)
				runPipelineGit(t, project, "--git-dir="+bare, "remote", "set-url", "origin", "https://example.invalid/project.git")
				runPipelineGit(t, project, "--git-dir="+bare, "worktree", "add", "--quiet", "--detach", managed, "HEAD")
				clone := filepath.Join(root, "base")
				runPipelineGit(t, project, "clone", "--quiet", "--shared", managed, clone)
				runPipelineGit(t, clone, "checkout", "--quiet", "--detach", baseCommit)
				directory := managed
				if location == "base clone" {
					directory = clone
				}
				switch scenario {
				case "non-git managed directory", "replacement checkout":
					if err := os.Rename(managed, managed+"-original"); err != nil {
						t.Fatal(err)
					}
					if scenario == "replacement checkout" {
						runPipelineGit(t, project, "clone", "--quiet", project, managed)
					} else if err := os.MkdirAll(managed, 0o755); err != nil {
						t.Fatal(err)
					}
				case "wrong managed origin":
					runPipelineGit(t, managed, "remote", "set-url", "origin", "https://example.invalid/another.git")
				case "invalid base":
					baseCommit = "HEAD"
				case "missing base":
					baseCommit = strings.Repeat("0", 40)
				case "base is a tree":
					baseCommit = runPipelineGit(t, managed, "rev-parse", "HEAD^{tree}")
				case "wrong clone origin":
					runPipelineGit(t, clone, "remote", "set-url", "origin", project)
				case "wrong clone head":
					runPipelineGit(t, clone, "checkout", "--quiet", "--detach", "origin/main")
				}
				sql := `CREATE TABLE repos(id TEXT,working_path TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,worktree_dir TEXT,base_sha TEXT);
INSERT INTO repos VALUES('repo',` + sqlString(project) + `);
INSERT INTO runs VALUES('run','repo','main',` + sqlString(managed) + `,` + sqlString(baseCommit) + `);`
				if output, err := exec.Command("sqlite3", filepath.Join(root, "state.sqlite"), sql).CombinedOutput(); err != nil {
					t.Fatalf("record source: %s (%v)", output, err)
				}
				reader := Reader{Root: root, Commands: execx.OSRunner{}}

				source, err := reader.CommandSource(t.Context(), directory, "run")

				isValid := scenario == "valid" || location == "managed" && (scenario == "wrong clone origin" || scenario == "wrong clone head")
				if isValid {
					if err != nil || source != (RunSource{Project: project, Branch: "main", Worktree: managed}) {
						t.Fatalf("source=%+v err=%v; want the recorded source", source, err)
					}
				} else if err == nil || source != (RunSource{}) {
					t.Fatalf("source=%+v err=%v; want invalid provenance refused", source, err)
				}
			})
		}
	}
}

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
