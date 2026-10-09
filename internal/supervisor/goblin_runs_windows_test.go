package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// The Overlord, 2026-10-02, on a goblin's waiting card whose command sat in a
// paragraph: "run in powershell button". A goblin's wait or question can carry
// a command he runs with one click, shown as the run card the CFO's own
// requests use, named for the goblin, in a terminal on its card.

// waitWithACommand makes the fixture's goblin wait on the Overlord with a
// command for him to run, as cfo notify --run does, and returns the goblin,
// its terminal and connection, and the run item on the board.
func waitWithACommand(t *testing.T, store *Store, command string) (state.TaskMeta, hostedTerminal, *CFOConnection, Run) {
	t.Helper()
	meta, _, goblin, connection := goblinFixture(t, store)
	if err := state.AppendStatus(store.Home.State, meta.ID, "waiting on overlord: Sign in to GitHub so I can push"); err != nil {
		t.Fatal(err)
	}
	if err := PublishGoblinRun(store.Home, meta.ID, 7, "Sign in to GitHub so I can push", "powershell", command); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestGoblinRuns(); err != nil {
		t.Fatal(err)
	}
	runs := store.Snapshot().Runs
	if len(runs) != 1 {
		t.Fatalf("runs = %+v, issues %q, want the goblin's one command", runs, store.Snapshot().Issues)
	}
	return meta, goblin, connection, runs[0]
}

func TestAGoblinsCommandBecomesARunCardNamedForIt(t *testing.T) {
	// Arrange
	store, _ := testStore(t)

	// Act
	meta, _, _, r := waitWithACommand(t, store, "gh auth login\n")
	name, _ := runScript(r)
	script, readErr := os.ReadFile(filepath.Join(runDir(store.Home.State, r), name))

	// Assert
	if r.ID != "run-task-1-7" || r.Task != meta.ID || r.Identity != goblinIdentity(meta) || r.Title != "Sign in to GitHub so I can push" {
		t.Errorf("run = %s for %q as %q titled %q, want the goblin's own item", r.ID, r.Task, r.Identity, r.Title)
	}
	if r.State != "ready" || r.Admin || r.Shell != "powershell" || r.Cwd != meta.Worktree {
		t.Errorf("run = %s admin=%t in %s at %s, want a ready command in the goblin's worktree", r.State, r.Admin, r.Shell, r.Cwd)
	}
	if readErr != nil || !strings.HasSuffix(string(script), "gh auth login\n") || runDigest(script) != r.ScriptSum {
		t.Errorf("script = %q (%v), want the goblin's command as the file Run executes", script, readErr)
	}
}

