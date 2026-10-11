package uninstall

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "core.autocrlf=false"}, args...)...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, output)
	}
	return strings.TrimSpace(string(output))
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// goblinWork is a person's clone with the home's goblins at work in it, as a
// spawn leaves them: g1 detached at origin's main with a commit it never
// pushed and a file it never committed, and g2 on a branch it made, with a
// commit. The person has a branch of their own from before, an ignore line of
// their own, and cleanup archived a goblin's work in a tag.
type goblinWork struct {
	machine  Machine
	checkout string
	kept     string
}

func newGoblinWork(t *testing.T) goblinWork {
	t.Helper()
	dir := t.TempDir()
	origin := filepath.Join(dir, "origin.git")
	git(t, dir, "init", "-q", "--bare", "-b", "main", origin)
	seed := filepath.Join(dir, "seed")
	git(t, dir, "init", "-q", "-b", "main", seed)
	write(t, filepath.Join(seed, "README.md"), "demo\n")
	git(t, seed, "add", "README.md")
	git(t, seed, "commit", "-q", "-m", "first")
	git(t, seed, "push", "-q", origin, "main")
	checkout := filepath.Join(dir, "person", "demo")
	git(t, dir, "clone", "-q", origin, checkout)
	git(t, checkout, "branch", "mine")
	write(t, filepath.Join(checkout, ".git", "info", "exclude"), "# git ls-files --others --exclude-from=.git/info/exclude\nmine.log\n"+worktree.ExcludeMarker+"\n.env\n")
	// The goblins' branches are made after their worktrees, by the second
	// that git's records keep.
	time.Sleep(1100 * time.Millisecond)

	root := filepath.Join(dir, "home")
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	if err := os.MkdirAll(h.State, 0o755); err != nil {
		t.Fatal(err)
	}
	g1 := filepath.Join(root, "worktrees", "demo", "g1")
	git(t, checkout, "worktree", "add", "-q", "--detach", g1, "origin/main")
	write(t, filepath.Join(g1, "unpushed.txt"), "a commit never pushed\n")
	git(t, g1, "add", "unpushed.txt")
	git(t, g1, "commit", "-q", "-m", "work not pushed")
	write(t, filepath.Join(g1, "uncommitted.txt"), "work not committed\n")

	g2 := filepath.Join(root, "worktrees", "demo", "g2")
	git(t, checkout, "worktree", "add", "-q", "--detach", g2, "origin/main")
	time.Sleep(1100 * time.Millisecond)
	git(t, g2, "switch", "-q", "-c", "feat/g2")
	write(t, filepath.Join(g2, "g2.txt"), "g2\n")
	git(t, g2, "add", "g2.txt")
	git(t, g2, "commit", "-q", "-m", "g2 work")

	archived := filepath.Join(dir, "archived")
	git(t, checkout, "worktree", "add", "-q", "--detach", archived, "origin/main")
	write(t, filepath.Join(archived, "old.txt"), "old\n")
	git(t, archived, "add", "old.txt")
	git(t, archived, "commit", "-q", "-m", "archived work")
	git(t, checkout, "tag", "archive/g0", git(t, archived, "rev-parse", "HEAD"))
	git(t, checkout, "worktree", "remove", "--force", archived)
	if err := state.RecordArchiveTag(h.State, state.ArchiveTag{Checkout: checkout, Tag: "archive/g0"}); err != nil {
		t.Fatal(err)
	}

	profile := filepath.Join(dir, "profile")
	return goblinWork{
		machine:  Machine{Home: h, Profile: profile, LocalAppData: filepath.Join(profile, "local"), AppData: filepath.Join(profile, "roaming"), Temp: filepath.Join(profile, "temp"), Commands: execx.OSRunner{}},
		checkout: checkout,
		kept:     filepath.Join(profile, KeptFolderName),
	}
}

