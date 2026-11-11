package codegoblins

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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

func TestTheBinaryCarriesDiscoverableSmartRouting(t *testing.T) {
	// Act
	skill := shipped(t, "smart-routing")
	header, _, isClosed := strings.Cut(strings.TrimPrefix(skill, "---\n"), "\n---\n")

	// Assert
	if !strings.HasPrefix(skill, "---\n") || !isClosed || !strings.Contains(header, "name: smart-routing\n") {
		t.Fatalf("smart-routing has no discovery header: %s", header)
	}
	if !regexp.MustCompile(`(?m)^description: Use when .+`).MatchString(header) {
		t.Fatalf("smart-routing has no discovery trigger: %s", header)
	}
	contract, err := fs.ReadFile(Contract, "AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	startup, _, _ := strings.Cut(string(contract), "## Prime directives")
	if !strings.Contains(startup, "`smart-routing`") {
		t.Fatal("the shared startup contract does not discover smart-routing before its size limit")
	}
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
	if !strings.Contains(skill, "goblins project check") {
		t.Error("the skill does not send its reader to goblins project check, where the checks live")
	}
}

// oneHarnessOnly are the words that belong to one harness: its name, its
// folders and the names of its own tools. A skill that uses one reads
// differently, or not at all, in the other two.
var oneHarnessOnly = regexp.MustCompile(`(?i)\b(claude|codex|pi|anthropic|openai|askuserquestion|todowrite|apply_patch|subagents?|slash commands?|(bash|read|edit|write|grep|glob|task|shell) tool)\b|~[/\\]\.(claude|codex|pi)\b|/project-check\b`)

// The skill is one text for every harness: it sends its reader to goblins
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

// Issue #1936: a terminal can hold a credential its brief and its task's
// record do not name, after a revoke while it runs or from an older build's
// spawn. A rule that lists where credentials come from then clears a test
// that reaches production, so the skill decides by the variable itself.
func TestTheProjectCheckSkillDecidesWhatItsTerminalCarriesByTheVariable(t *testing.T) {
	// Arrange
	skill := shipped(t, "project-check")
	const opening = "   - Run no test while the report holds a `terminal-reaches-production` line"
	_, after, isFound := strings.Cut(skill, opening)
	if !isFound {
		t.Fatal("the skill has no rule against running a test under a terminal-reaches-production line")
	}

	// Act
	rule, _, _ := strings.Cut(after, "\n   - ")

	// Assert
	for _, want := range []string{
		"is set in your terminal's environment now",
		"whatever your brief or your task's record says",
		"by whether it is set, never by printing its value",
	} {
		if !strings.Contains(rule, want) {
			t.Errorf("the rule does not say %q:\n%s", want, rule)
		}
	}
	if strings.Contains(rule, "carries the services") {
		t.Errorf("the rule says which services the terminal carries by where they come from, which a revoke or an older build's spawn makes wrong:\n%s", rule)
	}
}

// The Overlord, 2026-10-14: every browser and computer-use automation that
// reaches a step only he can take sends him one card and picks up the moment
// it closes. The skill a goblin loads for that work says so, in one text for
// every harness, and is offered by a header that names when to use it.
func TestTheBinaryCarriesTheStepCardSkillForBrowserWork(t *testing.T) {
	// Act
	skill := shipped(t, "step-card")

	// Assert
	header, _, isClosed := strings.Cut(strings.TrimPrefix(skill, "---\n"), "\n---\n")
	if !strings.HasPrefix(skill, "---\n") || !isClosed || !strings.Contains(header, "name: step-card\n") {
		t.Fatalf("the step-card skill has no header naming it:\n%s", header)
	}
	if !regexp.MustCompile(`(?m)^description: \S.{80,}$`).MatchString(header) || !strings.Contains(header, "browser") {
		t.Errorf("the header has no description saying when to use the skill:\n%s", header)
	}
	for _, want := range []string{"goblins auth request", "--wait", "--plain", "--label", "--window", "--done", "--withdraw", "never take", "in the background"} {
		if !strings.Contains(skill, want) {
			t.Errorf("the skill does not say %q", want)
		}
	}
	for number, line := range strings.Split(skill, "\n") {
		if found := oneHarnessOnly.FindString(line); found != "" {
			t.Errorf("line %d names %q, which belongs to one harness: %s", number+1, found, line)
		}
	}
}

// Issue #2017: a goblin asked to rebuild or document an existing site is
// offered the reverse-engineer-site skill, which a harness lists by the name
// and description it reads from the skill's YAML header.
func TestTheBinaryCarriesTheReverseEngineerSiteSkillWithItsHeader(t *testing.T) {
	// Act
	skill := shipped(t, "reverse-engineer-site")

	// Assert
	header, _, isClosed := strings.Cut(strings.TrimPrefix(skill, "---\n"), "\n---\n")
	if !strings.HasPrefix(skill, "---\n") || !isClosed {
		t.Fatalf("the reverse-engineer-site skill has no header between --- lines:\n%s", skill)
	}
	var fields struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(header), &fields); err != nil {
		t.Fatalf("the header is not YAML a harness can read: %v\n%s", err, header)
	}
	if fields.Name != "reverse-engineer-site" || strings.TrimSpace(fields.Description) == "" {
		t.Errorf("the header reads as name %q and description %q, want the name reverse-engineer-site and a description", fields.Name, fields.Description)
	}
}
