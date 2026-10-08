package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	codegoblins "github.com/fpresta0607/code-goblins"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// A checkout an older build made the home is kept when an install updates
// it. The files git tracks there, the contract among them, stay exactly as
// they are; the marker joins them, untracked, so the checkout is the primary
// home this build's commands and hooks look for, as the older build took it
// for one without a marker; the fleet's state, data and worktrees are not
// touched; the programs go into bin, and those at its root are brought up to
// this build, where sessions started before still run them. CFO_HOME stays,
// and PATH gives every new terminal bin.
func TestInstallKeepsACheckoutHomeAndItsFleetAsTheyAre(t *testing.T) {
	// Arrange
	f := newFixture(t, adopterSettings, nil)
	f.service.Contract, f.service.Policy, f.service.Checkout = codegoblins.Contract, codegoblins.Policy, true
	tracked := map[string]string{
		"AGENTS.md":            "the checkout's own contract",
		"CLAUDE.md":            "the checkout's own memory",
		"cmd/cfo/main.go":      "package main",
		"config/pipeline.json": `{"tuned": true}`,
		"data/routing.json":    `{"tuned": true}`,
	}
	fleet := map[string]string{
		"state/tasks/demo-task/meta.json":     `{"id":"demo-task","status":"working"}`,
		"data/demo-task/brief.md":             "# Brief demo-task",
		".worktrees/gb-demo-task/unlanded.go": "package unlanded",
	}
	for name, content := range merged(tracked, fleet) {
		writeFile(t, filepath.Join(f.root, filepath.FromSlash(name)), content)
	}
	if err := os.MkdirAll(filepath.Join(f.root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cfo.exe", "goblins.exe"} {
		writeFile(t, filepath.Join(f.root, name), "older build")
	}
	f.env.values["CFO_HOME"] = f.root
	f.env.values["Path"] = `C:\Windows;` + f.root

	// Act
	output := f.install()

	// Assert
	for name, content := range merged(tracked, fleet) {
		if got := readFile(t, filepath.Join(f.root, filepath.FromSlash(name))); got != content {
			t.Errorf("%s = %q, want it left as %q:\n%s", name, got, content, output)
		}
	}
	if _, err := os.Stat(filepath.Join(f.root, "docs", "pipeline.md")); !os.IsNotExist(err) {
		t.Errorf("the install wrote the contract into the checkout (%v):\n%s", err, output)
	}
	if !home.IsPrimary(home.Home{Root: f.root, State: filepath.Join(f.root, "state")}) {
		t.Errorf("the checkout is not the primary home after the install:\n%s", output)
	}
	for _, program := range []string{filepath.Join(f.bin, "cfo.exe"), filepath.Join(f.bin, "goblins.exe"), filepath.Join(f.root, "cfo.exe"), filepath.Join(f.root, "goblins.exe")} {
		if got := readFile(t, program); got != "build 1" {
			t.Errorf("%s = %q, want this build", program, got)
		}
	}
	if got := f.env.values["CFO_HOME"]; got != f.root {
		t.Errorf("CFO_HOME = %q, want it kept at %s", got, f.root)
	}
	if got := f.env.values["Path"]; got != `C:\Windows;`+f.bin {
		t.Errorf("PATH = %q, want new terminals to find bin:\n%s", got, output)
	}
}

// A git checkout an older build made the home is the primary home once an
// install keeps it, while a linked worktree of it, where a goblin works, is
// not, even with the checkout's marker and a state folder copied into it.
func TestAKeptCheckoutHomeIsPrimaryAndItsLinkedWorktreeIsNot(t *testing.T) {
	// Arrange
	f := newFixture(t, adopterSettings, nil)
	f.service.Contract, f.service.Policy, f.service.Checkout = codegoblins.Contract, codegoblins.Policy, true
	for name, content := range map[string]string{
		"AGENTS.md":                       "the checkout's own contract",
		"cmd/cfo/main.go":                 "package main",
		"state/tasks/demo-task/meta.json": `{"id":"demo-task","status":"working"}`,
	} {
		writeFile(t, filepath.Join(f.root, filepath.FromSlash(name)), content)
	}
	gitIn(t, f.root, "init", "-q")
	gitIn(t, f.root, "add", "AGENTS.md", "cmd")
	gitIn(t, f.root, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "checkout")
	f.env.values["CFO_HOME"] = f.root

	// Act
	output := f.install()

	// Assert
	if !home.IsPrimary(home.Home{Root: f.root, State: filepath.Join(f.root, "state")}) {
		t.Fatalf("the checkout is not the primary home after the install:\n%s", output)
	}
	worktree := filepath.Join(t.TempDir(), "gb-demo-task")
	gitIn(t, f.root, "worktree", "add", "-q", "--detach", worktree)
	writeFile(t, filepath.Join(worktree, home.InstalledMarker), readFile(t, filepath.Join(f.root, home.InstalledMarker)))
	if err := os.MkdirAll(filepath.Join(worktree, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if home.IsPrimary(home.Home{Root: worktree, State: filepath.Join(worktree, "state")}) {
		t.Errorf("a linked worktree of the checkout home is primary")
	}
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	if output, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func merged(maps ...map[string]string) map[string]string {
	all := map[string]string{}
	for _, m := range maps {
		for key, value := range m {
			all[key] = value
		}
	}
	return all
}
