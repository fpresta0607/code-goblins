package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func gateTaskFixture(t *testing.T, holder string, earlier ...string) (project, gateRoot, worktree string) {
	t.Helper()
	return gateTaskFixtureThrough(t, "", holder, earlier...)
}

// gateTaskFixtureThrough is gateTaskFixture with the gate's worktrees\repo a
// directory junction to repoTarget, when repoTarget is not empty.
func gateTaskFixtureThrough(t *testing.T, repoTarget, holder string, earlier ...string) (project, gateRoot, worktree string) {
	t.Helper()
	project = testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	git := func(dir string, args ...string) {
		t.Helper()
		if output, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s (%v)", args, output, err)
		}
	}
	git(project, "switch", "main")
	for _, id := range earlier {
		if err := os.MkdirAll(filepath.Join(project, ".worktrees", "gb-"+id), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	git(project, "worktree", "add", filepath.Join(project, ".worktrees", "gb-"+holder), "feature")
	gateRoot = t.TempDir()
	worktree = filepath.Join(gateRoot, "worktrees", "repo", "run")
	if repoTarget != "" {
		if err := os.MkdirAll(repoTarget, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(gateRoot, "worktrees"), 0o755); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command("cmd", "/d", "/c", "mklink", "/J", filepath.Dir(worktree), repoTarget).CombinedOutput(); err != nil {
			t.Fatalf("mklink /J: %v %s", err, output)
		}
	}
	git(project, "remote", "add", "origin", "https://example.invalid/project.git")
	bare := filepath.Join(gateRoot, "repos", "repo.git")
	if output, err := exec.Command("git", "clone", "--bare", "--no-hardlinks", project, bare).CombinedOutput(); err != nil {
		t.Fatalf("clone gate fixture: %s (%v)", output, err)
	}
	git(project, "--git-dir="+bare, "remote", "set-url", "origin", "https://example.invalid/project.git")
	git(project, "--git-dir="+bare, "update-ref", "refs/remotes/origin/main", "main")
	git(project, "--git-dir="+bare, "worktree", "add", "--detach", worktree, "feature")
	t.Chdir(worktree)
	t.Setenv("GOBLINS_TASK_ID", "cg-misleading")
	t.Setenv("NO_MISTAKES_GATE", "")
	t.Setenv("NM_HOME", t.TempDir())
	t.Setenv("GOBLINS_HOME", t.TempDir())
	t.Setenv("GOBLINS_STATE_OVERRIDE", "")
	if err := os.MkdirAll(filepath.Join(os.Getenv("GOBLINS_HOME"), "state"), 0o755); err != nil {
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

func writeGateSource(t *testing.T, root, project, worktree, branch string) string {
	t.Helper()
	base, err := exec.Command("git", "-C", worktree, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	sql := `CREATE TABLE repos(id TEXT,working_path TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,worktree_dir TEXT,base_sha TEXT,head_sha TEXT);
INSERT INTO repos VALUES('repo',` + quote(project) + `);
INSERT INTO runs VALUES('run','repo',` + quote(branch) + `,` + quote(worktree) + `,` + quote(strings.TrimSpace(string(base))) + `,` + quote(strings.TrimSpace(string(base))) + `);`
	if output, err := exec.Command("sqlite3", filepath.Join(root, "state.sqlite"), sql).CombinedOutput(); err != nil {
		t.Fatalf("write gate source: %s (%v)", output, err)
	}
	return strings.TrimSpace(string(base))
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
			project, gateRoot, worktree := gateTaskFixture(t, test.holder, "cg-example")
			t.Setenv("NO_MISTAKES_GATE", test.marker)
			owner := filepath.Join(project, ".worktrees", "gb-cg-example")
			if err := state.WriteTaskMeta(filepath.Join(os.Getenv("GOBLINS_HOME"), "state"), state.TaskMeta{ID: "cg-example", Project: project, Worktree: owner}); err != nil {
				t.Fatal(err)
			}
			if test.owner != "cg-example" {
				if err := state.WriteTaskMeta(filepath.Join(os.Getenv("GOBLINS_HOME"), "state"), state.TaskMeta{ID: test.owner, Project: project, Worktree: filepath.Join(project, ".worktrees", "gb-"+test.owner)}); err != nil {
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

// A gate home whose worktrees\<repo> is a junction to a folder elsewhere, as
// one moved to a Dev Drive is, reads its run from its own state database,
// not from one beside the junction's target.
func TestAGateRunBehindAJunctionNamesItsOwner(t *testing.T) {
	// Arrange
	elsewhere := t.TempDir()
	project, gateRoot, worktree := gateTaskFixtureThrough(t, filepath.Join(elsewhere, "worktrees", "repo"), "cg-example")
	if err := state.WriteTaskMeta(filepath.Join(os.Getenv("GOBLINS_HOME"), "state"), state.TaskMeta{ID: "cg-example", Project: project, Worktree: filepath.Join(project, ".worktrees", "gb-cg-example")}); err != nil {
		t.Fatal(err)
	}
	writeGateSource(t, gateRoot, project, worktree, "feature")

	// Act
	task, err := gateTask(worktree, standIn())

	// Assert
	if task != "cg-example" || err != nil {
		t.Fatalf("task=%q, err=%v; want cg-example from the gate home's own state database", task, err)
	}
}

// A home whose heavy folders moved to a Dev Drive puts new worktrees there and
// keeps those of tasks made before the move in its own folder: a gate run of
// either names its owner.
func TestAGateRunNamesItsOwnerInEitherWorktreeFolderOfAMovedHome(t *testing.T) {
	for name, onTheDrive := range map[string]bool{"a worktree on the Dev Drive": true, "a worktree made before the move": false} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			project, gateRoot, worktree := gateTaskFixture(t, "cg-example")
			// git worktree move records the long spelling of a path, and a
			// CI runner's temporary folder has a short one (RUNNER~1), so
			// every folder here is spelled long, as spawn's always are.
			ceoHome, err := fsx.Canonical(os.Getenv("GOBLINS_HOME"))
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("GOBLINS_HOME", ceoHome)
			devDrive, err := fsx.Canonical(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			devDrive = filepath.Join(devDrive, "CodeGoblins")
			writeDevDriveConfig(t, ceoHome, devDrive)
			parent := ceoHome
			if onTheDrive {
				parent = devDrive
			}
			owner := filepath.Join(parent, "worktrees", filepath.Base(project), "cg-example")
			if err := os.MkdirAll(filepath.Dir(owner), 0o755); err != nil {
				t.Fatal(err)
			}
			if output, err := exec.Command("git", "-C", project, "worktree", "move", filepath.Join(project, ".worktrees", "gb-cg-example"), owner).CombinedOutput(); err != nil {
				t.Fatalf("git worktree move: %s (%v)", output, err)
			}
			if err := state.WriteTaskMeta(filepath.Join(ceoHome, "state"), state.TaskMeta{ID: "cg-example", Project: project, Worktree: owner}); err != nil {
				t.Fatal(err)
			}
			writeGateSource(t, gateRoot, project, worktree, "feature")
			runtime := standIn()
			runtime.gateRun = func(command []string, _ string, _ []string, stdout, stderr io.Writer) (int, error) {
				if command[1] == "test" {
					if exit := runGateTurns(io.Discard, stderr, plenty); exit != 0 {
						t.Errorf("turns exited %d", exit)
					}
				}
				return 0, nil
			}

			// Act
			var stdout, stderr bytes.Buffer
			exit := gateTestWith(runtime, &stdout, &stderr)

			// Assert
			if report, _ := lastReport(t); exit != 0 || report.Task != "cg-example" {
				t.Errorf("exit=%d, task=%q, stderr=%q; want the gate run named for cg-example", exit, report.Task, stderr.String())
			}
		})
	}
}

// writeDevDriveConfig moves root's heavy folders to devDrive, as goblins
// dev-drive move does.
func writeDevDriveConfig(t *testing.T, root, devDrive string) {
	t.Helper()
	data, err := json.Marshal(home.DevDriveConfig{Root: devDrive})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(home.DevDriveConfigPath(root), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAnOlderWorktreeIsNotNamedForANewerTaskSharingItsPrefix(t *testing.T) {
	// Arrange
	project, gateRoot, worktree := gateTaskFixture(t, "cg-example-child")
	newer := filepath.Join(project, ".worktrees", "gb-cg-example")
	if err := os.MkdirAll(newer, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTaskMeta(filepath.Join(os.Getenv("GOBLINS_HOME"), "state"), state.TaskMeta{ID: "cg-example", Project: project, Worktree: newer}); err != nil {
		t.Fatal(err)
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
	if exit != 0 || report.Task != "" || strings.Contains(turns.String(), ", task ") {
		t.Errorf("exit=%d, task=%q, turns=%q; want a successful check that names no task", exit, report.Task, turns.String())
	}
	log, err := os.ReadFile(report.Log)
	if err != nil || !strings.Contains(stderr.String(), "task attribution unavailable") || !strings.Contains(string(log), "task attribution unavailable") {
		t.Fatalf("stderr=%q, log=%q (%v); want the unowned worktree explained and recorded", stderr.String(), log, err)
	}
}
