package codegoblins

import (
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSharedCoreFitsTheSmallestHarnessAndRoutesItsProcedures(t *testing.T) {
	core, err := fs.ReadFile(Contract, "AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(core) >= 30*1024 {
		t.Errorf("AGENTS.md is %d bytes; keep the complete shared core below 30 KiB", len(core))
	}
	coreRoutes := localReferences("AGENTS.md", core)
	for _, topic := range []string{"dispatch", "authentication", "worktrees", "services", "supervision", "delivery", "decisions", "afk", "home", "root-issues"} {
		t.Run(topic, func(t *testing.T) {
			name := "docs/contract/" + topic + ".md"
			if !slices.Contains(coreRoutes, contractReference{file: name}) {
				t.Errorf("the core has no discoverable route to %s", topic)
			}
			if _, err := fs.Stat(Contract, name); err != nil {
				t.Errorf("the binary does not ship %s: %v", name, err)
			}
			skill := shipped(t, "cfo-"+topic)
			header, _, isClosed := strings.Cut(strings.TrimPrefix(skill, "---\n"), "\n---\n")
			if !strings.HasPrefix(skill, "---\n") || !isClosed {
				t.Fatal("skill discovery metadata is missing or unclosed")
			}
			var metadata struct {
				Name        string `yaml:"name"`
				Description string `yaml:"description"`
			}
			if err := yaml.Unmarshal([]byte(header), &metadata); err != nil {
				t.Fatal(err)
			}
			if metadata.Name != "cfo-"+topic || strings.TrimSpace(metadata.Description) == "" {
				t.Errorf("invalid skill discovery metadata: %+v", metadata)
			}
		})
	}
}

func TestEveryExtractedSectionHasAnInventoryOwner(t *testing.T) {
	inventory, err := fs.ReadFile(Contract, "docs/contract/ownership.md")
	if err != nil {
		t.Fatal(err)
	}
	owners := map[string]contractReference{}
	isSectionInventory := false
	for _, line := range strings.Split(string(inventory), "\n") {
		if strings.HasPrefix(line, "## ") {
			isSectionInventory = strings.TrimSpace(line[3:]) == "Section inventory"
		}
		if !isSectionInventory || !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|\r"), "|")
		if len(cells) != 2 {
			t.Fatalf("invalid section inventory row: %s", line)
		}
		references := localReferences("docs/contract/ownership.md", []byte(cells[1]))
		if len(references) == 0 {
			continue
		}
		section := strings.TrimSpace(cells[0])
		if _, exists := owners[section]; exists || len(references) != 1 {
			t.Fatalf("section %q must have exactly one owner", section)
		}
		owners[section] = references[0]
	}

	want := map[string]contractReference{}
	for file, sections := range map[string][]string{
		"AGENTS.md":                       {"Prime directives", "The loop: ask away → done", "Secondmates", "Escalation", "Cut from this build", "Microsoft Defender", "Memory", "Restart is a non-event"},
		"docs/contract/dispatch.md":       {"Dispatching", "Heavy local runs", "Helper goblins", "Goblin names", "Naming a project", "The fleet never idles", "Pausing and automatic resume", "Switching a running goblin", "Dispatch policy", "Model routing"},
		"docs/contract/authentication.md": {"Project authentication", "Credentials by need", "The credential store", "Fetching keys with the browser", "A browser step only he can take", "Status words are earned"},
		"docs/contract/worktrees.md":      {"Project worktree environment", "Share caches, never share materialized environments", "A release a week old, never a fresher one", "Code that runs at every Python start"},
		"docs/contract/services.md":       {"Project local services"},
		"docs/contract/delivery.md":       {"Delivery", "What CI runs", "The board's render budget", "Merge trains"},
		"docs/contract/supervision.md":    {"Supervision", "The CFO's own session"},
		"docs/contract/decisions.md":      {"Reporting surface", "User decisions on the board"},
		"docs/contract/afk.md":            {"AFK mode"},
		"docs/contract/root-issues.md":    {"Root issues"},
		"docs/contract/home.md":           {"The CFO home", "The janitor"},
	} {
		for _, section := range sections {
			want[section] = contractReference{file, headingAnchor(section)}
		}
	}
	if !maps.Equal(owners, want) {
		t.Errorf("section ownership differs from the pre-extraction inventory:\ngot %v\nwant %v", owners, want)
	}
	for section, owner := range want {
		body, err := fs.ReadFile(Contract, owner.file)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(headingAnchors(body), owner.anchor) {
			t.Errorf("section %q has no destination at %s#%s", section, owner.file, owner.anchor)
		}
	}
	entries, err := fs.ReadDir(Contract, "docs/contract")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == "ownership.md" {
			continue
		}
		file := path.Join("docs/contract", entry.Name())
		body, err := fs.ReadFile(Contract, file)
		if err != nil {
			t.Fatal(err)
		}
		for _, heading := range markdownHeading.FindAllStringSubmatch(string(body), -1) {
			section := strings.TrimSpace(heading[1])
			if owners[section] != (contractReference{file, headingAnchor(section)}) {
				t.Errorf("unlisted section %q in %s", section, file)
			}
		}
	}
}
