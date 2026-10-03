package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func gateTaskFixture(t *testing.T, holder string) (project, gateRoot, worktree string) {
	t.Helper()
	project = testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	git := func(dir string, args ...string) {
		t.Helper()
		if output, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s (%v)", args, output, err)
		}
	}
	git(project, "switch", "main")
	git(project, "worktree", "add", filepath.Join(project, ".worktrees", "gb-"+holder), "feature")
	gateRoot = t.TempDir()
	worktree = filepath.Join(gateRoot, "worktrees", "repo", "run")
	if output, err := exec.Command("git", "clone", "--no-hardlinks", project, worktree).CombinedOutput(); err != nil {
		t.Fatalf("clone gate fixture: %s (%v)", output, err)
	}
	git(worktree, "checkout", "--detach", "origin/feature")
	t.Chdir(worktree)
	t.Setenv("CFO_TASK_ID", "cg-misleading")
	t.Setenv("NO_MISTAKES_GATE", "")
	t.Setenv("NM_HOME", t.TempDir())
	t.Setenv("CFO_HOME", t.TempDir())
	t.Setenv("CFO_STATE_OVERRIDE", "")
	if err := os.MkdirAll(filepath.Join(os.Getenv("CFO_HOME"), "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	return project, gateRoot, worktree
}

func TestAnUnknownGateRunNeverNamesTheDaemonsTask(t *testing.T) {
	// Arrange
	gateTaskFixture(t, "cg-example-child")
	runtime := standIn()
	var turns bytes.Buffer
	runtime.gateRun = func(command []string, _ string, _ []string, stdout, stderr io.Writer) (int, error) {
		if command[1] == "test" {
			if exit := runGateTurns(&turns, stderr, plenty); exit != 0 {
				t.Errorf("turns exited %d", exit)
			}
		}
		return 0, nil
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := gateTestWith(runtime, &stdout, &stderr)

	// Assert
	report, _ := lastReport(t)
	if exit != 0 || report.Task != "" || strings.Contains(turns.String(), "cg-misleading") {
		t.Errorf("exit=%d, task=%q, turns=%q; want a successful check with no unverified task", exit, report.Task, turns.String())
	}
	log, err := os.ReadFile(report.Log)
	if err != nil || !strings.Contains(stderr.String(), "task attribution unavailable") || !strings.Contains(string(log), "task attribution unavailable") {
		t.Fatalf("stderr=%q, log=%q (%v); want the missing identity explained and recorded", stderr.String(), log, err)
	}
}

func writeGateSource(t *testing.T, root, project, worktree, branch string) {
	t.Helper()
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	sql := `CREATE TABLE repos(id TEXT,working_path TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,worktree_dir TEXT);
INSERT INTO repos VALUES('repo',` + quote(project) + `);
INSERT INTO runs VALUES('run','repo',` + quote(branch) + `,` + quote(worktree) + `);`
	if output, err := exec.Command("sqlite3", filepath.Join(root, "state.sqlite"), sql).CombinedOutput(); err != nil {
		t.Fatalf("write gate source: %s (%v)", output, err)
	}
}

func TestAGateRunNamesItsOwnerDespiteTheInheritedTask(t *testing.T) {
	for _, test := range []struct {
		name, holder, owner, marker string
	}{
		{"primary", "cg-example", "cg-example", ""},
		{"extra worktree", "cg-example-child", "cg-example", ""},
		{"validation agent", "cg-example-child", "cg-example", "1"},
		{"specific task", "cg-example-child", "cg-example-child", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			project, gateRoot, worktree := gateTaskFixture(t, test.holder)
			t.Setenv("NO_MISTAKES_GATE", test.marker)
			owner := filepath.Join(project, ".worktrees", "gb-cg-example")
			if err := os.MkdirAll(owner, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := state.WriteTaskMeta(filepath.Join(os.Getenv("CFO_HOME"), "state"), state.TaskMeta{ID: "cg-example", Project: project, Worktree: owner}); err != nil {
				t.Fatal(err)
			}
			if test.owner != "cg-example" {
				if err := state.WriteTaskMeta(filepath.Join(os.Getenv("CFO_HOME"), "state"), state.TaskMeta{ID: test.owner, Project: project, Worktree: filepath.Join(project, ".worktrees", "gb-"+test.owner)}); err != nil {
					t.Fatal(err)
				}
			}
			writeGateSource(t, gateRoot, project, worktree, "feature")
			runtime := standIn()
			var turns bytes.Buffer
			runtime.gateRun = func(command []string, _ string, _ []string, stdout, stderr io.Writer) (int, error) {
				if command[1] == "test" {
					if exit := runGateTurns(&turns, stderr, plenty); exit != 0 {
						t.Errorf("turns exited %d", exit)
					}
				}
				return 0, nil
			}

			// Act
			var stdout, stderr bytes.Buffer
			exit := gateTestWith(runtime, &stdout, &stderr)

			// Assert
			report, _ := lastReport(t)
			if exit != 0 || report.Task != test.owner || !strings.Contains(turns.String(), ", task "+test.owner+" (pid ") || strings.Contains(turns.String(), "cg-misleading") || stderr.Len() != 0 {
				t.Errorf("exit=%d, task=%q, turns=%q, stderr=%q; want %s from the gate's source worktree", exit, report.Task, turns.String(), stderr.String(), test.owner)
			}
		})
	}
}

func TestAGateTaskRejectsUnboundRunAndOwnerEvidence(t *testing.T) {
	for _, name := range []string{"wrong run", "wrong repository", "wrong worktree", "duplicate record", "missing branch", "empty branch", "relative project", "unknown task", "wrong task project", "wrong task worktree", "unreadable task", "unreadable database", "unresolved home"} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			project, gateRoot, worktree := gateTaskFixture(t, "cg-example")
			writeGateSource(t, gateRoot, project, worktree, "feature")
			stateDir := filepath.Join(os.Getenv("CFO_HOME"), "state")
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
			if name == "unreadable task" {
				if err := os.Mkdir(filepath.Join(stateDir, "cg-example.meta"), 0o755); err != nil {
					t.Fatal(err)
				}
			} else if name != "unknown task" {
				if err := state.WriteTaskMeta(stateDir, meta); err != nil {
					t.Fatal(err)
				}
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
