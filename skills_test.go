package codegoblins

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// shipped reads a skill the binary carries, which is what an install puts in
// the shared skills folder every harness reads.
func shipped(t *testing.T, name string) string {
	t.Helper()
	data, err := fs.ReadFile(Skills, ".agents/skills/"+name+"/SKILL.md")
	if err != nil {
		t.Fatalf("the binary does not carry the %s skill: %v", name, err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

// A harness lists a skill by the name and description in its header, so a
// skill without them is installed and never offered.
func TestTheBinaryCarriesTheProjectCheckSkillWithItsHeader(t *testing.T) {
	// Act
	skill := shipped(t, "project-check")

	// Assert
	header, _, isClosed := strings.Cut(strings.TrimPrefix(skill, "---\n"), "\n---\n")
	if !strings.HasPrefix(skill, "---\n") || !isClosed {
		t.Fatal("the project-check skill has no header between --- lines")
	}
	if !strings.Contains(header, "name: project-check\n") {
		t.Errorf("the header does not name the skill project-check:\n%s", header)
	}
	if !regexp.MustCompile(`(?m)^description: \S.{80,}$`).MatchString(header) {
		t.Errorf("the header has no description saying when to use the skill:\n%s", header)
	}
	if !strings.Contains(skill, "cfo project check") {
		t.Error("the skill does not send its reader to cfo project check, where the checks live")
	}
}

// oneHarnessOnly are the words that belong to one harness: its name, its
// folders and the names of its own tools. A skill that uses one reads
// differently, or not at all, in the other two.
var oneHarnessOnly = regexp.MustCompile(`(?i)\b(claude|codex|pi|anthropic|openai|askuserquestion|todowrite|apply_patch|subagents?|slash commands?|(bash|read|edit|write|grep|glob|task|shell) tool)\b|~[/\\]\.(claude|codex|pi)\b|/project-check\b`)

// The skill is one text for every harness: it sends its reader to cfo
// commands and names no tool, folder or product of a single harness.
func TestTheProjectCheckSkillNamesNoToolOfOneHarness(t *testing.T) {
	// Act
	skill := shipped(t, "project-check")

	// Assert
	lines := strings.Split(skill, "\n")
	if len(lines) < 40 {
		t.Fatalf("the skill has %d lines, so this test read next to nothing", len(lines))
	}
	for number, line := range lines {
		if found := oneHarnessOnly.FindString(line); found != "" {
			t.Errorf("line %d names %q, which belongs to one harness: %s", number+1, found, line)
		}
	}
}
