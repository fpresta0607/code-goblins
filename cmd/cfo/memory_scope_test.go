package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/harness"
)

const harborRulings = "## 2026-10-01 Releases\n\nThe release needs approval.\n\n## 2026-10-02 Ports\n\nHarbor keeps port 4300 free.\nHarbor closes at dusk.\n"

// scopeHome is the CEO's home with three rulings, an index and the fact it
// links, and no catalog yet. It returns the home's data folder.
func scopeHome(t *testing.T) string {
	t.Helper()
	_, dataDir := memoryHome(t)
	t.Setenv(harness.RoleVariable, "")
	t.Setenv(gateAgentVariable, "")
	for name, content := range map[string]string{
		"overlord.md":      harborRulings,
		"memory/MEMORY.md": "- [Cache](cache.md) - where the cache lives <!--a:2026-10-30-->\n",
		"memory/cache.md":  "---\nname: cache\ndescription: The cache lives on the second drive\nmetadata:\n  type: project\n---\n\nUse the second drive.\n",
	} {
		if err := os.WriteFile(filepath.Join(dataDir, filepath.FromSlash(name)), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dataDir
}

// memoryCommand runs cfo memory as the CEO's own session does.
func memoryCommand(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	exit := runMemory(args, &stdout, &stderr)
	return exit, stdout.String(), stderr.String()
}

func TestMemorySyncThenScopeTakesAProjectsRulingsOutOfStartupAndBringsThemByScope(t *testing.T) {
	// Arrange
	dataDir := scopeHome(t)
	exit, synced, stderr := memoryCommand("sync")
	if exit != 0 || !strings.Contains(synced, "The catalog lists 5 entries: 4 for startup and 1 by scope.\nNew to it: 5.") || !strings.Contains(synced, "of a budget of 15000.") {
		t.Fatalf("sync exit=%d stdout=%q stderr=%q", exit, synced, stderr)
	}
	exit, listed, stderr := memoryCommand("scope", "--list")
	ids := regexp.MustCompile(`\[(ruling-[0-9a-f]{12})\]`).FindAllStringSubmatch(listed, -1)
	if exit != 0 || len(ids) != 3 || !strings.Contains(listed, "] Harbor closes at dusk.\n") {
		t.Fatalf("list exit=%d stdout=%q stderr=%q", exit, listed, stderr)
	}
	release, ports, dusk := ids[0][1], ids[1][1], ids[2][1]
	// One id stands in the brackets the map prints it in, one before its words.
	idsFile := filepath.Join(t.TempDir(), "harbor.txt")
	if err := os.WriteFile(idsFile, []byte("["+ports+"]\n\n"+dusk+" Harbor closes at dusk.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	exit, tagged, stderr := memoryCommand("scope", "--project", "harbor", "--ids", idsFile)

	// Assert
	if exit != 0 || !strings.Contains(tagged, "Tagged 2.\nThe catalog lists 5 entries: 2 for startup and 3 by scope.\nStartup memory is ") {
		t.Fatalf("scope exit=%d stdout=%q stderr=%q", exit, tagged, stderr)
	}
	rulings, err := os.ReadFile(filepath.Join(dataDir, "overlord.md"))
	if err != nil || string(rulings) != harborRulings {
		t.Fatalf("the rulings were rewritten: %q, %v", rulings, err)
	}
	exit, startup, _ := runCommand("session-start")
	if exit != 0 || !strings.Contains(startup, "The release needs approval.") || strings.Contains(startup, "Harbor keeps port 4300 free.") || !strings.Contains(startup, "project harbor: rulings 2\n") {
		t.Fatalf("startup does not load the fleet's ruling alone and count the project's: exit=%d %s", exit, startup)
	}
	exit, scoped, stderr := memoryCommand("retrieve", "--project", "harbor", "--cold")
	want := "\noverlord.md\n[" + ports + "]\n## 2026-10-02 Ports\n\nHarbor keeps port 4300 free.\n[" + dusk + "] Harbor closes at dusk.\n"
	if exit != 0 || scoped != want {
		t.Fatalf("retrieve --cold exit=%d stdout=%q stderr=%q, want %q", exit, scoped, stderr, want)
	}
	exit, exact, stderr := memoryCommand("retrieve", "--id", dusk)
	if exit != 0 || !strings.Contains(exact, " authority=ruling tier=pinned ") || !strings.HasSuffix(exact, "\nHarbor closes at dusk.\n\n") {
		t.Fatalf("retrieve --id exit=%d stdout=%q stderr=%q", exit, exact, stderr)
	}
	exit, fleet, stderr := memoryCommand("scope", "--fleet", release)
	if exit != 0 || !strings.HasPrefix(fleet, "Tagged 1.\n") {
		t.Fatalf("scope --fleet exit=%d stdout=%q stderr=%q", exit, fleet, stderr)
	}
	if exit, listed, _ := memoryCommand("scope", "--list"); exit != 0 || listed != "Every ruling has a scope.\n" {
		t.Fatalf("a classified home still lists rulings: exit=%d %q", exit, listed)
	}
}

func TestMemoryRetrieveSaysToSyncWhenMemoryChangedAfterTheCatalog(t *testing.T) {
	// Arrange
	dataDir := scopeHome(t)
	if exit, _, stderr := memoryCommand("sync"); exit != 0 {
		t.Fatal(stderr)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "overlord.md"), []byte(harborRulings+"Harbor opens at dawn.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	exit, _, stderr := memoryCommand("retrieve")

	// Assert
	if exit != 1 || !strings.Contains(stderr, "MEMORY UNREADABLE: memory source overlord.md is corrupt or changed") || !strings.Contains(stderr, "cfo memory sync derives it again") {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	if exit, synced, stderr := memoryCommand("sync"); exit != 0 || !strings.Contains(synced, "New to it: 1.") {
		t.Fatalf("sync after the edit: exit=%d stdout=%q stderr=%q", exit, synced, stderr)
	}
	if exit, startup, stderr := memoryCommand("retrieve"); exit != 0 || !strings.Contains(startup, "Harbor opens at dawn.") {
		t.Fatalf("the new ruling does not load: exit=%d stdout=%q stderr=%q", exit, startup, stderr)
	}
}

func TestMemorySyncAndScopeRefuseWhatTheyCannotDo(t *testing.T) {
	for name, test := range map[string]struct {
		env  map[string]string
		args []string
		exit int
		want string
	}{
		"a goblin syncing":            {map[string]string{harness.RoleVariable: harness.RoleGoblin}, []string{"sync"}, 2, "the CEO's to write, never a goblin's or a gate agent's"},
		"a gate agent tagging":        {map[string]string{gateAgentVariable: "1"}, []string{"scope", "--fleet", "ruling-000000000000"}, 2, "the CEO's to write, never a goblin's or a gate agent's"},
		"an argument to sync":         {nil, []string{"sync", "now"}, 2, "takes no argument"},
		"a tag with no id":            {nil, []string{"scope", "--project", "harbor"}, 1, "name each entry to tag by its id"},
		"a tag with no scope":         {nil, []string{"scope", "ruling-000000000000"}, 1, "name a project, the whole fleet or an expiry"},
		"both scopes at once":         {nil, []string{"scope", "--fleet", "--project", "harbor", "ruling-000000000000"}, 1, "never both"},
		"an id no entry has":          {nil, []string{"scope", "--project", "harbor", "ruling-000000000000"}, 1, "no memory entry has the id ruling-000000000000"},
		"a list that also tags":       {nil, []string{"scope", "--list", "--fleet"}, 2, "--list only lists"},
		"an ids file that is missing": {nil, []string{"scope", "--fleet", "--ids", filepath.Join("no", "such", "file")}, 1, "file"},
		"cold with no scope":          {nil, []string{"retrieve", "--cold"}, 2, "--cold prints what a scope or a search selects"},
		"cold with an id":             {nil, []string{"retrieve", "--cold", "--id", "cache"}, 2, "--cold prints what a scope or a search selects"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dataDir := scopeHome(t)
			for key, value := range test.env {
				t.Setenv(key, value)
			}

			// Act
			exit, _, stderr := memoryCommand(test.args...)

			// Assert
			if exit != test.exit || !strings.Contains(stderr, test.want) {
				t.Fatalf("exit=%d stderr=%q, want %d and %q", exit, stderr, test.exit, test.want)
			}
			if _, err := os.Stat(filepath.Join(dataDir, "memory", "startup.json")); !os.IsNotExist(err) {
				t.Fatalf("a refused command left a catalog: %v", err)
			}
		})
	}
}
