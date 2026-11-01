package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

var referenceDate = time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)

func catalogFixture(t *testing.T) (string, Catalog) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "memory"), 0o700); err != nil {
		t.Fatal(err)
	}
	catalog := Catalog{Version: 1}
	for _, fact := range []struct{ path, id, text, authority, location, topic string }{
		{"overlord.md", "authority", "The release needs approval.\n", "ruling", "warm", ""},
		{"memory/current.md", "current", "Use the local cache. <!--a:2026-10-30-->\n", "fact", "warm", ""},
		{"memory/project.md", "project", "A project uses port 4300. <!--a:2026-10-30-->\n", "project", "cold", "ports"},
		{"memory/history.md", "history", "A completed investigation.\n", "history", "cold", "history"},
	} {
		writeMemoryFile(t, dir, fact.path, []byte(fact.text))
		entry := Entry{ID: fact.id, End: len(fact.text), SHA256: Hash([]byte(fact.text)), Authority: fact.authority, Location: fact.location}
		if fact.topic != "" {
			entry.Trigger, entry.Topics = fact.topic, []string{fact.topic}
		}
		if fact.authority == "history" {
			entry.Approval = &Approval{By: "Overlord", Quote: "Offload history to its named owner.", Date: "2026-10-30", Destination: fact.path + "#history"}
		}
		catalog.Sources = append(catalog.Sources, Source{Path: fact.path, SHA256: Hash([]byte(fact.text)), Entries: []Entry{entry}})
	}
	writeCatalog(t, dir, catalog)
	return dir, catalog
}

func writeMemoryFile(t *testing.T, root, name string, content []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeCatalog(t *testing.T, dir string, catalog Catalog) {
	t.Helper()
	content, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	writeMemoryFile(t, dir, CatalogPath, content)
}

func TestRetrievalSelectsRelevantColdFactsAndPreservesRulings(t *testing.T) {
	// Oracle: upstream Stow's exact-word authority and conditional offload contract.
	dir, _ := catalogFixture(t)
	catalog, err := Load(dir, referenceDate)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name            string
		query           Query
		present, absent string
	}{
		{"startup", Query{}, "The release needs approval.", "port 4300"},
		{"relevant topic", Query{Topic: "ports"}, "port 4300", "completed investigation"},
		{"exact history", Query{ID: "history"}, "completed investigation", "release needs approval"},
	} {
		t.Run(test.name, func(t *testing.T) {
			output, err := catalog.Retrieve(test.query, referenceDate)
			if err != nil || !strings.Contains(output, test.present) || strings.Contains(output, test.absent) {
				t.Fatalf("retrieval = %q, %v", output, err)
			}
		})
	}
	if _, err := catalog.Retrieve(Query{ID: "missing"}, referenceDate); err == nil {
		t.Fatal("missing named authority was reported as success")
	}
}

func TestCatalogRejectsCorruptMissingOrUnapprovedAuthority(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Catalog)
	}{
		{"checksum", func(c *Catalog) { c.Sources[0].SHA256 = strings.Repeat("0", 64) }},
		{"range checksum", func(c *Catalog) { c.Sources[0].Entries[0].SHA256 = strings.Repeat("0", 64) }},
		{"gap", func(c *Catalog) { c.Sources[0].Entries[0].Start = 1 }},
		{"missing source", func(c *Catalog) { c.Sources = c.Sources[1:] }},
		{"duplicate source", func(c *Catalog) { c.Sources = append(c.Sources, c.Sources[0]) }},
		{"duplicate id", func(c *Catalog) { c.Sources[1].Entries[0].ID = "authority" }},
		{"unapproved pin", func(c *Catalog) { c.Sources[3].Entries[0].Approval = nil }},
		{"broad approval", func(c *Catalog) { c.Sources[3].Entries[0].Approval.Quote = "Approve every move." }},
		{"not live", func(c *Catalog) { c.Sources[3].Entries[0].Approval.Destination = "a future skill" }},
		{"unknown supersession", func(c *Catalog) { c.Sources[1].Entries[0].Supersedes = []string{"missing"} }},
		{"layer conflict", func(c *Catalog) {
			c.Layers = []Layer{{Name: "one", Classes: []string{"facts", "facts"}, Owner: "CFO", Capture: "owner", Correction: "owner"}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir, catalog := catalogFixture(t)
			test.change(&catalog)
			writeCatalog(t, dir, catalog)
			if _, err := Load(dir, referenceDate); err == nil {
				t.Fatal("invalid authority catalog was accepted")
			}
		})
	}
}

