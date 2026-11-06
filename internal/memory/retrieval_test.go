package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSearchKeepsAuthorityAndScopesColdCandidates(t *testing.T) {
	dir, definition := catalogFixture(t)
	definition.Sources[2].Entries[0].Projects = []string{"northwind"}
	definition.Sources[3].Entries[0].Projects = []string{"other"}
	writeCatalog(t, dir, definition)
	catalog, err := Load(dir, referenceDate)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name            string
		query           Query
		present, absent string
	}{
		{"word discovery", Query{Search: "PORT", Limit: 2}, "port 4300", "completed investigation"},
		{"project restriction", Query{Search: "investigation", Project: "northwind", Limit: 2}, "No cold search matches", "completed investigation"},
		{"absent", Query{Search: "unicorn", Limit: 2}, "No cold search matches", "port 4300"},
	} {
		t.Run(test.name, func(t *testing.T) {
			output, err := catalog.Retrieve(test.query)
			if err != nil || !strings.Contains(output, "The release needs approval.") || !strings.Contains(output, test.present) || strings.Contains(output, test.absent) {
				t.Fatalf("%s: %v", output, err)
			}
		})
	}
}

func TestDeliveryAcknowledgementRequiresFreshSourceAndReplaysExactID(t *testing.T) {
	dir, _ := catalogFixture(t)
	catalog, err := Load(dir, referenceDate)
	if err != nil {
		t.Fatal(err)
	}
	first, err := catalog.Retrieve(Query{})
	if err != nil {
		t.Fatal(err)
	}
	var delivered []string
	for _, word := range strings.Fields(first) {
		if strings.HasPrefix(word, "delivery=") {
			delivered = append(delivered, strings.TrimPrefix(word, "delivery="))
		}
	}
	if len(delivered) != 2 {
		t.Fatalf("missing delivery fingerprints: %s", first)
	}
	repeated, err := catalog.Retrieve(Query{Delivered: delivered})
	if err != nil || strings.Contains(repeated, "The release needs approval.") || !strings.Contains(repeated, "already delivered") {
		t.Fatalf("duplicate delivery: %s, %v", repeated, err)
	}
	exact, err := catalog.Retrieve(Query{ID: "authority", Delivered: delivered})
	if err != nil || !strings.Contains(exact, "The release needs approval.") {
		t.Fatalf("exact recovery: %s, %v", exact, err)
	}
	writeMemoryFile(t, dir, "overlord.md", []byte("Changed ruling.\n"))
	if _, err := catalog.Retrieve(Query{Delivered: delivered}); err == nil {
		t.Fatal("cached authority survived a source edit")
	}
}

func TestConcurrentReadersRejectChangedDeletedRenamedAndSupersededSources(t *testing.T) {
	for _, change := range []string{"edit", "delete", "rename", "supersession"} {
		t.Run(change, func(t *testing.T) {
			dir, definition := catalogFixture(t)
			catalog, err := Load(dir, referenceDate)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "overlord.md")
			switch change {
			case "edit":
				writeMemoryFile(t, dir, "overlord.md", []byte("new"))
			case "delete":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "rename":
				if err := os.Rename(path, path+".old"); err != nil {
					t.Fatal(err)
				}
			case "supersession":
				definition.Sources[1].Entries[0].Supersedes = []string{"authority"}
				writeCatalog(t, dir, definition)
			}
			var readers sync.WaitGroup
			for range 4 {
				readers.Go(func() {
					if _, err := catalog.Retrieve(Query{}); err == nil {
						t.Error("reader reused stale snapshot")
					}
				})
			}
			readers.Wait()
			if change != "supersession" {
				if _, err := Load(dir, referenceDate); err == nil {
					t.Fatal("restart accepted stale sources")
				}
			}
		})
	}
}

func TestTaskStateProtectsActiveAndHeldEntries(t *testing.T) {
	for _, state := range []string{"active", "held", "completed", "unknown"} {
		t.Run(state, func(t *testing.T) {
			dir, definition := catalogFixture(t)
			definition.Sources[2].Entries[0].TaskState = state
			writeCatalog(t, dir, definition)
			catalog, err := Load(dir, referenceDate)
			if state != "completed" {
				if err == nil {
					t.Fatal("accepted unsafe task state")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			output, err := catalog.Retrieve(Query{Search: "port", Limit: 1})
			if err != nil || strings.Contains(output, "port 4300") {
				t.Fatalf("completed task injected: %s %v", output, err)
			}
			output, err = catalog.Retrieve(Query{ID: "project"})
			if err != nil || !strings.Contains(output, "port 4300") {
				t.Fatalf("completed history lost: %s %v", output, err)
			}
		})
	}
}

func TestSearchRejectsInvalidQueriesAndReceipts(t *testing.T) {
	dir, _ := catalogFixture(t)
	catalog, err := Load(dir, referenceDate)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []Query{{Search: "port", Limit: -1}, {Search: "port", Limit: 51}, {Search: "!!!", Limit: 1}, {Search: "port", ID: "project"}, {Delivered: []string{"bad"}}} {
		if _, err := catalog.Retrieve(query); err == nil {
			encoded, _ := json.Marshal(query)
			t.Fatalf("accepted invalid query %s", encoded)
		}
	}
}

func TestDeliveredStaleFactsStillReportStowDiagnostics(t *testing.T) {
	dir, definition := catalogFixture(t)
	text := "Old cache note. <!--a:2026-09-01-->\n"
	writeMemoryFile(t, dir, "memory/current.md", []byte(text))
	definition.Sources[1].SHA256 = Hash([]byte(text))
	definition.Sources[1].Entries[0].End = len(text)
	definition.Sources[1].Entries[0].SHA256 = Hash([]byte(text))
	writeCatalog(t, dir, definition)
	catalog, err := Load(dir, referenceDate)
	if err != nil {
		t.Fatal(err)
	}
	output, err := catalog.Retrieve(Query{Delivered: []string{catalog.deliveryKey("current")}})
	if err != nil || strings.Contains(output, text) || !strings.Contains(output, "STOW REQUIRED: stale=true") {
		t.Fatalf("lost diagnostic: %s %v", output, err)
	}
}

func TestSearchLimitNeverCapsWarmAuthorityAndMetadataEditsReplayBodies(t *testing.T) {
	dir, definition := catalogFixture(t)
	definition.Sources[2].Entries[0].Trigger = "cache port"
	definition.Sources[3].Entries[0].Trigger = "cache investigation"
	writeCatalog(t, dir, definition)
	catalog, err := Load(dir, referenceDate)
	if err != nil {
		t.Fatal(err)
	}
	output, err := catalog.Retrieve(Query{Search: "cache", Limit: 1})
	if err != nil || !strings.Contains(output, "The release needs approval.") || !strings.Contains(output, "Use the local cache.") {
		t.Fatalf("warm authority capped: %s %v", output, err)
	}
	if strings.Contains(output, "port 4300") && strings.Contains(output, "completed investigation") {
		t.Fatal("cold limit ignored")
	}
	previous := catalog.deliveryKey("authority")
	definition.Sources[1].Entries[0].Supersedes = []string{"authority"}
	writeCatalog(t, dir, definition)
	reloaded, err := Load(dir, referenceDate)
	if err != nil {
		t.Fatal(err)
	}
	output, err = reloaded.Retrieve(Query{Delivered: []string{previous}})
	if err != nil || !strings.Contains(output, "The release needs approval.") || !strings.Contains(output, "Supersession candidates") {
		t.Fatalf("metadata change hid a conflict: %s %v", output, err)
	}
}
