package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// killedTask is a native task in a home of the test's own whose terminal has
// ended: a project cloned from a bare origin and the task's worktree of it,
// beside a gate root of its own that holds no runs.
func killedTask(t *testing.T) (home.Home, state.TaskMeta) {
	t.Helper()
	t.Setenv("NM_HOME", t.TempDir())
	root := t.TempDir()
	git := func(dir string, args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = dir
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	origin := filepath.Join(root, "origin.git")
	git(root, "init", "-q", "--bare", "--initial-branch=main", origin)
	project := filepath.Join(root, "app")
	git(root, "clone", "-q", origin, project)
	git(project, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "seed")
	git(project, "push", "-q", "origin", "HEAD:main")
	git(project, "remote", "set-head", "origin", "--auto")
	h := home.Home{Root: filepath.Join(root, "home"), State: filepath.Join(root, "home", "state"), Data: filepath.Join(root, "home", "data")}
	worktree := filepath.Join(h.Worktrees(), "app", "g1")
	for _, dir := range []string{h.State, h.Data, filepath.Dir(worktree)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	git(project, "worktree", "add", "-q", "--detach", worktree, "origin/main")
	meta := state.TaskMeta{ID: "g1", Window: "native", Worktree: worktree, Project: project, Harness: "claude", Backend: "native", SpawnGen: "s1", TaskTmp: filepath.Join(h.State, "tasktmp", "g1")}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	return h, meta
}

// keptMessage is a board message as the supervisor keeps it in a paused
// goblin's resume note.
const keptMessage = "Message from the Overlord, typed on the board: use the staging database"

// pausedWithKeptMessage records meta's goblin paused, with keptMessage kept
// for its resume.
func pausedWithKeptMessage(t *testing.T, h home.Home, meta state.TaskMeta) {
	t.Helper()
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, RequestGeneration: meta.SpawnGen, Operation: "pause-1", Action: "pause", Phase: "paused", ResumeNote: keptMessage, NoticeSent: true}); err != nil {
		t.Fatal(err)
	}
}

// toldCFO is what task's notify wakes tell the CFO.
func toldCFO(t *testing.T, h home.Home, task string) []string {
	t.Helper()
	pending, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	var told []string
	for _, record := range pending {
		if record.Kind == "notify" && record.Key == task {
			told = append(told, record.Detail)
		}
	}
	return told
}

// A cfo cleanup that retires a paused goblin before its resume runs hands each
// board message kept for that resume to the CFO with its words, and leaves
// none in its record. Before, the messages stayed in the paused record, which
// nothing reads once the task is retired (issue #1317).
func TestACleanupOfAPausedGoblinHandsItsKeptMessagesToTheCFO(t *testing.T) {
	// Arrange
	h, meta := killedTask(t)
	pausedWithKeptMessage(t, h, meta)

	// Act
	output, err := defaultCleanup(t.Context(), h, meta.ID, false)

	// Assert
	if err != nil {
		t.Fatalf("the cleanup = %q, %v, want the task retired", output, err)
	}
	want := "message not delivered: g1 was retired by a cleanup while it was paused, so the resume the message was kept for never runs, so it is yours to relay or answer. The Overlord wrote: use the staging database"
	if told := toldCFO(t, h, meta.ID); len(told) != 1 || told[0] != want {
		t.Fatalf("the CFO was told %q, want %q once", told, want)
	}
	if record, err := state.ReadLifecycle(h.State, meta.ID); err != nil || record.ResumeNote != "" {
		t.Fatalf("the paused record after the cleanup = %+v, %v; want no message left in its note", record, err)
	}
}

