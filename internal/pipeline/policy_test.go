package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckedInPolicyUsesCodexForEveryGateRole(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "config", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		Version  int      `json:"version"`
		Primary  Reviewer `json:"primary"`
		Reviewer Reviewer `json:"reviewer"`
		Fixer    Reviewer `json:"fixer"`
	}
	if err := json.Unmarshal(data, &policy); err != nil {
		t.Fatal(err)
	}
	want := Reviewer{Harness: "codex", Model: "gpt-5.6-sol", Effort: "high"}
	if policy.Version != 2 || policy.Primary != want || policy.Reviewer != want || policy.Fixer != want {
		t.Fatalf("policy=%+v, want v2 Codex profile for every role", policy)
	}
}

func TestMigrateSelectionPreservesClassAndReviewCycleCap(t *testing.T) {
	legacy := Policy{
		Version:  1,
		Reviewer: Reviewer{Harness: "claude", Model: "opus", Effort: "high"},
		AutoFix:  AutoFix{Review: 0, Test: 1, Lint: 1, Rebase: 1, CI: 1},
		Classes:  Classes{Ordinary: Class{ReviewCycles: 2}, HighRisk: Class{ReviewCycles: 3}, Mechanical: Class{ReviewCycles: 2}},
	}
	old, err := legacy.Select("high-risk")
	if err != nil {
		t.Fatal(err)
	}
	current := testPolicy(t)
	migrated, err := MigrateSelection(old, current)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.Policy != current || migrated.Class != old.Class || migrated.ReviewCycles != old.ReviewCycles || migrated.Hash == old.Hash {
		t.Fatalf("migrated=%+v old=%+v", migrated, old)
	}
}

func TestLoadSelectionAcceptsFrozenVersionOneHash(t *testing.T) {
	const snapshot = `{
  "policy": {
    "version": 1,
    "reviewer": {"harness": "claude", "model": "opus", "effort": "high"},
    "auto_fix": {"review": 0, "test": 1, "lint": 1, "rebase": 1, "ci": 1},
    "classes": {
      "ordinary": {"review_cycles": 2},
      "high-risk": {"review_cycles": 3},
      "mechanical": {"review_cycles": 2}
    }
  },
  "class": "ordinary",
  "review_cycles": 2,
  "policy_sha256": "aa8eb7346aef93bd82a88cf6fad446a46c9f6abaebc2c40b700ee7c607d313e5"
}`
	path := filepath.Join(t.TempDir(), "pipeline.json")
	if err := os.WriteFile(path, []byte(snapshot), 0600); err != nil {
		t.Fatal(err)
	}
	selection, err := LoadSelection(path)
	if err != nil {
		t.Fatal(err)
	}
	if selection.Policy.Version != 1 || selection.Class != "ordinary" || selection.ReviewCycles != 2 {
		t.Fatalf("selection=%+v", selection)
	}
}

func TestCheckedInPolicyAndSnapshot(t *testing.T) {
	p, err := Load(filepath.Join("..", "..", "config", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	for class, want := range map[string]int{"ordinary": 2, "mechanical": 2, "high-risk": 3} {
		s, err := p.Select(class)
		if err != nil || s.ReviewCycles != want {
			t.Fatalf("%s: %+v %v", class, s, err)
		}
		path := filepath.Join(t.TempDir(), "pipeline.json")
		if err := s.Save(path); err != nil {
			t.Fatal(err)
		}
		got, err := LoadSelection(path)
		if err != nil || got != s {
			t.Fatalf("snapshot: %+v %v", got, err)
		}
	}
	if _, err := p.Select("unknown"); err == nil {
		t.Fatal("unknown class accepted")
	}
}

func TestPolicyRejectsInvalidInput(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "config", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		string(data) + `{}`, strings.Replace(string(data), `"version": 2`, `"version": 3`, 1),
		strings.Replace(string(data), `"version": 2`, `"typo": 2`, 1),
		strings.Replace(string(data), `"review_cycles": 2`, `"review_cycles": 10`, 1),
		strings.Replace(string(data), `"review": 0`, `"review": 10`, 1),
		strings.Replace(string(data), `"effort": "high"`, `"effort": "low"`, 1),
		strings.Replace(string(data), `"harness": "codex"`, `"harness": "claude"`, 1),
	} {
		path := filepath.Join(t.TempDir(), "policy.json")
		if err := os.WriteFile(path, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatal("invalid policy accepted")
		}
	}
}
