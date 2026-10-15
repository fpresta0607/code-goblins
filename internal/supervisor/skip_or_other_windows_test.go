package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// The Overlord, 2026-10-15: "again theres no way for me to answer this
// command with a skip or other". Every Command Center card has Skip, which
// clears it and tells the goblin to go on without it, and Other, a free answer
// that reaches the goblin as his word.

// skipBoard is a goblin whose terminal records what it is told, with a
// command of its own waiting on the board.
func skipBoard(t *testing.T) (*Service, state.TaskMeta, Run, *[]string) {
	t.Helper()
	store, h := testStore(t)
	meta := state.TaskMeta{ID: "olive-task", Backend: "native", SpawnGen: "g1", Worktree: h.Root}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	previous := typeIntoGoblin
	t.Cleanup(func() { typeIntoGoblin = previous })
	told := &[]string{}
	typeIntoGoblin = func(_ context.Context, _ string, _ state.TaskMeta, text string) error {
		*told = append(*told, text)
		return nil
	}
	r := Run{ID: "run-olive-task-7", Identity: goblinIdentity(meta), By: "goblin", Task: meta.ID, Title: "Sign in to GitHub so I can push", Shell: "powershell", Command: "gh auth login\n", Cwd: h.Root, CreatedAt: time.Now().UTC()}
	name, script := runScript(r)
	dir := runDir(h.State, r)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), script, 0600); err != nil {
		t.Fatal(err)
	}
	r.ScriptSum = runDigest(script)
	if err := store.acceptRun(r); err != nil {
		t.Fatal(err)
	}
	return &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}}}, meta, r, told
}

func pressOnRun(t *testing.T, s *Service, a Action) error {
	t.Helper()
	if _, err := s.Store.Queue(a); err != nil {
		return err
	}
	return s.Store.ProcessOne(context.Background(), s.execute)
}

func TestSkipOnACommandClearsItAndTellsTheGoblinToGoOnWithoutIt(t *testing.T) {
	// Arrange
	s, _, r, told := skipBoard(t)

	// Act
	err := pressOnRun(t, s, Action{ID: "skip-run", Kind: "run_skip", RunID: r.ID, Generation: r.Identity})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	skipped := s.Store.Snapshot().Runs[0]
	if skipped.State != "withdrawn" || !strings.Contains(skipped.Reason, "skipped") {
		t.Fatalf("run = %s %q, want it withdrawn as skipped", skipped.State, skipped.Reason)
	}
	if len(*told) != 1 || !strings.Contains((*told)[0], r.ID) || !strings.Contains((*told)[0], "Go on without it") {
		t.Fatalf("the goblin was told %q, want one note to go on without it", *told)
	}
	if audit, err := os.ReadFile(filepath.Join(s.Store.Home.State, "runs.audit")); err != nil || !strings.Contains(string(audit), r.ID) {
		t.Fatalf("runs.audit = %q, %v; want the skip recorded", audit, err)
	}
	if err := pressOnRun(t, s, Action{ID: "run-after-skip", Kind: "run", RunID: r.ID, Generation: r.Identity}); err == nil {
		t.Fatal("a skipped command ran")
	}
}

func TestOtherOnACommandSendsHisWordToTheGoblinInsteadOfRunningIt(t *testing.T) {
	// Arrange
	s, _, r, told := skipBoard(t)
	word := "Use the deploy key in the vault instead of signing in"

	// Act
	err := pressOnRun(t, s, Action{ID: "other-run", Kind: "run_answer", RunID: r.ID, Generation: r.Identity, Text: word})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	answered := s.Store.Snapshot().Runs[0]
	if answered.State != "withdrawn" || !strings.Contains(answered.Reason, word) {
		t.Fatalf("run = %s %q, want it closed with his answer", answered.State, answered.Reason)
	}
	if len(*told) != 1 || !strings.Contains((*told)[0], word) || !strings.Contains((*told)[0], r.ID) {
		t.Fatalf("the goblin was told %q, want his answer", *told)
	}
}

func TestSkipAndOtherOnACommandTakeOnlyTheirShape(t *testing.T) {
	for name, a := range map[string]Action{
		"a skip with text":         {ID: "skip-text", Kind: "run_skip", Text: "no", RunID: "run-olive-task-7"},
		"an answer with no text":   {ID: "other-empty", Kind: "run_answer", Text: " ", RunID: "run-olive-task-7"},
		"a skip of no such run":    {ID: "skip-none", Kind: "run_skip", RunID: "run-olive-task-9"},
		"an answer with old proof": {ID: "other-old", Kind: "run_answer", Text: "use the key", RunID: "run-olive-task-7", Generation: "old"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			s, _, r, told := skipBoard(t)
			if a.Generation == "" {
				a.Generation = r.Identity
			}

			// Act
			_, err := s.Store.Queue(a)

			// Assert
			if err == nil {
				t.Fatal("the press was taken")
			}
			if run := s.Store.Snapshot().Runs[0]; run.State != "ready" || len(*told) != 0 {
				t.Fatalf("run = %s, told %q; want it untouched", run.State, *told)
			}
		})
	}
}

// Skip on a goblin's question clears it as Dismiss did, and now tells the
// goblin itself, not only the CFO, to go on without it.
func TestSkipOnAGoblinsQuestionTellsTheGoblinToGoOnWithoutIt(t *testing.T) {
	// Arrange
	s, meta, _, told := skipBoard(t)
	question := Question{ID: "notify-olive-task-4", Identity: goblinIdentity(meta), Task: meta.ID, Seq: 4, Text: "Which store?", Status: "pending", CreatedAt: time.Now().UTC()}
	s.Store.mu.Lock()
	s.Store.db.Questions = append(s.Store.db.Questions, question)
	s.Store.mu.Unlock()

	// Act
	err := pressOnRun(t, s, Action{ID: "skip-question", Kind: "question_clear", QuestionID: question.ID, Generation: question.Identity})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(*told) != 1 || !strings.Contains((*told)[0], question.ID) || !strings.Contains((*told)[0], "Go on without it") {
		t.Fatalf("the goblin was told %q, want one note to go on without its question", *told)
	}
}