func TestCatalogRejectsDuplicateJSONAndCyclicSupersession(t *testing.T) {
	for _, test := range []string{"duplicate json", "cyclic supersession", "nested memory"} {
		t.Run(test, func(t *testing.T) {
			dir, catalog := catalogFixture(t)
			switch test {
			case "duplicate json":
				writeMemoryFile(t, dir, CatalogPath, []byte(`{"version":2,"version":1,"sources":[]}`))
				var value Catalog
				if err := Decode([]byte(`{"version":2,"version":1}`), &value); err == nil {
					t.Fatal("duplicate JSON keys can replace an authority classification")
				}
				return
			case "cyclic supersession":
				catalog.Sources[0].Entries[0].Supersedes = []string{"current"}
				catalog.Sources[1].Entries[0].Supersedes = []string{"authority"}
				writeCatalog(t, dir, catalog)
			case "nested memory":
				writeMemoryFile(t, dir, "memory/cold/unaccounted.md", []byte("unaccounted authority"))
			}
			if _, err := Load(dir, referenceDate); err == nil {
				t.Fatal("conflicting or unaccounted authority was accepted")
			}
		})
	}
}

func TestTierClocksAndGraceFollowReviewedStowContract(t *testing.T) {
	// The pinned exemption, 30-day aging and 7-day perishable boundaries are
	// independent constants from First Mate's reviewed Stow contract.
	for _, test := range []struct {
		name, text, authority, tier string
		stale, grace, legacy        bool
	}{
		{"unmarked ruling", "Keep these exact words.", "ruling", "pinned", false, false, false},
		{"legacy fact", "A fact", "fact", "aging", false, false, true},
		{"grace", "A fact <!--g-->", "fact", "aging", false, true, false},
		{"29 days", "A fact <!--a:2026-10-02-->", "fact", "aging", false, false, false},
		{"30 days", "A fact <!--a:2026-10-01-->", "fact", "aging", true, false, false},
		{"6 days", "A fact <!--p:2026-10-25-->", "fact", "perishable", false, false, false},
		{"7 days", "A fact <!--p:2026-10-24-->", "fact", "perishable", true, false, false},
		{"pin", "A fact <!--P-->", "fact", "pinned", false, false, false},
		{"aging history", "An incident <!--a:2026-10-30-->", "history", "aging", false, false, false},
		{"aging ruling", "A ruling <!--a:2026-10-30-->", "ruling", "aging", false, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := []byte(test.text)
			status, err := Inspect(before, test.authority, referenceDate)
			if err != nil || status.Tier != test.tier || status.IsStale != test.stale || status.IsGrace != test.grace || status.IsLegacy != test.legacy {
				t.Fatalf("status = %+v, %v", status, err)
			}
			if !reflect.DeepEqual(before, []byte(test.text)) {
				t.Fatal("retrieval advanced the decay clock or changed bytes")
			}
		})
	}
	for _, text := range []string{"<!--a:2026-02-30-->", "<!--a:2026-11-01-->", "<!--a:bad-->", "<!--P--><!--g-->", "<!--a:2026-10-30/100-->", "<!--p:2026-10-30/2-->"} {
		if _, err := Inspect([]byte(text), "fact", referenceDate); err == nil {
			t.Fatalf("accepted corrupt markers %q", text)
		}
	}
}

