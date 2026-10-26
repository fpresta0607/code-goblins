package pipeline

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// A review fixer's unfinished repairs live in the gate run's worktree, and
// native recovery removes that worktree with force. On 2026-10-23 an
// allowance-interrupted review repair lost its frontend changes exactly there:
// the run was failed, custody returned at the submitted head, and the worktree
// swept before anything kept the edits. Every tracked and untracked change is
// therefore committed and pinned under the run before native recovery runs.
func TestRecoverKeepLocalPreservesTheFixersUnfinishedEditsBeforeNativeRecovery(t *testing.T) {
	requireFixtureTools(t, "git", "sqlite3")
	// Arrange
	fixture := newGateFixture(t)
	// The fixer's repairs: a tracked edit, a tracked deletion, and an untracked
	// test beside them.
	writeFixtureFile(t, filepath.Join(fixture.gateWorktree, "page.tsx"), "export const page = 2; // repaired\n")
	if err := os.Remove(filepath.Join(fixture.gateWorktree, "app.go")); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(fixture.gateWorktree, "repair_test.go"), "package app\n\nfunc TestRepair(t *testing.T) {}\n")
	writeFixtureSQLite(t, filepath.Join(fixture.root, "state.sqlite"), `CREATE TABLE repos(id TEXT,working_path TEXT,default_branch TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,created_at INTEGER,status TEXT,head_sha TEXT,submitted_head_sha TEXT,last_pushed_sha TEXT,custody_returned_at INTEGER);
INSERT INTO repos VALUES('`+recoveryRepo+`','C:/project','main');
INSERT INTO runs VALUES('`+recoveryRun+`','`+recoveryRepo+`','`+recoveryBranch+`',1,'failed','`+fixture.submitted+`','`+fixture.submitted+`',NULL,NULL);`)
	runner := &gateRecoveryRunner{bare: fixture.bare, gateWorktree: fixture.gateWorktree, submitted: fixture.submitted}

	// Act
	result, err := (Reader{Root: fixture.root, Commands: runner}).RecoverKeepLocal(context.Background(), "C:/project", fixture.taskWorktree, recoveryBranch, nil)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "native custody recovery exited 1") {
		t.Fatalf("recover = %v, want the native refusal after the worktree was swept", err)
	}
	if runner.pinned == "" || !runner.pinnedBeforeRecovery {
		t.Fatal("the fixer's edits were not pinned before native recovery removed the worktree")
	}
	if result.WorktreeEdits != runner.pinned {
		t.Fatalf("worktree edits = %q, want the pinned commit %q", result.WorktreeEdits, runner.pinned)
	}
	if _, err := os.Stat(fixture.gateWorktree); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("gate worktree survived the fixture's sweep: %v", err)
	}
	if got := fixture.git(t, "rev-parse", WorktreeRef(recoveryRun)); got != runner.pinned {
		t.Fatalf("%s = %s, want %s", WorktreeRef(recoveryRun), got, runner.pinned)
	}
	if parent := fixture.git(t, "rev-parse", runner.pinned+"^"); parent != fixture.submitted {
		t.Fatalf("preserved commit's parent = %s, want the run head %s", parent, fixture.submitted)
	}
	if changes := fixture.git(t, "show", "--name-status", "--format=", runner.pinned); changes != "D\tapp.go\nM\tpage.tsx\nA\trepair_test.go" {
		t.Fatalf("preserved changes:\n%s\nwant the fixer's edit, deletion and untracked test", changes)
	}
	if repaired := fixture.git(t, "cat-file", "-p", runner.pinned+":page.tsx"); repaired != "export const page = 2; // repaired" {
		t.Fatalf("preserved page.tsx = %q", repaired)
	}
}

// A second capture stands on the first, since the first is what a goblin may
// already be restoring, and a clean worktree pins nothing at all. The goblin's
// own index is never written by either.
func TestPreserveWorktreeEditsKeepsTheFirstCaptureAndIgnoresACleanWorktree(t *testing.T) {
	requireFixtureTools(t, "git")
	// Arrange
	fixture := newGateFixture(t)
	reader := Reader{Root: fixture.root, Commands: execx.OSRunner{}}

	// Act
	clean, cleanErr := reader.preserveWorktreeEdits(context.Background(), recoveryRun, recoveryRepo, fixture.gateWorktree, fixture.submitted)
	writeFixtureFile(t, filepath.Join(fixture.gateWorktree, "app.go"), "package app // repaired\n")
	first, firstErr := reader.preserveWorktreeEdits(context.Background(), recoveryRun, recoveryRepo, fixture.gateWorktree, fixture.submitted)
	writeFixtureFile(t, filepath.Join(fixture.gateWorktree, "app.go"), "package app // written twice\n")
	again, againErr := reader.preserveWorktreeEdits(context.Background(), recoveryRun, recoveryRepo, fixture.gateWorktree, fixture.submitted)

	// Assert
	if cleanErr != nil || clean != "" {
		t.Fatalf("clean worktree preserved %q, %v", clean, cleanErr)
	}
	if firstErr != nil || first == "" {
		t.Fatalf("first capture = %q, %v", first, firstErr)
	}
	if againErr != nil || again != first {
		t.Fatalf("second capture = %q, %v, want the first %q", again, againErr, first)
	}
	if got := fixture.git(t, "rev-parse", WorktreeRef(recoveryRun)); got != first {
		t.Fatalf("%s = %s, want %s", WorktreeRef(recoveryRun), got, first)
	}
	if kept := fixture.git(t, "cat-file", "-p", first+":app.go"); kept != "package app // repaired" {
		t.Fatalf("preserved app.go = %q, want the first capture", kept)
	}
	if status := runPipelineGit(t, fixture.gateWorktree, "status", "--porcelain"); !strings.Contains(status, "app.go") {
		t.Fatalf("the goblin's own worktree no longer shows its edit:\n%s", status)
	}
}

