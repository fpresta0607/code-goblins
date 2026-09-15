package taskcontext

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

type Retirement struct {
	Schema    string              `json:"schema"`
	Meta      state.TaskMeta      `json:"task"`
	Paths     Paths               `json:"paths"`
	Merge     worktree.MergeProof `json:"merge"`
	RetiredAt time.Time           `json:"retired_at"`
}

// Written only after cleanup has proved merge, returned the worktree and
// confirmed the worker absent. This survives removal of the live metadata.
func SaveRetirement(h home.Home, meta state.TaskMeta, proof worktree.MergeProof) error {
	p := PathsFor(h, meta.ID)
	receipt := Retirement{Schema: "cfo-task-retirement.v1", Meta: meta, Paths: p, Merge: proof, RetiredAt: time.Now().UTC()}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(filepath.Dir(p.Manifest), "retirement.json"), data)
}

func ReadRetirement(h home.Home, id string) (Retirement, error) {
	var receipt Retirement
	if err := state.ValidTaskID(id); err != nil {
		return receipt, err
	}
	p := PathsFor(h, id)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(p.Manifest), "retirement.json"))
	if err != nil {
		return receipt, err
	}
	if err = json.Unmarshal(data, &receipt); err != nil {
		return receipt, err
	}
	if receipt.Schema != "cfo-task-retirement.v1" || receipt.Meta.ID != id || receipt.Paths != p || receipt.RetiredAt.IsZero() || receipt.Merge.VerifiedAt.IsZero() || len(receipt.Merge.Head) != 40 || len(receipt.Merge.DefaultHead) != 40 || (receipt.Merge.Method != "ancestor" && receipt.Merge.Method != "content-equal") {
		return receipt, errors.New("retirement: incomplete ownership or merge proof")
	}
	return receipt, nil
}