func TestPinnedOffloadRequiresASeparatelyDelimitedExactID(t *testing.T) {
	for _, test := range []struct {
		quote      string
		isApproved bool
	}{
		{"Offload rule-10 only", false},
		{"Offload prefix-rule-1 only", false},
		{"Offload rule-1-extra only", false},
		{"Offload rule-1.other only", false},
		{"Offload rule-1/path only", false},
		{"Offload team:rule-1 only", false},
		{"Offload `rule-1` only.", true},
		{"Offload rule-1.", true},
		{"Offload (rule-1), retaining its source.", true},
	} {
		t.Run(test.quote, func(t *testing.T) {
			dir, catalog := catalogFixture(t)
			entry := &catalog.Sources[3].Entries[0]
			entry.ID = "rule-1"
			entry.Approval.Quote = test.quote
			entry.Approval.Destination = "memory/history.md#rule-1"
			writeCatalog(t, dir, catalog)

			_, err := Load(dir, referenceDate)

			if (err == nil) != test.isApproved {
				t.Fatalf("approval accepted=%t, want %t: %v", err == nil, test.isApproved, err)
			}
		})
	}
}

func TestCatalogIDsUseAnUnambiguousApprovalGrammar(t *testing.T) {
	for _, id := range []string{"team:rule-1", "rule-1.", "rule.1", "-rule", "rule\u2003one"} {
		t.Run(id, func(t *testing.T) {
			dir, catalog := catalogFixture(t)
			entry := &catalog.Sources[3].Entries[0]
			entry.ID = id
			entry.Approval.Quote = "Offload " + id + " only"
			entry.Approval.Destination = "memory/history.md#" + id
			writeCatalog(t, dir, catalog)

			if _, err := Load(dir, referenceDate); err == nil {
				t.Fatal("ambiguous identifier was accepted as an approval target")
			}
		})
	}
}

func TestTierAnnotationsLeaveLiteralExamplesAsSourceBytes(t *testing.T) {
	for _, text := range []string{
		"Show `<!--a:2026-10-30-->` in examples.",
		"Show `<!--a:bad-->` in examples.",
		"Keep `<!--P-->` as an example.",
		"Keep `<!--t:team-->` as an example.",
		"Example:\n```text\n<!--a:2026-10-30-->\n```\n",
	} {
		t.Run(text, func(t *testing.T) {
			status, err := Inspect([]byte(text), "ruling", referenceDate)
			if err != nil || status.Tier != "pinned" || status.Reinforced != "" || status.PendingCapture != "" {
				t.Fatalf("literal example changed ruling metadata: %+v, %v", status, err)
			}
			dir := t.TempDir()
			writeMemoryFile(t, dir, "memory/preferences.md", []byte(text))
			catalog := Catalog{Version: 1, Sources: []Source{{Path: "memory/preferences.md", SHA256: Hash([]byte(text)), Entries: []Entry{{ID: "preference", End: len(text), SHA256: Hash([]byte(text)), Authority: "ruling", Location: "warm", Trigger: "preferences", Topics: []string{"preferences"}}}}}}
			writeCatalog(t, dir, catalog)
			plan, err := PlanMigration(dir)
			if err != nil || !plan.Files[0].Moves[0].NeedsApproval || plan.Files[0].RetainedSHA256 != Hash([]byte(text)) {
				t.Fatalf("inventory reclassified or changed literal authority: %+v, %v", plan, err)
			}
			catalog.Sources[0].Entries[0].Location = "cold"
			writeCatalog(t, dir, catalog)
			if _, err := Load(dir, referenceDate); err == nil {
				t.Fatal("literal example allowed unapproved ruling offload")
			}
		})
	}
}