// cfo kill of a paused goblin stops it before the resume its record keeps a
// board message for, so the stop hands the message to the CFO with its words
// before it writes its own record, and the cleanup inside the stop finds
// nothing more to hand on: the CFO is told once (issue #1317).
func TestAKillOfAPausedGoblinHandsItsKeptMessagesToTheCFOOnce(t *testing.T) {
	// Arrange
	h, meta := killedTask(t)
	pausedWithKeptMessage(t, h, meta)
	service := lifecycle.Service{StateDir: h.State, Operations: lifecycle.Operations{
		Stop: func(context.Context, state.TaskMeta, *state.Lifecycle) ([]string, error) { return nil, nil },
		Archive: func(ctx context.Context, meta state.TaskMeta, _ *state.Lifecycle) (lifecycle.Preservation, error) {
			_, err := defaultCleanup(ctx, h, meta.ID, false)
			return lifecycle.Preservation{}, err
		},
		Notify: func(state.Lifecycle) error { return nil },
		HandOver: func(meta state.TaskMeta, prior state.Lifecycle) (bool, error) {
			return supervisor.HandOverReplacedNote(h.State, meta, prior)
		},
		HandOverEnded: func(meta state.TaskMeta, prior state.Lifecycle, action string) (bool, error) {
			return supervisor.HandOverEndedNote(h.State, meta, prior, action)
		},
	}}

	// Act
	record, err := service.Run(t.Context(), lifecycle.Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "kill-1", Action: "stop", Reason: "Requested by the CFO", IsWatched: true})

	// Assert
	if err != nil || record.Phase != "stopped" {
		t.Fatalf("the kill = phase %q, %v, want the task stopped\nproblems: %s", record.Phase, err, strings.Join(record.Problems, "; "))
	}
	want := "message not delivered: g1 was stopped while it was paused, so the resume the message was kept for never runs, so it is yours to relay or answer. The Overlord wrote: use the staging database"
	if told := toldCFO(t, h, meta.ID); len(told) != 1 || told[0] != want {
		t.Fatalf("the CFO was told %q, want %q once", told, want)
	}
}

// cfo kill stops its goblin and cleans it up in one process: the stop holds
// the task's lifecycle and record locks while its archive step runs the
// cleanup, which holds those same locks against every other command. The
// cleanup takes them as its caller's, and the kill archives the task.
func TestKillStillArchivesThroughItsOwnCleanup(t *testing.T) {
	// Arrange: the stop's own steps, with the production cleanup as its
	// archive step and nothing left running to stop.
	h, meta := killedTask(t)
	var heldDuringArchive []string
	service := lifecycle.Service{StateDir: h.State, Operations: lifecycle.Operations{
		Stop: func(context.Context, state.TaskMeta, *state.Lifecycle) ([]string, error) { return nil, nil },
		Archive: func(ctx context.Context, meta state.TaskMeta, _ *state.Lifecycle) (lifecycle.Preservation, error) {
			for _, name := range []string{state.LifecycleLockName(meta.ID), state.MetadataLockName(meta.ID)} {
				if lock.HeldByNamed(h.State, name, os.Getpid()) {
					heldDuringArchive = append(heldDuringArchive, name)
				}
			}
			_, err := defaultCleanup(ctx, h, meta.ID, false)
			return lifecycle.Preservation{}, err
		},
		Notify: func(state.Lifecycle) error { return nil },
	}}

	// Act
	record, err := service.Run(context.Background(), lifecycle.Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "kill-1", Action: "stop", Reason: "Requested by the CFO", IsWatched: true})

	// Assert
	if err != nil || record.Phase != "stopped" {
		t.Fatalf("the kill = phase %q, %v, want the task stopped\nproblems: %s", record.Phase, err, strings.Join(record.Problems, "; "))
	}
	if len(heldDuringArchive) != 2 {
		t.Fatalf("the stop held %v while it archived, want the task's lifecycle and record locks, which is what the cleanup must run under", heldDuringArchive)
	}
	if _, err := state.ReadTaskMeta(h.State, meta.ID); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the task record after the kill: %v, want it retired", err)
	}
	if _, err := os.Stat(meta.Worktree); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the worktree after the kill: %v, want it returned", err)
	}
	for _, name := range []string{state.LifecycleLockName(meta.ID), state.MetadataLockName(meta.ID), state.CleanupLockName(meta.ID)} {
		if _, err := lock.ReadNamed(h.State, name); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s is still held after the kill: %v", name, err)
		}
	}
}

