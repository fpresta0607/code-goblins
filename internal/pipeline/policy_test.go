package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
		string(data) + `{}`, strings.Replace(string(data), `"version": 1`, `"version": 2`, 1),
		strings.Replace(string(data), `"version": 1`, `"typo": 1`, 1),
		strings.Replace(string(data), `"review_cycles": 2`, `"review_cycles": 10`, 1),
		strings.Replace(string(data), `"review": 0`, `"review": 10`, 1),
		strings.Replace(string(data), `"effort": "high"`, `"effort": "low"`, 1),
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