func TestWindowsIndexPinsFollowFilesystemIdentity(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case-varied existing paths share identity on Windows")
	}
	for _, text := range []string{"Keep the exact preference.\n", "A dated preference. <!--a:2026-10-30-->\n"} {
		t.Run(text, func(t *testing.T) {
			dir := t.TempDir()
			index := []byte("- [Preference](preferences.md) - owner choice <!--P-->\n")
			fact := []byte(text)
			writeMemoryFile(t, dir, "memory/MEMORY.md", index)
			writeMemoryFile(t, dir, "memory/Preferences.md", fact)
			catalog := Catalog{Version: 1, Sources: []Source{
				{Path: "memory/MEMORY.md", SHA256: Hash(index), Entries: []Entry{{ID: "index", End: len(index), SHA256: Hash(index), Authority: "fact", Location: "warm"}}},
				{Path: "memory/Preferences.md", SHA256: Hash(fact), Entries: []Entry{{ID: "preference", End: len(fact), SHA256: Hash(fact), Authority: "fact", Location: "warm", Trigger: "preferences", Topics: []string{"preferences"}}}},
			}}
			writeCatalog(t, dir, catalog)
			loaded, err := Load(dir, referenceDate)
			if strings.Contains(text, "<!--a:") {
				if err == nil {
					t.Fatal("case-varied index pin failed to reject a conflicting dated tier")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				output, err := loaded.Retrieve(Query{ID: "preference"}, referenceDate)
				if err != nil || !strings.Contains(output, "tier=pinned") {
					t.Fatalf("case-varied index pin was lost: %s, %v", output, err)
				}
				catalog.Sources[1].Entries[0].Location = "cold"
				writeCatalog(t, dir, catalog)
				if _, err := Load(dir, referenceDate); err == nil {
					t.Fatal("case-varied index pin allowed unapproved cold classification")
				}
			}
			if err := os.Remove(filepath.Join(dir, filepath.FromSlash(CatalogPath))); err != nil {
				t.Fatal(err)
			}
			plan, err := PlanMigration(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range plan.Files {
				if file.Path == "memory/Preferences.md" && (file.Moves[0].Tier != "pinned" || !file.Moves[0].NeedsApproval || file.RetainedSHA256 != Hash(fact)) {
					t.Fatalf("legacy inventory lost case-varied pin: %+v", file)
				}
			}
		})
	}
}

func TestIndexPinsResolveMarkdownDestinations(t *testing.T) {
	for _, test := range []struct{ destination, source string }{
		{"preferences.md#policy", "preferences.md"},
		{"preferences%20file.md#policy", "preferences file.md"},
		{"<preferences file.md#policy>", "preferences file.md"},
		{"<nested/preferences%20file.md#policy>", "nested/preferences file.md"},
		{"preferences%23policy.md#section", "preferences#policy.md"},
		{"preferences.md#policy \"Policy title\"", "preferences.md"},
		{"<preferences file.md#policy> 'Policy title'", "preferences file.md"},
	} {
		t.Run(test.destination, func(t *testing.T) {
			dir := t.TempDir()
			index := []byte("- [Preference](" + test.destination + ") - owner choice <!--P-->\n")
			fact := []byte("Keep the exact preference.\n")
			source := "memory/" + test.source
			writeMemoryFile(t, dir, "memory/MEMORY.md", index)
			writeMemoryFile(t, dir, source, fact)
			catalog := Catalog{Version: 1, Sources: []Source{
				{Path: "memory/MEMORY.md", SHA256: Hash(index), Entries: []Entry{{ID: "index", End: len(index), SHA256: Hash(index), Authority: "fact", Location: "warm"}}},
				{Path: source, SHA256: Hash(fact), Entries: []Entry{{ID: "preference", End: len(fact), SHA256: Hash(fact), Authority: "fact", Location: "warm", Trigger: "preferences", Topics: []string{"preferences"}}}},
			}}
			for _, hasCatalog := range []bool{false, true} {
				if hasCatalog {
					writeCatalog(t, dir, catalog)
					loaded, err := Load(dir, referenceDate)
					if err != nil {
						t.Fatal(err)
					}
					text, err := loaded.Retrieve(Query{ID: "preference"}, referenceDate)
					if err != nil || !strings.Contains(text, "tier=pinned") || !strings.Contains(text, string(fact)) {
						t.Fatalf("resolved index link lost its pin or bytes: %s, %v", text, err)
					}
				}
				plan, err := PlanMigration(dir)
				if err != nil {
					t.Fatal(err)
				}
				for _, file := range plan.Files {
					if file.Path == source && (file.Moves[0].Tier != "pinned" || !file.Moves[0].NeedsApproval || file.RetainedSHA256 != Hash(fact)) {
						t.Fatalf("inventory lost the resolved index pin: %+v", file)
					}
				}
			}
			entry := &catalog.Sources[1].Entries[0]
			entry.Location = "cold"
			writeCatalog(t, dir, catalog)
			if _, err := Load(dir, referenceDate); err == nil {
				t.Fatal("resolved index link allowed unapproved offload")
			}
			entry.Approval = &Approval{By: "Overlord", Quote: "Offload preference only.", Date: "2026-10-30", Destination: source + "#preference"}
			writeCatalog(t, dir, catalog)
			if _, err := Load(dir, referenceDate); err != nil {
				t.Fatalf("individual approval failed for resolved index pin: %v", err)
			}
			fact = []byte("A dated preference. <!--a:2026-10-30-->\n")
			writeMemoryFile(t, dir, source, fact)
			catalog.Sources[1].SHA256 = Hash(fact)
			entry.End, entry.SHA256 = len(fact), Hash(fact)
			writeCatalog(t, dir, catalog)
			if _, err := Load(dir, referenceDate); err == nil {
				t.Fatal("resolved index pin allowed a conflicting dated tier")
			}
			if err := os.Remove(filepath.Join(dir, filepath.FromSlash(CatalogPath))); err != nil {
				t.Fatal(err)
			}
			plan, err := PlanMigration(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range plan.Files {
				if file.Path == source && (file.Moves[0].Tier != "pinned" || !file.Moves[0].NeedsApproval || file.Moves[0].ClassificationError == "" || file.RetainedSHA256 != Hash(fact)) {
					t.Fatalf("legacy inventory lost conflicting pin or bytes: %+v", file)
				}
			}
		})
	}
}

func TestCatalogRejectsBlankRoutingKeysAndColdTriggers(t *testing.T) {
	for _, location := range []string{"warm", "cold"} {
		for _, field := range []string{"projects", "tasks", "topics", "trigger"} {
			if field == "trigger" && location == "warm" {
				continue
			}
			for _, blank := range []string{"", " \t\r\n", "\u2003"} {
				t.Run(location+"/"+field+"/"+blank, func(t *testing.T) {
					dir, catalog := catalogFixture(t)
					entry := &catalog.Sources[2].Entries[0]
					entry.Location = location
					keys := []string{"ports", blank}
					switch field {
					case "projects":
						entry.Projects = keys
					case "tasks":
						entry.Tasks = keys
					case "topics":
						entry.Topics = keys
					case "trigger":
						entry.Trigger = blank
					}
					writeCatalog(t, dir, catalog)

					if _, err := Load(dir, referenceDate); err == nil {
						t.Fatal("unusable routing text was accepted")
					}
				})
			}
		}
	}
}

func TestPendingCaptureGraceAndPerishableEntriesCannotBeCold(t *testing.T) {
	for _, text := range []string{
		"A cache fact <!--a:2026-10-30--><!--t:team-->",
		"A cache fact <!--g-->",
		"A legacy cache fact",
		"Wait for task-7 <!--p:2026-10-30-->",
	} {
		t.Run(text, func(t *testing.T) {
			dir, catalog := catalogFixture(t)
			source := &catalog.Sources[2]
			writeMemoryFile(t, dir, source.Path, []byte(text))
			source.SHA256 = Hash([]byte(text))
			source.Entries[0].End, source.Entries[0].SHA256 = len(text), source.SHA256
			source.Entries[0].Expiry = "task-7"
			writeCatalog(t, dir, catalog)
			if _, err := Load(dir, referenceDate); err == nil {
				t.Fatal("protected fact was removed from startup by a cold classification")
			}
		})
	}
}

func TestStaleColdFactsRemainExplicitInTheRoutingMap(t *testing.T) {
	dir, _ := catalogFixture(t)
	catalog, err := Load(dir, referenceDate.AddDate(0, 2, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(catalog.Routes(2000), "STALE") {
		t.Fatal("stale cold fact disappeared behind an apparently current route")
	}
}

func TestDeclaredTierCaptureAndCorrectionRulesReachEveryRetrieval(t *testing.T) {
	dir, catalog := catalogFixture(t)
	catalog.Layers = []Layer{{Name: "team-memory", Classes: []string{"project-facts"}, Owner: "project owner", Capture: "Report the verified finding to its project owner.", Correction: "Append a correction with the prior reference; retain the original."}}
	writeCatalog(t, dir, catalog)
	loaded, err := Load(dir, referenceDate)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []Query{{}, {Topic: "ports"}, {ID: "history"}} {
		output, err := loaded.Retrieve(query, referenceDate)
		if err != nil || !strings.Contains(output, catalog.Layers[0].Capture) || !strings.Contains(output, catalog.Layers[0].Correction) {
			t.Fatalf("declared tier ownership was hidden from the consumer: %q, %v", output, err)
		}
	}
}

func TestExistingColdArchiveRemainsColdWithoutInventingANewOffload(t *testing.T) {
	dir, catalog := catalogFixture(t)
	content := []byte("A retired fact with its original provenance.\n")
	writeMemoryFile(t, dir, "memory-archive.md", content)
	catalog.Sources = append(catalog.Sources, Source{Path: "memory-archive.md", SHA256: Hash(content), Entries: []Entry{{ID: "existing-archive", End: len(content), SHA256: Hash(content), Authority: "history", Location: "cold", Trigger: "archive recovery", Topics: []string{"archive"}}}})
	writeCatalog(t, dir, catalog)
	loaded, err := Load(dir, referenceDate)
	if err != nil {
		t.Fatal(err)
	}
	startup, err := loaded.Retrieve(Query{}, referenceDate)
	if err != nil || strings.Contains(startup, string(content)) {
		t.Fatal("existing archive was injected as new warm authority")
	}
	recovered, err := loaded.Retrieve(Query{ID: "existing-archive"}, referenceDate)
	if err != nil || !strings.Contains(recovered, string(content)) {
		t.Fatal("existing archive became irretrievable")
	}
}

func TestCatalogPreservesIndexPinsAcrossRetrievalAndInventory(t *testing.T) {
	dir := t.TempDir()
	index := []byte("- [Preference](preferences.md) - standing preference <!--P-->\n")
	fact := []byte("Keep the preference verbatim.\n")
	writeMemoryFile(t, dir, "memory/MEMORY.md", index)
	writeMemoryFile(t, dir, "memory/preferences.md", fact)
	catalog := Catalog{Version: 1, Sources: []Source{
		{Path: "memory/MEMORY.md", SHA256: Hash(index), Entries: []Entry{{ID: "index-pin", End: len(index), SHA256: Hash(index), Authority: "fact", Location: "warm"}}},
		{Path: "memory/preferences.md", SHA256: Hash(fact), Entries: []Entry{{ID: "preference", End: len(fact), SHA256: Hash(fact), Authority: "fact", Location: "warm", Topics: []string{"preferences"}, Trigger: "preference changes"}}},
	}}
	writeCatalog(t, dir, catalog)
	loaded, err := Load(dir, referenceDate)
	if err != nil {
		t.Fatal(err)
	}

	text, err := loaded.Retrieve(Query{ID: "preference"}, referenceDate)
	plan, planErr := PlanMigration(dir)

	if err != nil || !strings.Contains(text, "tier=pinned") || planErr != nil {
		t.Fatalf("retrieval or inventory lost index authority: %s, %v, %v", text, err, planErr)
	}
	for _, file := range plan.Files {
		if file.Path == "memory/preferences.md" && (!file.Moves[0].NeedsApproval || file.Moves[0].Tier != "pinned") {
			t.Fatalf("inventory lost index pin: %+v", file)
		}
	}
	catalog.Sources[1].Entries[0].Location = "cold"
	writeCatalog(t, dir, catalog)
	if _, err := Load(dir, referenceDate); err == nil {
		t.Fatal("index-pinned fact was offloaded without individual approval")
	}
	for _, marker := range []string{"<!--a:2026-10-30-->", "<!--p:2026-10-30-->", "<!--g-->"} {
		content := append([]byte("A differently tiered preference "), []byte(marker)...)
		writeMemoryFile(t, dir, "memory/preferences.md", content)
		catalog.Sources[1].SHA256 = Hash(content)
		entry := &catalog.Sources[1].Entries[0]
		entry.End, entry.SHA256, entry.Location = len(content), Hash(content), "warm"
		writeCatalog(t, dir, catalog)
		if _, err := Load(dir, referenceDate); err == nil {
			t.Fatalf("index pin silently erased explicit tier %s", marker)
		}
	}
}
