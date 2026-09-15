package taskcontext

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

func TestReadRetirementRejectsTransientV1(t *testing.T) {
	h := home.Home{State: filepath.Join(t.TempDir(), "state")}
	paths := PathsFor(h, "task")
	if err := os.MkdirAll(filepath.Dir(paths.Manifest), 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	receipt := Retirement{
		Schema:    "cfo-task-retirement.v1",
		Meta:      state.TaskMeta{ID: "task"},
		Paths:     paths,
		Merge:     worktree.MergeProof{Head: "0123456789abcdef0123456789abcdef01234567", DefaultHead: "0123456789abcdef0123456789abcdef01234567", Method: "ancestor", VerifiedAt: now},
		RetiredAt: now,
	}
	if err := writeRetirement(paths, receipt); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRetirementState(h, "task"); err == nil {
		t.Fatal("transient v1 retirement receipt was accepted")
	}
}
