package reap

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// registerEverything answers git worktree list with every directory under the
// queried root's .worktrees/, so a test about which directories are scanned is
// not also a test about registration.
type registerEverything struct{}

func (registerEverything) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	entries, err := os.ReadDir(filepath.Join(req.Dir, ".worktrees"))
	if err != nil {
		return execx.Result{Stdout: []byte("worktree " + req.Dir + "\n")}, nil
	}
	lines := []string{"worktree " + req.Dir}
	for _, entry := range entries {
		lines = append(lines, "worktree "+filepath.Join(req.Dir, ".worktrees", entry.Name()))
	}
	return execx.Result{Stdout: []byte(strings.Join(lines, "\n") + "\n")}, nil
}

// A junction is how an operator keeps a checkout on another drive while it
// still sits under the projects root, so the scan has to see through one.
func TestWorktreesScansAJunctionedCheckout(t *testing.T) {
	root := t.TempDir()
	projects := filepath.Join(root, "dev")
	elsewhere := filepath.Join(root, "other-drive", "BigRepo")
	for _, dir := range []string{projects, filepath.Join(elsewhere, ".worktrees", "gb-archived")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	junction := filepath.Join(projects, "BigRepo")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", junction, elsewhere).CombinedOutput(); err != nil {
		t.Skipf("no directory junction available here: %v: %s", err, out)
	}

	collector := Collector{
		Home:         home.Home{Root: filepath.Join(root, "home")},
		Commands:     registerEverything{},
		ProjectsRoot: func() (string, error) { return projects, nil },
	}

	var notes []string
	found := collector.worktrees(context.Background(), nil, &notes)
	want := filepath.Join(junction, ".worktrees", "gb-archived")
	if len(found) != 1 || found[0].Path != want {
		t.Errorf("worktrees = %+v, want only %q", found, want)
	}
	if len(notes) != 0 {
		t.Errorf("unexpected notes: %q", notes)
	}
}

func TestWorktreesScansTheProjectsRootForGoblinWorktreesOnly(t *testing.T) {
	root := t.TempDir()
	projects := filepath.Join(root, "dev")
	known := filepath.Join(projects, "known")
	for _, dir := range []string{
		filepath.Join(root, "home"),
		filepath.Join(known, ".worktrees", "gb-live"),
		filepath.Join(known, ".worktrees", "stray"),
		filepath.Join(projects, "forgotten", ".worktrees", "gb-archived"),
		filepath.Join(projects, "forgotten", ".worktrees", "operators-own"),
		filepath.Join(projects, "untouched"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	collector := Collector{
		Home:         home.Home{Root: filepath.Join(root, "home")},
		Commands:     registerEverything{},
		ProjectsRoot: func() (string, error) { return projects, nil },
	}
	tasks := []Task{{ID: "live", Meta: state.TaskMeta{Project: known}}}

	var notes []string
	var got []string
	for _, worktree := range collector.worktrees(context.Background(), tasks, &notes) {
		got = append(got, worktree.Path)
	}
	sort.Strings(got)
	want := []string{
		// A project a task names reports everything, exactly as before.
		filepath.Join(known, ".worktrees", "gb-live"),
		filepath.Join(known, ".worktrees", "stray"),
		// A checkout no task names is reached through the projects root, and
		// only its goblin worktree is the fleet's to report.
		filepath.Join(projects, "forgotten", ".worktrees", "gb-archived"),
	}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("worktrees = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("worktrees = %q, want %q", got, want)
			break
		}
	}
	if len(notes) != 0 {
		t.Errorf("unexpected notes: %q", notes)
	}

	collector.ProjectsRoot = nil
	if found := collector.worktrees(context.Background(), tasks, &notes); len(found) != 2 {
		t.Errorf("without a projects root the scan found %d worktrees, want the known project's 2", len(found))
	}
}

// gitInit builds a real repository with one commit, because the premise this
// scan rests on is what a real git says, not what a fake says it says.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required to read a repository's worktree registrations")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "add", "README.md"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-m", "initial"},
	} {
		command := exec.Command("git", args...)
		command.Dir = dir
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

// TestWorktreesConfirmRegistrationWithTheRepository is the premise check. A
// task that dies leaves its directory behind, the repository drops it from git
// worktree list, and nothing on disk distinguishes the shell from the live
// worktree beside it. Only the repository can say which is which.
func TestWorktreesConfirmRegistrationWithTheRepository(t *testing.T) {
	root := t.TempDir()
	projects := filepath.Join(root, "dev")
	checkout := filepath.Join(projects, "repo")
	gitInit(t, checkout)

	live := filepath.Join(checkout, ".worktrees", "gb-live")
	command := exec.Command("git", "worktree", "add", "--detach", live)
	command.Dir = checkout
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v: %s", err, out)
	}
	shell := filepath.Join(checkout, ".worktrees", "gb-dead")
	if err := os.MkdirAll(shell, 0o755); err != nil {
		t.Fatal(err)
	}

	collector := Collector{
		Home:         home.Home{Root: filepath.Join(root, "home")},
		Commands:     execx.OSRunner{},
		ProjectsRoot: func() (string, error) { return projects, nil },
	}
	var notes []string
	registration := map[string]Registration{}
	for _, worktree := range collector.worktrees(context.Background(), nil, &notes) {
		registration[worktree.Path] = worktree.Registration
	}
	if registration[live] != RegistrationListed {
		t.Errorf("the live worktree was not confirmed registered; scanned %v", registration)
	}
	if registration[shell] != RegistrationUnlisted {
		t.Errorf("an empty directory shell was not placed as unlisted; scanned %v", registration)
	}
	if len(notes) != 0 {
		t.Errorf("unexpected notes: %q", notes)
	}
}

