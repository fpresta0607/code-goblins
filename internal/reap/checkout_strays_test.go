package reap

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// A worktree a project registers at a path the fleet's folders do not hold is
// one a harness's own tooling left there, in a checkout or a scratchpad: no
// task of this home records it, and nothing else in the fleet reports it. The
// scan lists it with the checkout it belongs to, as a stray, so the sweep can
// see it at all.
func TestWorktreesFindAWorktreeAProjectRegistersAwayFromTheFleetsFolders(t *testing.T) {
	// Arrange
	root := t.TempDir()
	projects := filepath.Join(root, "dev")
	checkout := filepath.Join(projects, "repo")
	gitInit(t, checkout)
	leftover := filepath.Join(checkout, ".claude", "worktrees", "leftover")
	command := exec.Command("git", "worktree", "add", "--detach", leftover)
	command.Dir = checkout
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v: %s", err, out)
	}
	collector := Collector{
		Home:         withRecords(t, filepath.Join(root, "home")),
		Commands:     execx.OSRunner{},
		ProjectsRoot: func() (string, error) { return projects, nil },
	}

	// Act
	var notes []string
	found := collector.worktrees(context.Background(), nil, &notes)

	// Assert
	var stray *WorktreeDir
	for index, worktree := range found {
		if sameFolder(t, worktree.Path, leftover) {
			stray = &found[index]
		}
	}
	if stray == nil {
		t.Fatalf("worktrees = %+v, want the checkout's own harness worktree among them; notes %q", found, notes)
	}
	if stray.Source != SourceCheckout {
		t.Errorf("source = %q, want %q: a path the fleet's folders do not hold", stray.Source, SourceCheckout)
	}
	if stray.Registration != RegistrationListed {
		t.Errorf("registration = %q, want listed: the project answered worktree list itself", stray.Registration)
	}
	if !sameFolder(t, stray.Project, checkout) {
		t.Errorf("project = %q, want the checkout %q", stray.Project, checkout)
	}
	if len(notes) != 0 {
		t.Errorf("unexpected notes: %q", notes)
	}
	if _, err := os.Stat(leftover); err != nil {
		t.Fatalf("the scan removed something: %v", err)
	}
}

// The sweep reports a checkout's harness worktree as no task's, and says what
// left it there, rather than describing it as a goblin's worktree whose list is
// missing.
func TestACheckoutWorktreeIsReportedAsAHarnesssLeftover(t *testing.T) {
	// Arrange
	checkout := filepath.Join(t.TempDir(), "repo")
	stray := WorktreeDir{Path: filepath.Join(checkout, ".claude", "worktrees", "leftover"), Project: checkout, Source: SourceCheckout, Registration: RegistrationListed}

	// Act
	findings := Classify(Inventory{Worktrees: []WorktreeDir{stray}})

	// Assert
	for _, finding := range findings {
		if finding.Class != OrphanWorktree || finding.Path != stray.Path {
			continue
		}
		if finding.TaskID != "" {
			t.Errorf("task = %q, want none: no task of this home records it", finding.TaskID)
		}
		if !strings.Contains(finding.Detail, "left by a harness or an older session") {
			t.Errorf("detail = %q, want it naming what left the worktree there", finding.Detail)
		}
		return
	}
	t.Fatalf("findings = %+v, want the checkout's worktree reported", findings)
}

// sameFolder reports whether two paths name one folder. git spells a path
// through an 8.3 folder name, as C:\Users\RUNNER~1\... for
// C:\Users\runneradmin\..., so a comparison by spelling fails on a machine
// whose temporary folder has one.
func sameFolder(t *testing.T, left, right string) bool {
	t.Helper()
	if strings.EqualFold(filepath.Clean(left), filepath.Clean(right)) {
		return true
	}
	leftInfo, err := os.Stat(left)
	if err != nil {
		return false
	}
	rightInfo, err := os.Stat(right)
	if err != nil {
		return false
	}
	return os.SameFile(leftInfo, rightInfo)
}

func TestWorktreesDoNotListAFleetWorktreeTwiceThroughAJunction(t *testing.T) {
	root := t.TempDir()
	checkout := filepath.Join(root, "project")
	gitInit(t, checkout)
	h := withRecords(t, filepath.Join(root, "home"))
	alias := filepath.Join(root, "home-alias")
	result, err := (execx.OSRunner{}).Run(context.Background(), execx.Request{Name: "cmd", Args: []string{"/c", "mklink", "/J", alias, h.Root}})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("create junction: %v: %s", err, result.Stderr)
	}
	h.Root = alias
	path := filepath.Join(h.Worktrees(), "project", "live")
	result, err = (execx.OSRunner{}).Run(context.Background(), execx.Request{Dir: checkout, Name: "git", Args: []string{"worktree", "add", "--detach", path}})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("create worktree: %v: %s", err, result.Stderr)
	}
	tasks := []Task{{ID: "live", Meta: state.TaskMeta{ID: "live", Project: checkout, Worktree: path}}}
	collector := Collector{Home: h, Commands: execx.OSRunner{}}

	var notes []string
	found := collector.worktrees(context.Background(), tasks, &notes)

	if len(found) != 1 || found[0].TaskID != "live" || found[0].Source == SourceCheckout {
		t.Fatalf("worktrees = %+v, notes = %v; want the live task once, never a checkout stray", found, notes)
	}
}