// A pause leaves its goblin's gate run running, so cfo cleanup, and the
// sweep's cleanup of a task it found, read the task's gate run before they
// change anything. A run that completed or was cancelled, as a landed or
// killed task's is, lets the task retire, while one still running, or a gate
// whose run cannot be verified, refuses with the task as it was (issue
// #2089). A paused task's run is the one its pause saved, read by its id
// whatever state the worktree is in, --force-archive and an empty folder's
// cleanup included. With no saved run, the run is read by the worktree's
// branch only while the folder is still a Git worktree, so a worktree gone,
// or a folder no longer a Git worktree, retires its task unread. The branch
// is read beside a saved run too, so a goblin that ran again after its saved
// run ended, as after a resume or a failed pause, is refused while its newer
// run is open. A refusal where cfo kill cannot find the run, the worktree
// gone or no longer a Git worktree, names the abort that ends it (#2246).
func TestCleanupReadsTheTaskBranchsGateRunBeforeRetiringIt(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Fatal("sqlite3 is needed for the gate fixture")
	}
	callers := []struct {
		name  string
		clean func(ctx context.Context, h home.Home, id string, forceArchive bool) (string, error)
	}{
		{name: "cfo cleanup", clean: defaultCleanup},
		{name: "cfo reap", clean: func(ctx context.Context, h home.Home, id string, forceArchive bool) (string, error) {
			return reapCleanup(ctx, h, &herdr.Client{Commands: execx.OSRunner{}, Session: herdrSession()}, id, forceArchive)
		}},
	}
	for _, caller := range callers {
		for _, tc := range []struct {
			name, status, head, phase                                                         string
			want                                                                              []string
			isForceArchive, isWorktreeGone, isNotAWorktree, isOlderRunSaved, isGateUnreadable bool
		}{
			{name: "a completed run", status: "completed"},
			{name: "a cancelled run", status: "cancelled"},
			{name: "a run still open", status: "running", want: []string{"cfo kill g1"}},
			{name: "a gate whose run cannot be verified", status: "running", head: "not-a-commit", want: []string{"cannot be read"}},
			{name: "a force archive of a completed run", status: "completed", isForceArchive: true},
			{name: "a force archive of a run still open", status: "running", isForceArchive: true, want: []string{"cfo kill g1"}},
			{name: "a force archive of a gate whose run cannot be verified", status: "running", head: "not-a-commit", isForceArchive: true, want: []string{"cannot be read"}},
			{name: "a force archive of a task whose worktree is gone", status: "running", isForceArchive: true, isWorktreeGone: true},
			{name: "a force archive of a folder no longer a Git worktree", status: "running", isForceArchive: true, isNotAWorktree: true},
			{name: "an empty folder no longer a Git worktree", status: "running", isNotAWorktree: true},
			{name: "a paused task whose saved run completed", status: "completed", phase: "paused"},
			{name: "a paused task whose saved run is still open", status: "running", phase: "paused", want: []string{"cfo kill g1"}},
			{name: "a force archive of a paused task whose worktree is gone and whose saved run is open", status: "running", isForceArchive: true, isWorktreeGone: true, phase: "paused", want: []string{"cfo kill g1", "no-mistakes axi abort --run run-1"}},
			{name: "a force archive of a paused task whose worktree is gone and whose saved run completed", status: "completed", isForceArchive: true, isWorktreeGone: true, phase: "paused"},
			{name: "a force archive of a paused task whose folder is no longer a Git worktree", status: "running", isForceArchive: true, isNotAWorktree: true, phase: "paused", want: []string{"cfo kill g1", "no-mistakes axi abort --run run-1"}},
			{name: "a paused task whose empty folder is no longer a Git worktree", status: "running", isNotAWorktree: true, phase: "paused", want: []string{"cfo kill g1", "no-mistakes axi abort --run run-1"}},
			{name: "a force archive of a paused task whose worktree is gone and whose gate cannot be read", isForceArchive: true, isWorktreeGone: true, phase: "paused", isGateUnreadable: true, want: []string{"cannot be read"}},
			{name: "a resumed task whose saved run completed and whose branch's newer run is open", status: "running", phase: "running", isOlderRunSaved: true, want: []string{"gate run run-1", "cfo kill g1"}},
			{name: "a failed pause whose saved run completed and whose branch's newer run is open", status: "running", phase: "failed", isOlderRunSaved: true, want: []string{"gate run run-1", "cfo kill g1"}},
		} {
			t.Run(caller.name+" of "+tc.name, func(t *testing.T) {
				// Arrange: the task's worktree on its own branch, whose
				// latest run the scratch gate's database holds, and for a
				// paused task the run its pause saved.
				h, meta := killedTask(t)
				git := func(args ...string) string {
					t.Helper()
					command := exec.Command("git", args...)
					command.Dir = meta.Worktree
					output, err := command.Output()
					if err != nil {
						t.Fatalf("git %v: %v", args, err)
					}
					return strings.TrimSpace(string(output))
				}
				git("switch", "-q", "-c", "fix/g1")
				head := tc.head
				if head == "" {
					head = git("rev-parse", "HEAD")
				}
				gateRoot := t.TempDir()
				t.Setenv("NM_HOME", gateRoot)
				gateDatabase := filepath.Join(gateRoot, "state.sqlite")
				if tc.isGateUnreadable {
					if err := os.WriteFile(gateDatabase, []byte("not a database"), 0o644); err != nil {
						t.Fatal(err)
					}
				} else {
					schema := fmt.Sprintf(`CREATE TABLE repos(id TEXT,working_path TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,status TEXT,head_sha TEXT,last_pushed_sha TEXT,intent TEXT,worktree_dir TEXT,created_at INTEGER,submitted_head_sha TEXT,custody_returned_at INTEGER,review_approved_head_sha TEXT,pr_url TEXT,pr_state TEXT,ci_ready_at INTEGER,terminal_head_verified_at INTEGER);
INSERT INTO repos VALUES('repo-1','%s');
INSERT INTO runs(id,repo_id,branch,status,head_sha,last_pushed_sha,intent,worktree_dir,created_at) VALUES('run-1','repo-1','fix/g1','%s','%s','','','',1);`, strings.ReplaceAll(filepath.ToSlash(meta.Project), "'", "''"), tc.status, head)
					if tc.isOlderRunSaved {
						schema += fmt.Sprintf(`INSERT INTO runs(id,repo_id,branch,status,head_sha,last_pushed_sha,intent,worktree_dir,created_at) VALUES('run-0','repo-1','fix/g1','completed','%s','','','',0);`, head)
					}
					if output, err := exec.Command("sqlite3", gateDatabase, schema).CombinedOutput(); err != nil {
						t.Fatalf("create gate fixture: %v %s", err, output)
					}
				}
				if tc.phase != "" {
					action, saved := "pause", "run-1"
					if tc.phase == "running" {
						action = "resume"
					}
					if tc.isOlderRunSaved {
						saved = "run-0"
					}
					if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, RequestGeneration: meta.SpawnGen, Operation: action + "-1", Action: action, Phase: tc.phase, GateRun: saved, NoticeSent: true}); err != nil {
						t.Fatal(err)
					}
				}
				switch {
				case tc.isWorktreeGone:
					if err := os.RemoveAll(meta.Worktree); err != nil {
						t.Fatal(err)
					}
				case tc.isNotAWorktree:
					// The seed commit is empty, so the worktree holds only
					// its .git link, and without it the folder is empty.
					if err := os.Remove(filepath.Join(meta.Worktree, ".git")); err != nil {
						t.Fatal(err)
					}
				}

				// Act
				output, err := caller.clean(t.Context(), h, meta.ID, tc.isForceArchive)

				// Assert
				_, metaErr := state.ReadTaskMeta(h.State, meta.ID)
				if tc.want == nil {
					if err != nil || !errors.Is(metaErr, os.ErrNotExist) {
						t.Fatalf("cleanup = %q, %v, task record %v; want the task retired", output, err, metaErr)
					}
					return
				}
				for _, want := range tc.want {
					if err == nil || !strings.Contains(err.Error(), want) || metaErr != nil {
						t.Fatalf("cleanup = %q, %v, task record %v; want it refused saying %q with the task kept", output, err, metaErr, want)
					}
				}
				if _, err := os.Stat(meta.Worktree); !tc.isWorktreeGone && err != nil {
					t.Fatalf("worktree after a refused cleanup: %v, want it kept", err)
				}
			})
		}
	}
}
