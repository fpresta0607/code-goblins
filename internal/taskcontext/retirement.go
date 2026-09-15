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
	Schema     string              `json:"schema"`
	Stage      string              `json:"stage,omitempty"`
	Meta       state.TaskMeta      `json:"task"`
	Paths      Paths               `json:"paths"`
	Merge      worktree.MergeProof `json:"merge"`
	PreparedAt time.Time           `json:"prepared_at,omitempty"`
	RetiredAt  time.Time           `json:"retired_at"`
}

const (
	retirementSchema   = "cfo-task-retirement.v2"
	retirementPrepared = "return-prepared"
	retirementComplete = "retired"
)

func StageRetirement(h home.Home, meta state.TaskMeta, proof worktree.MergeProof) error {
	p := PathsFor(h, meta.ID)
	receipt := Retirement{Schema: retirementSchema, Stage: retirementPrepared, Meta: meta, Paths: p, Merge: proof, PreparedAt: time.Now().UTC()}
	return writeRetirement(p, receipt)
}

func CompleteRetirement(h home.Home, id string) error {
	receipt, err := ReadRetirementState(h, id)
	if err != nil {
		return err
	}
	if receipt.Stage == retirementComplete {
		return nil
	}
	receipt.Stage = retirementComplete
	receipt.RetiredAt = time.Now().UTC()
	return writeRetirement(receipt.Paths, receipt)
}

func writeRetirement(p Paths, receipt Retirement) error {
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(filepath.Dir(p.Manifest), "retirement.json"), data)
}

func ReadRetirement(h home.Home, id string) (Retirement, error) {
	receipt, err := ReadRetirementState(h, id)
	if err != nil {
		return receipt, err
	}
	if receipt.Stage != retirementComplete || receipt.RetiredAt.IsZero() {
		return receipt, errors.New("retirement: worktree return is not complete")
	}
	return receipt, nil
}

func ReadRetirementState(h home.Home, id string) (Retirement, error) {
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
	if receipt.Schema != retirementSchema || receipt.Meta.ID != id || receipt.Paths != p || receipt.PreparedAt.IsZero() || receipt.Merge.VerifiedAt.IsZero() || len(receipt.Merge.Head) != 40 || len(receipt.Merge.DefaultHead) != 40 || (receipt.Merge.Method != "ancestor" && receipt.Merge.Method != "content-equal") {
		return receipt, errors.New("retirement: incomplete ownership or merge proof")
	}
	if receipt.Stage != retirementPrepared && receipt.Stage != retirementComplete {
		return receipt, errors.New("retirement: invalid return stage")
	}
	if receipt.Stage == retirementComplete && receipt.RetiredAt.IsZero() {
		return receipt, errors.New("retirement: completed return has no timestamp")
	}
	return receipt, nil
}
