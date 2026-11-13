package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestAGateTaskRejectsUnboundRunAndOwnerEvidence(t *testing.T) {
	for _, name := range []string{"wrong run", "wrong repository", "wrong worktree", "duplicate record", "missing branch", "empty branch", "relative project", "unknown task", "wrong task project", "wrong task worktree", "unreadable task", "unreadable database", "unresolved home"} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			holder, earlier := "cg-example", []string(nil)
			if name == "unreadable task" {
				holder, earlier = "cg-example-child", []string{"cg-example"}
			}
			project, gateRoot, worktree := gateTaskFixture(t, holder, earlier...)
			writeGateSource(t, gateRoot, project, worktree, "feature")
			stateDir := filepath.Join(os.Getenv("GOBLINS_HOME"), "state")
			meta := state.TaskMeta{ID: "cg-example", Project: project, Worktree: filepath.Join(project, ".worktrees", "gb-cg-example")}
			runtime := standIn()
			var sql string
			switch name {
			case "wrong run":
				sql = `UPDATE runs SET id='another-run'`
			case "wrong repository":
				sql = `UPDATE repos SET id='other'; UPDATE runs SET repo_id='other'`
			case "wrong worktree":
				sql = `UPDATE runs SET worktree_dir='C:/different/worktree'`
			case "duplicate record":
				sql = `INSERT INTO runs SELECT * FROM runs`
			case "missing branch":
				sql = `UPDATE runs SET branch='missing'`
			case "empty branch":
				sql = `UPDATE runs SET branch=''`
			case "relative project":
				sql = `UPDATE repos SET working_path='relative-project'`
			case "wrong task project":
				meta.Project = t.TempDir()
			case "wrong task worktree":
				meta.Worktree = t.TempDir()
			case "unresolved home":
				runtime.resolveHome = func() (home.Home, error) { return home.Home{}, errors.New("home unavailable") }
			}
			if sql != "" {
				if output, err := exec.Command("sqlite3", filepath.Join(gateRoot, "state.sqlite"), sql).CombinedOutput(); err != nil {
					t.Fatalf("alter source: %s (%v)", output, err)
				}
			}
			if name == "unreadable database" {
				if err := os.WriteFile(filepath.Join(gateRoot, "state.sqlite"), []byte("not a database"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if name != "unknown task" {
				if err := state.WriteTaskMeta(stateDir, meta); err != nil {
					t.Fatal(err)
				}
			}
			if name == "unreadable task" {
				if err := state.WriteTaskMeta(stateDir, state.TaskMeta{ID: holder, Project: project, Worktree: filepath.Join(project, ".worktrees", "gb-"+holder)}); err != nil {
					t.Fatal(err)
				}
				held, err := holdExclusively(state.TaskMetaPath(stateDir, holder), time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				defer syscall.CloseHandle(held)
			}

			// Act
			task, err := gateTask(worktree, runtime)

			// Assert
			if task != "" || err == nil {
				t.Fatalf("task=%q, err=%v; want unknown ownership with its cause", task, err)
			}
		})
	}
}
