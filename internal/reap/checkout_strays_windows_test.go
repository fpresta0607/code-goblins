package reap

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

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
