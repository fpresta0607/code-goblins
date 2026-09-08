// Package pipeline supplies checked-in policy to the existing no-mistakes engine.
package pipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

type Reviewer struct {
	Harness string `json:"harness"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
}

type AutoFix struct {
	Review int `json:"review"`
	Test   int `json:"test"`
	Lint   int `json:"lint"`
	Rebase int `json:"rebase"`
	CI     int `json:"ci"`
}

type Class struct {
	ReviewCycles int `json:"review_cycles"`
}
type Classes struct {
	Ordinary   Class `json:"ordinary"`
	HighRisk   Class `json:"high-risk"`
	Mechanical Class `json:"mechanical"`
}
type Policy struct {
	Version  int      `json:"version"`
	Reviewer Reviewer `json:"reviewer"`
	AutoFix  AutoFix  `json:"auto_fix"`
	Classes  Classes  `json:"classes"`
}

// Selection freezes the whole approved policy plus the chosen class at spawn.
// Editing the source later cannot silently change a running task's budget.
type Selection struct {
	Policy       Policy `json:"policy"`
	Class        string `json:"class"`
	ReviewCycles int    `json:"review_cycles"`
	Hash         string `json:"policy_sha256"`
}

func decode(path string, target interface{}) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 65537))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return errors.New("pipeline: invalid JSON or unknown policy field")
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		return errors.New("pipeline: trailing policy data")
	}
	return nil
}

func Load(path string) (Policy, error) {
	var p Policy
	if err := decode(path, &p); err != nil {
		return p, err
	}
	return p, p.Validate()
}

func (p Policy) Validate() error {
	if p.Version != 1 {
		return errors.New("pipeline: policy version must be 1")
	}
	if p.Reviewer != (Reviewer{"claude", "opus", "high"}) {
		return errors.New("pipeline: approved reviewer is Claude Opus high")
	}
	if p.AutoFix != (AutoFix{Review: 0, Test: 1, Lint: 1, Rebase: 1, CI: 1}) {
		return errors.New("pipeline: automatic review must be 0 and test/lint/rebase/ci follow-ups must be 1")
	}
	if p.Classes != (Classes{Class{2}, Class{3}, Class{2}}) {
		return errors.New("pipeline: review repair budgets must be ordinary 2, high-risk 3, mechanical 2")
	}
	return nil
}

func ValidClass(class string) bool {
	return class == "ordinary" || class == "high-risk" || class == "mechanical"
}

func (p Policy) Select(class string) (Selection, error) {
	if err := p.Validate(); err != nil {
		return Selection{}, err
	}
	var budget int
	switch class {
	case "ordinary":
		budget = p.Classes.Ordinary.ReviewCycles
	case "high-risk":
		budget = p.Classes.HighRisk.ReviewCycles
	case "mechanical":
		budget = p.Classes.Mechanical.ReviewCycles
	default:
		return Selection{}, errors.New("pipeline: class must be ordinary, high-risk, or mechanical")
	}
	data, err := json.Marshal(p)
	if err != nil {
		return Selection{}, err
	}
	sum := sha256.Sum256(data)
	return Selection{p, class, budget, hex.EncodeToString(sum[:])}, nil
}

func (s Selection) Validate() error {
	want, err := s.Policy.Select(s.Class)
	if err != nil {
		return err
	}
	if want != s {
		return errors.New("pipeline: task policy snapshot does not match its hash and class")
	}
	return nil
}

func (s Selection) Save(path string) error {
	if err := s.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, append(data, '\n'))
}

func LoadSelection(path string) (Selection, error) {
	var s Selection
	if err := decode(path, &s); err != nil {
		return s, err
	}
	return s, s.Validate()
}

func (s Selection) Instruction(id, path string) string {
	return fmt.Sprintf(" Pipeline policy: read %s. Class %s permits %d review repair cycles, then unresolved. Use cfo pipeline run %s --intent <intent> and cfo pipeline respond %s for gate decisions. Never use --yes, skip a gate, or bypass an exhausted budget with native AXI. Reviewer is Claude Opus high. Shared config changes require an explicit idle config-apply; spawn never changes it.", path, s.Class, s.ReviewCycles, id, id)
}