// gateFixture is a gate repository whose run worktree still exists, with the
// task worktree a recovery acts in.
type gateFixture struct {
	root         string
	bare         string
	submitted    string
	gateWorktree string
	taskWorktree string
}

func newGateFixture(t *testing.T) gateFixture {
	t.Helper()
	fixture := gateFixture{root: t.TempDir(), taskWorktree: t.TempDir()}
	fixture.gateWorktree = filepath.Join(fixture.root, "worktrees", recoveryRepo, recoveryRun)
	fixture.bare = filepath.Join(fixture.root, "repos", recoveryRepo+".git")
	runPipelineGit(t, fixture.root, "init", "--quiet", "--bare", fixture.bare)
	seed := t.TempDir()
	runPipelineGit(t, seed, "init", "--quiet")
	runPipelineGit(t, seed, "checkout", "--quiet", "-b", recoveryBranch)
	writeFixtureFile(t, filepath.Join(seed, "app.go"), "package app\n")
	writeFixtureFile(t, filepath.Join(seed, "page.tsx"), "export const page = 1;\n")
	runPipelineGit(t, seed, "add", "--all")
	identity := []string{"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false"}
	runPipelineGit(t, seed, append(identity, "commit", "--quiet", "-m", "submitted work")...)
	fixture.submitted = runPipelineGit(t, seed, "rev-parse", "HEAD")
	runPipelineGit(t, seed, "push", "--quiet", fixture.bare, recoveryBranch+":refs/heads/"+recoveryBranch)
	// The run's worktree and the task's are both worktrees of the gate
	// repository, as the fleet's are at repos/<repo>.git.
	runPipelineGit(t, fixture.root, "--git-dir="+fixture.bare, "worktree", "add", "--quiet", "--detach", fixture.gateWorktree, fixture.submitted)
	runPipelineGit(t, fixture.root, "--git-dir="+fixture.bare, "worktree", "add", "--quiet", "--detach", fixture.taskWorktree, fixture.submitted)
	return fixture
}

func (fixture gateFixture) git(t *testing.T, args ...string) string {
	t.Helper()
	return fixtureGit(t, append([]string{"--git-dir=" + fixture.bare}, args...)...)
}

// gateRecoveryRunner runs git for real and answers no-mistakes itself, sweeping
// the run's worktree with force as native recovery does.
type gateRecoveryRunner struct {
	bare                 string
	gateWorktree         string
	submitted            string
	pinned               string
	pinnedBeforeRecovery bool
}

func (runner *gateRecoveryRunner) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name != "no-mistakes" {
		return execx.OSRunner{}.Run(ctx, request)
	}
	switch command := strings.Join(request.Args, " "); command {
	case "axi sync --check":
		sync := "branch_sync:\n  state: pipeline_owned\n  safety: blocked_pipeline_owned_unrecoverable\n  local:\n    head: " + runner.submitted + "\n    clean: true\n  pipeline:\n    run: \"" + recoveryRun + "\"\n    submitted_head: " + runner.submitted + "\n    current_head: " + runner.submitted + "\n"
		return execx.Result{ExitCode: 1, Stdout: []byte(sync)}, nil
	case "axi sync --recover --keep-local":
		probe, err := execx.OSRunner{}.Run(ctx, execx.Request{Name: "git", Args: []string{"--git-dir=" + runner.bare, "rev-parse", "--verify", "--quiet", WorktreeRef(recoveryRun)}})
		if err != nil {
			return execx.Result{}, err
		}
		runner.pinnedBeforeRecovery = probe.ExitCode == 0
		runner.pinned = strings.TrimSpace(string(probe.Stdout))
		if err := os.RemoveAll(runner.gateWorktree); err != nil {
			return execx.Result{}, err
		}
		return execx.Result{ExitCode: 1, Stderr: []byte("recovery refused")}, nil
	default:
		return execx.Result{}, errors.New("unexpected no-mistakes command: " + command)
	}
}

func fixtureGit(t *testing.T, args ...string) string {
	t.Helper()
	output, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %s: %v", strings.Join(args, " "), output, err)
	}
	return strings.TrimSpace(string(output))
}

func requireFixtureTools(t *testing.T, tools ...string) {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " CLI not available")
		}
	}
}

func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeFixtureSQLite(t *testing.T, path, schema string) {
	t.Helper()
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	if out, err := exec.Command(sqlite, path, schema).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", out, err)
	}
}