// A file in the inbox proves nothing about who wrote it, so it can only ever
// be a live goblin's own command: never the CFO's, never elevated, never under
// an ID the goblin does not own.
func TestARunFromTheInboxIsRefusedUnlessItIsALiveGoblinsOwn(t *testing.T) {
	for _, c := range []struct {
		name   string
		change func(r *Run)
	}{
		{"it names no task, so it speaks for the CFO", func(r *Run) { r.Task = "" }},
		{"its task has no live record", func(r *Run) { r.Task, r.ID = "task-gone", "run-task-gone-7" }},
		{"it carries another generation's identity", func(r *Run) { r.Identity = strings.Repeat("b", 64) }},
		{"it asks to run elevated", func(r *Run) { r.Admin = true }},
		{"it takes an ID that is not the goblin's", func(r *Run) { r.ID = "install-main-66714dea" }},
		{"its shell is not one the board runs", func(r *Run) { r.Shell = "cmd" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta, _, _, _ := goblinFixture(t, store)
			now := time.Now().UTC()
			r := Run{ID: "run-task-1-7", Identity: goblinIdentity(meta), Task: meta.ID, Title: "Sign in", Shell: "powershell", Command: "gh auth login\n", Cwd: meta.Worktree, State: "ready", CreatedAt: now, ExpiresAt: now.Add(runLifetime)}
			c.change(&r)
			data, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(goblinRunInbox(h.State), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(goblinRunInbox(h.State), "forged.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			ingested := store.ingestGoblinRuns()
			after := store.Snapshot()

			// Assert
			if ingested != nil {
				t.Fatal(ingested)
			}
			if len(after.Runs) != 0 {
				t.Errorf("runs = %+v, want the record refused", after.Runs)
			}
			if len(after.Issues) != 1 || !strings.HasPrefix(after.Issues[0], "Run request rejected: ") {
				t.Errorf("issues = %q, want the refusal said once", after.Issues)
			}
			if _, err := os.Stat(filepath.Join(goblinRunInbox(h.State), "forged.json")); !os.IsNotExist(err) {
				t.Errorf("the refused record is still in the inbox (%v)", err)
			}
		})
	}
}

// A goblin's command stays in the Command Center while the goblin works on
// beside it: run-cg-codex-pi-proof-6513 was withdrawn on 2026-10-08 by its
// goblin's own working report, and asked again eight minutes later. It leaves
// with its wait once the goblin finishes.
func TestAGoblinsUnrunCommandStaysWhileItWorksAndIsWithdrawnOnceItFinishes(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta, _, _, r := waitWithACommand(t, store, "gh auth login\n")

	// Act
	standing := store.retireGoblinRuns()
	kept := store.Snapshot().Runs[0]
	// The report lands in a later second than the command, as a real one does.
	store.mu.Lock()
	store.db.Runs[0].CreatedAt = store.db.Runs[0].CreatedAt.Add(-5 * time.Second)
	saved := store.save()
	store.mu.Unlock()
	if err := state.AppendStatus(h.State, meta.ID, "working: running the proof in a scratch home meanwhile"); err != nil {
		t.Fatal(err)
	}
	working := store.retireGoblinRuns()
	still := store.Snapshot().Runs[0]
	if err := state.AppendStatus(h.State, meta.ID, "done: PR https://github.com/acme/api/pull/12"); err != nil {
		t.Fatal(err)
	}
	moved := store.retireGoblinRuns()
	gone := store.Snapshot().Runs[0]

	// Assert
	if standing != nil || saved != nil || working != nil || moved != nil {
		t.Fatal(standing, saved, working, moved)
	}
	if kept.State != "ready" || still.State != "ready" {
		t.Errorf("while the goblin waits: run %s, then %s while it works on, want it ready", kept.State, still.State)
	}
	if gone.State != "withdrawn" || !strings.Contains(gone.Reason, r.Task+" reported again") {
		t.Errorf("after the goblin reported again: run %s %q, want it withdrawn saying so", gone.State, gone.Reason)
	}
}

// One click runs it in the goblin's worktree, never elevated, and the goblin,
// not the CFO, hears how its command went.
func TestAGoblinsCommandRunsAndTheGoblinIsTold(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	_, goblin, connection, r := waitWithACommand(t, store, "gh auth login\n")
	launcher := &fakeRunLauncher{started: liveStart(t)}
	s := &Service{Store: store, Options: Options{CFO: connection, Runs: launcher}}

	// Act
	pressRun(t, s, r, "run-action-1")
	launches := launcher.all()
	if err := os.WriteFile(filepath.Join(runDir(store.Home.State, r), "exit.txt"), []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}
	finished := s.finishRuns(context.Background())
	after := store.Snapshot().Runs[0]

	// Assert
	if finished != nil {
		t.Fatal(finished)
	}
	if len(launches) != 1 || launches[0].Admin || launches[0].Shell != "powershell" || launches[0].Cwd != r.Cwd {
		t.Fatalf("launches = %+v, want one terminal in the goblin's worktree", launches)
	}
	if after.State != "succeeded" || after.ExitCode == nil || *after.ExitCode != 0 {
		t.Errorf("run = %s exit %v, want it finished with exit code 0", after.State, after.ExitCode)
	}
	if told := goblin.lines(t); len(told) != 1 || !strings.Contains(told[0], "The Overlord ran your command") || !strings.Contains(told[0], "exit code 0") {
		t.Errorf("the goblin got %q, want one message that he ran its command and how it ended", told)
	}
}