// TestWorktreesConfirmRegistrationThroughAJunction: git answers with the
// junction's target while the scan joins the junction's own name, so comparing
// the two spellings reports every live worktree in a junctioned checkout as an
// abandoned directory. Both sides have to be resolved before they are compared.
func TestWorktreesConfirmRegistrationThroughAJunction(t *testing.T) {
	root := t.TempDir()
	projects := filepath.Join(root, "dev")
	elsewhere := filepath.Join(root, "other-drive", "BigRepo")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, elsewhere)
	junction := filepath.Join(projects, "BigRepo")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", junction, elsewhere).CombinedOutput(); err != nil {
		t.Skipf("no directory junction available here: %v: %s", err, out)
	}

	live := filepath.Join(junction, ".worktrees", "gb-live")
	command := exec.Command("git", "worktree", "add", "--detach", live)
	command.Dir = junction
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v: %s", err, out)
	}

	collector := Collector{
		Home:         home.Home{Root: filepath.Join(root, "home")},
		Commands:     execx.OSRunner{},
		ProjectsRoot: func() (string, error) { return projects, nil },
	}
	var notes []string
	found := collector.worktrees(context.Background(), nil, &notes)
	if len(found) != 1 || found[0].Path != live {
		t.Fatalf("worktrees = %+v, want only %q", found, live)
	}
	if found[0].Registration != RegistrationListed {
		t.Errorf("a live worktree reached through a junction was not confirmed registered: %+v", found[0])
	}
	if len(notes) != 0 {
		t.Errorf("unexpected notes: %q", notes)
	}
}

// TestWorktreesSayWhenRegistrationCannotBeConfirmed: a scan with no way to ask
// the repository has not proven anything is a worktree, and says so rather
// than assuming it.
func TestWorktreesSayWhenRegistrationCannotBeConfirmed(t *testing.T) {
	root := t.TempDir()
	checkout := filepath.Join(root, "home")
	if err := os.MkdirAll(filepath.Join(checkout, ".worktrees", "gb-live"), 0o755); err != nil {
		t.Fatal(err)
	}
	collector := Collector{Home: home.Home{Root: checkout}}

	var notes []string
	found := collector.worktrees(context.Background(), nil, &notes)
	if len(found) != 1 || found[0].Registration != RegistrationUnknown {
		t.Fatalf("worktrees = %+v, want one directory whose registration is unknown", found)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "no command runner") {
		t.Fatalf("notes = %q, want the missing-runner note", notes)
	}
}
