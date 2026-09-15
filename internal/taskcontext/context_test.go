package taskcontext

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestContextSurvivesWorkerAndMetadataRemoval(t *testing.T) {
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	if err := os.MkdirAll(h.State, 0700); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{ID: "task-a", Project: root, Worktree: filepath.Join(root, "worktree"), Brief: filepath.Join(root, "brief.md"), Harness: "claude"}
	if err := os.WriteFile(meta.Brief, []byte("Synthetic task brief survives restart"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(h.State, meta.ID, "blocked: retain my decision"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 250; i++ {
		if err := state.AppendStatus(h.State, meta.ID, "working: still investigating"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Refresh(context.Background(), h, meta.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Decisions) != 1 || got.Browser.Session == "" {
		t.Fatalf("context %+v", got)
	}
	original := got.Browser
	if err := os.Remove(meta.Brief); err != nil {
		t.Fatal(err)
	}
	meta.Harness = "codex"
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	switched, err := Refresh(context.Background(), h, meta.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if switched.Browser != original || switched.Harness != "codex" {
		t.Fatalf("switch context %+v", switched)
	}
	if brief, err := os.ReadFile(switched.Brief); err != nil || string(brief) != "Synthetic task brief survives restart" || !strings.Contains(switched.BriefStatus, "source unavailable") {
		t.Fatalf("lost durable brief: %s %v %+v", brief, err, switched)
	}
	if err := os.Remove(filepath.Join(h.State, meta.ID+".meta")); err != nil {
		t.Fatal(err)
	}
	saved, err := Refresh(context.Background(), h, meta.ID, nil)
	if err != nil || !saved.MetadataMissing || len(saved.Decisions) != 1 {
		t.Fatalf("retained = %+v, %v", saved, err)
	}
	if saved.Pointers["worktree"] != "missing" || !strings.HasPrefix(saved.IdentityStatus, "stale:") {
		t.Fatalf("missing custody hidden: %+v", saved)
	}
}

func TestBrowserOwnershipSeparatesHomesAndTasks(t *testing.T) {
	t.Setenv("CFO_BROWSER_HEADED", "")
	h := home.Home{State: filepath.Join(t.TempDir(), "state")}
	a := PathsFor(h, "a")
	b := PathsFor(h, "b")
	other := PathsFor(home.Home{State: filepath.Join(t.TempDir(), "state")}, "a")
	if a.Browser.Session == b.Browser.Session || a.Browser.Session == other.Browser.Session {
		t.Fatal("browser identity aliases")
	}
	if filepath.Dir(a.Browser.Profile) == filepath.Dir(b.Browser.Profile) {
		t.Fatal("shared profile owner")
	}
	longest := BrowserEnv(h, strings.Repeat("a", 64))["CHROME_DEVTOOLS_AXI_SESSION"]
	if len(longest) < 1 || len(longest) > 64 {
		t.Fatalf("invalid Chrome AXI session length: %d", len(longest))
	}
	// Operator browser arguments must not override the task's own profile.
	t.Setenv("CHROME_DEVTOOLS_AXI_CHROME_ARGS", "--user-data-dir=C:/personal-profile")
	env := BrowserEnv(h, "a")
	if env["CHROME_DEVTOOLS_AXI_CHROME_ARGS"] != "" || env["CHROME_DEVTOOLS_AXI_CHANNEL"] != "stable" || env["CHROME_DEVTOOLS_AXI_USER_DATA_DIR"] != a.Browser.Profile || env["CHROME_DEVTOOLS_AXI_HEADED"] != "0" {
		t.Fatal("task launch inherited a browser storage redirect")
	}
	t.Setenv("CFO_BROWSER_HEADED", "1")
	visible := BrowserEnv(h, "a")
	if visible["CHROME_DEVTOOLS_AXI_HEADED"] != "1" || visible["CHROME_DEVTOOLS_AXI_SESSION"] != a.Browser.Session || visible["CHROME_DEVTOOLS_AXI_USER_DATA_DIR"] != a.Browser.Profile || visible["CHROME_DEVTOOLS_AXI_AUTO_CONNECT"] != "0" || visible["CHROME_DEVTOOLS_AXI_BROWSER_URL"] != "" || visible["CHROME_DEVTOOLS_AXI_PORT"] != "" {
		t.Fatal("attended browser mode changed task ownership or attachment isolation")
	}
	t.Setenv("CFO_BROWSER_HEADED", "true")
	if BrowserEnv(h, "a")["CHROME_DEVTOOLS_AXI_HEADED"] != "0" {
		t.Fatal("non-explicit attended browser mode was accepted")
	}
}

func TestGitIdentityUsesActualBranchAndCommit(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "fixture-branch")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "fixture")
	want := git("rev-parse", "HEAD")
	h := home.Home{Root: root, State: filepath.Join(root, "state")}
	if err := os.MkdirAll(h.State, 0700); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "git-task", Project: root, Worktree: root}); err != nil {
		t.Fatal(err)
	}
	got, err := Refresh(context.Background(), h, "git-task", execx.OSRunner{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Branch != "fixture-branch" || got.Head != want || got.IdentityStatus != "current" {
		t.Fatalf("identity %+v, expected SHA %s", got, want)
	}
	git("checkout", "--detach")
	got, err = Refresh(context.Background(), h, "git-task", execx.OSRunner{})
	if err != nil || got.Head != want || got.Branch != "(detached)" {
		t.Fatalf("detached %+v: %v", got, err)
	}
}
