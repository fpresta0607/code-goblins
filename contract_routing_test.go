package codegoblins

import (
	"io/fs"
	"path"
	"strings"
	"testing"
)

func TestSharedCoreFitsTheSmallestHarnessAndRoutesItsProcedures(t *testing.T) {
	// Arrange
	core, err := fs.ReadFile(Contract, "AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	if len(core) >= 30*1024 {
		t.Errorf("AGENTS.md is %d bytes; keep the complete shared core below 30 KiB", len(core))
	}
	for _, topic := range []string{"dispatch", "authentication", "worktrees", "services", "supervision", "delivery", "decisions", "afk", "home", "root-issues"} {
		name := "docs/contract/" + topic + ".md"
		if !strings.Contains(string(core), "]("+name+")") {
			t.Errorf("the core has no discoverable route to %s", topic)
		}
		if _, err := fs.Stat(Contract, name); err != nil {
			t.Errorf("the binary does not ship %s: %v", name, err)
		}
		skill := shipped(t, "cfo-"+topic)
		if !strings.Contains(skill, "cfo help contract "+topic) || !strings.Contains(skill, "description: Use when") {
			t.Errorf("cfo-%s has no precise discovery trigger and command reference route", topic)
		}
	}
}

func TestEveryExtractedSectionHasAnInventoryOwner(t *testing.T) {
	// Arrange
	inventory, err := fs.ReadFile(Contract, "docs/contract/ownership.md")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(Contract, "docs/contract")
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	sections := 0
	for _, entry := range entries {
		if entry.Name() == "ownership.md" {
			continue
		}
		body, err := fs.ReadFile(Contract, path.Join("docs/contract", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, heading := range markdownHeading.FindAllStringSubmatch(string(body), -1) {
			sections++
			slug := strings.ReplaceAll(headingPunctuation.ReplaceAllString(strings.ToLower(strings.TrimSpace(heading[1])), ""), " ", "-")
			if !strings.Contains(string(inventory), "]("+entry.Name()+"#"+slug+")") {
				t.Errorf("section %q in %s has no inventory owner", heading[1], entry.Name())
			}
		}
	}
	if sections != 33 {
		t.Errorf("the extraction owns %d conditional sections, want all 33 previous sections", sections)
	}
}

func TestRoutedContractKeepsAuthorityAndCustodyBoundaries(t *testing.T) {
	for name, boundaries := range map[string][]string{
		"AGENTS.md":                       {"Never merge without the Supreme Overlord's explicit word", "Never tear down unlanded work", "CFO_ROLE=goblin", "nobody writes or runs a malware test sample"},
		"docs/contract/authentication.md": {"A request stores a value.", "It grants no service", "not a sandbox", "never put one in a brief"},
		"docs/contract/dispatch.md":       {"two cycles for ordinary and mechanical, three for high-risk", "No usable candidate means no dispatch"},
		"docs/contract/worktrees.md":      {"only once the Overlord has trusted it", "A `.venv` or a `node_modules` is never shared"},
		"docs/contract/supervision.md":    {"WAKE_ACK_REQUIRED", "range-scoped, not per-record", "no single variable is trusted alone", "custody.audit"},
		"docs/contract/delivery.md":       {"frozen policy", "exhausted review budget", "hold` label", "never merge red or divergent work"},
		"docs/contract/afk.md":            {"Never switch it on your own judgment", "Never publish anything as a `cfo question`", "AFK MODE: UNREADABLE"},
	} {
		// Arrange
		body, err := fs.ReadFile(Contract, name)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ToLower(strings.Join(strings.Fields(string(body)), " "))

		// Assert
		for _, boundary := range boundaries {
			if !strings.Contains(text, strings.ToLower(boundary)) {
				t.Errorf("%s no longer preserves %q", name, boundary)
			}
		}
	}
}
