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
	want := Reviewer{Harness: "codex", Model: "gpt-6.1-sol", Effort: "xhigh"}
	if policy.Version != 3 || policy.Primary != want || policy.Reviewer != want || policy.Fixer != want {
		t.Fatalf("policy=%+v, want v3 Codex profile for every role", policy)
	}
}

func TestMigrateSelectionPreservesClassAndReviewCycleCap(t *testing.T) {
	legacy := Policy{
		Version:  1,
		Reviewer: Reviewer{Harness: "claude", Model: "opus", Effort: "high"},
		AutoFix:  AutoFix{Review: 0, Test: 1, Lint: 1, Rebase: 1, CI: 1},
		Classes:  Classes{Ordinary: Class{ReviewCycles: 2}, HighRisk: Class{ReviewCycles: 3}, Mechanical: Class{ReviewCycles: 2}},
	}
	previous := legacy
	previous.Version = 2
	previous.Primary = Reviewer{"codex", "gpt-5.6-sol", "high"}
	previous.Reviewer, previous.Fixer = previous.Primary, previous.Primary
	current := previous
	current.Version = 3
	current.Primary = Reviewer{"codex", "gpt-6.1-sol", "xhigh"}
	current.Reviewer, current.Fixer = current.Primary, current.Primary
	for _, transition := range []struct {
		name     string
		from, to Policy
	}{
		{"v1-to-v2", legacy, previous},
		{"v1-to-v3", legacy, current},
		{"v2-to-v3", previous, current},
	} {
		for _, class := range []string{"ordinary", "high-risk", "mechanical"} {
			t.Run(transition.name+"/"+class, func(t *testing.T) {
				old, err := transition.from.Select(class)
				if err != nil {
					t.Fatal(err)
				}
				migrated, err := MigrateSelection(old, transition.to)
				if err != nil {
					t.Fatal(err)
				}
				if migrated.Policy != transition.to || migrated.Class != old.Class || migrated.ReviewCycles != old.ReviewCycles || migrated.Hash == old.Hash {
					t.Fatalf("migrated=%+v old=%+v", migrated, old)
				}
				if _, err := MigrateSelection(migrated, transition.from); err == nil {
					t.Fatal("backward migration accepted")
				}
				old.ReviewCycles++
				if _, err := MigrateSelection(old, transition.to); err == nil {
					t.Fatal("tampered frozen repair cap accepted")
				}
			})
		}
	}
}

func TestVersionThreeRequiresTheAvailableProfileForEveryRole(t *testing.T) {
	policy := testPolicy(t)
	policy.Version = 3
	policy.Primary = Reviewer{"codex", "gpt-6.1-sol", "xhigh"}
	policy.Reviewer, policy.Fixer = policy.Primary, policy.Primary
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"primary", "reviewer", "fixer"} {
		for _, profile := range []Reviewer{{"claude", "gpt-6.1-sol", "xhigh"}, {"codex", "gpt-5.6-sol", "xhigh"}, {"codex", "gpt-6.1-sol", "high"}} {
			t.Run(role+"/"+profile.Harness+"/"+profile.Model+"/"+profile.Effort, func(t *testing.T) {
				changed := policy
				switch role {
				case "primary":
					changed.Primary = profile
				case "reviewer":
					changed.Reviewer = profile
				case "fixer":
					changed.Fixer = profile
				}
				if err := changed.Validate(); err == nil {
					t.Fatal("unapproved role profile accepted")
				}
			})
		}
	}
	selection, err := policy.Select("ordinary")
	if err != nil {
		t.Fatal(err)
	}
	if instruction := selection.Instruction("task", "snapshot"); !strings.Contains(instruction, "Codex gpt-6.1-sol xhigh") || strings.Contains(instruction, "Claude") {
		t.Fatalf("instruction does not name the frozen available profile: %s", instruction)
	}
}

func TestLoadSelectionAcceptsFrozenVersionTwoHash(t *testing.T) {
	const snapshot = `{
  "policy": {
    "version": 2,
    "primary": {"harness": "codex", "model": "gpt-5.6-sol", "effort": "high"},
    "reviewer": {"harness": "codex", "model": "gpt-5.6-sol", "effort": "high"},
    "fixer": {"harness": "codex", "model": "gpt-5.6-sol", "effort": "high"},
    "auto_fix": {"review": 0, "test": 1, "lint": 1, "rebase": 1, "ci": 1},
    "classes": {
      "ordinary": {"review_cycles": 2},
      "high-risk": {"review_cycles": 3},
      "mechanical": {"review_cycles": 2}
    }
  },
  "class": "high-risk",
  "review_cycles": 3,
  "policy_sha256": "f0c14764124b013193051b95407f2c4c72c052714798df2c51bb36f3167d980a"
}`
	path := filepath.Join(t.TempDir(), "pipeline.json")
	if err := os.WriteFile(path, []byte(snapshot), 0600); err != nil {
		t.Fatal(err)
	}
	selection, err := LoadSelection(path)
	if err != nil {
		t.Fatal(err)
	}
	if selection.Policy.Version != 2 || selection.Class != "high-risk" || selection.ReviewCycles != 3 {
		t.Fatalf("selection=%+v", selection)
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
		string(data) + `{}`, strings.Replace(string(data), `"version": 3`, `"version": 4`, 1),
		strings.Replace(string(data), `"version": 3`, `"typo": 3`, 1),
		strings.Replace(string(data), `"review_cycles": 2`, `"review_cycles": 10`, 1),
		strings.Replace(string(data), `"review": 0`, `"review": 10`, 1),
		strings.Replace(string(data), `"effort": "xhigh"`, `"effort": "low"`, 1),
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