func (w goblinWork) plan(t *testing.T, choices Choices) Plan {
	t.Helper()
	m := w.machine
	p := Plan{Home: m.Home.Root, Kept: w.kept, aside: new(string), finders: []finder{
		{GroupWork, func(ctx context.Context) ([]Item, error) { return m.work(ctx, w.kept, choices) }},
		{GroupCheckouts, func(ctx context.Context) ([]Item, error) { return m.checkouts(ctx, choices) }},
	}}
	for _, f := range p.finders {
		items, err := f.find(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		p.Items = append(p.Items, items...)
	}
	return p
}

func TestTheGoblinsWorkIsKeptThenTheirRecordsLeaveTheCheckout(t *testing.T) {
	// Arrange
	w := newGoblinWork(t)
	plan := w.plan(t, Choices{})

	// Act
	outcome := plan.Run(context.Background(), func(Group, []Item, []Left, bool) {})

	// Assert
	if len(outcome.Left) > 0 || outcome.Stopped {
		t.Fatalf("left %v", outcome.Left)
	}
	var keeps []string
	for _, item := range plan.Items {
		if item.Action == Keep {
			keeps = append(keeps, item.What)
		}
	}
	for _, want := range []string{"g1's work, 1 commit not pushed, 1 changed file", "g2's work, 1 commit not pushed", "an archived goblin branch, archive/g0, 1 commit not pushed"} {
		if !strings.Contains(strings.Join(keeps, "\n"), want) {
			t.Errorf("the plan keeps %q, want %q among them", keeps, want)
		}
	}
	g1 := filepath.Join(w.kept, "work", "demo", "g1")
	if data, err := os.ReadFile(filepath.Join(g1, "files", "uncommitted.txt")); err != nil || string(data) != "work not committed\n" {
		t.Errorf("the kept uncommitted file = %q, %v", data, err)
	}
	for _, bundle := range []string{filepath.Join(g1, "commits.bundle"), filepath.Join(w.kept, "work", "demo", "g2", "commits.bundle"), filepath.Join(w.kept, "work", "demo", "archive-g0.bundle")} {
		git(t, w.checkout, "bundle", "verify", "--quiet", bundle)
	}
	if note, err := os.ReadFile(filepath.Join(g1, "README.txt")); err != nil || !strings.Contains(string(note), "fetch") || strings.ContainsAny(string(note), ";—") {
		t.Errorf("the kept note = %q, %v; want how to bring the commits back, with no semicolon or dash", note, err)
	}
	if listed := git(t, w.checkout, "worktree", "list", "--porcelain"); strings.Count(listed, "worktree ") != 1 {
		t.Errorf("the checkout still lists goblin worktrees:\n%s", listed)
	}
	branches := git(t, w.checkout, "branch", "--list")
	if strings.Contains(branches, "feat/g2") || !strings.Contains(branches, "mine") {
		t.Errorf("branches = %q, want the goblin's gone and the person's own kept", branches)
	}
	if tags := git(t, w.checkout, "tag", "--list"); tags != "" {
		t.Errorf("tags = %q, want the archive tag gone once its work was kept", tags)
	}
	exclude, _ := os.ReadFile(filepath.Join(w.checkout, ".git", "info", "exclude"))
	if strings.Contains(string(exclude), ".env") || strings.Contains(string(exclude), worktree.ExcludeMarker) || !strings.Contains(string(exclude), "mine.log") {
		t.Errorf("exclude = %q, want the marked line gone and the person's own kept", exclude)
	}
	if again := w.plan(t, Choices{}); len(again.Items) != 0 {
		t.Errorf("a second plan finds %v, want nothing left", names(again.Items))
	}
}

// A person who chose to remove the work too gets nothing kept, and the
// worktrees say what they held as they go.
func TestTheGoblinsWorkGoesWhenThePersonChoseSo(t *testing.T) {
	// Arrange
	w := newGoblinWork(t)

	// Act
	plan := w.plan(t, Choices{RemoveWork: true})
	outcome := plan.Run(context.Background(), func(Group, []Item, []Left, bool) {})

	// Assert
	for _, item := range plan.Items {
		if item.Action == Keep {
			t.Errorf("the plan keeps %s although the person chose to remove the work", item.What)
		}
	}
	if !strings.Contains(strings.Join(names(plan.Items), "\n"), "g1") {
		t.Fatalf("the plan names no worktree: %v", names(plan.Items))
	}
	if len(outcome.Left) > 0 {
		t.Errorf("left %v", outcome.Left)
	}
	if _, err := os.Stat(w.kept); !os.IsNotExist(err) {
		t.Errorf("the kept folder was made: %v", err)
	}
}
