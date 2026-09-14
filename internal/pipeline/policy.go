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
	Primary  Reviewer `json:"primary,omitempty"`
	Reviewer Reviewer `json:"reviewer"`
	Fixer    Reviewer `json:"fixer,omitempty"`
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
	switch p.Version {
	case 1:
		if p.Primary != (Reviewer{}) || p.Reviewer != (Reviewer{"claude", "opus", "high"}) || p.Fixer != (Reviewer{}) {
			return errors.New("pipeline: legacy reviewer must be Claude Opus high")
		}
	case 2:
		want := Reviewer{"codex", "gpt-5.6-sol", "high"}
		if p.Primary != want || p.Reviewer != want || p.Fixer != want {
			return errors.New("pipeline: primary, reviewer and fixer must be Codex gpt-5.6-sol high")
		}
	default:
		return errors.New("pipeline: policy version must be 1 or 2")
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
	var hashPolicy interface{} = p
	if p.Version == 1 {
		hashPolicy = struct {
			Version  int      `json:"version"`
			Reviewer Reviewer `json:"reviewer"`
			AutoFix  AutoFix  `json:"auto_fix"`
			Classes  Classes  `json:"classes"`
		}{p.Version, p.Reviewer, p.AutoFix, p.Classes}
	}
	data, err := json.Marshal(hashPolicy)
	if err != nil {
		return Selection{}, err
	}
	sum := sha256.Sum256(data)
	return Selection{Policy: p, Class: class, ReviewCycles: budget, Hash: hex.EncodeToString(sum[:])}, nil
}

func MigrateSelection(old Selection, current Policy) (Selection, error) {
	if err := old.Validate(); err != nil {
		return Selection{}, err
	}
	if err := current.Validate(); err != nil {
		return Selection{}, err
	}
	if old.Policy.Version == current.Version {
		if old.Policy != current {
			return Selection{}, errors.New("pipeline: policy migration requires a newer approved version")
		}
		return old, nil
	}
	if old.Policy.Version != 1 || current.Version != 2 {
		return Selection{}, errors.New("pipeline: unsupported task policy migration")
	}
	next, err := current.Select(old.Class)
	if err != nil {
		return Selection{}, err
	}
	next.ReviewCycles = old.ReviewCycles
	if err := next.Validate(); err != nil {
		return Selection{}, err
	}
	return next, nil
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
	roles := "Reviewer is Claude Opus high."
	if s.Policy.Version == 2 {
		roles = "Global primary, reviewer and review-fixer profiles are Codex gpt-5.6-sol high; a CFO gate requires the trusted repository primary to inherit that profile or select Codex explicitly."
	}
	return fmt.Sprintf(" Pipeline policy: read %s. Class %s permits %d review repair cycles, then unresolved. Use cfo pipeline run %s --intent <intent> and cfo pipeline respond %s for gate decisions. Never use --yes, skip a gate, or bypass an exhausted budget with native AXI. %s Shared config changes require an explicit idle config-apply; spawn never changes it.", path, s.Class, s.ReviewCycles, id, id, roles)
}
