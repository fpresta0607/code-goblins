package memory

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
)

const scopedRulings = "# Rulings\n\n## 2026-10-01 Releases\n\nThe release needs approval.\n\n- Harbor ships on Fridays.\n- Harbor ships on Fridays.\n\n## 2026-10-02 Ports\n\nHarbor keeps port 4300 free.\nHarbor closes at dusk.\n"

// scopedHome is a home whose memory has every shape a real one has: rulings
// under dated headings with a repeated line, an index with an aging and a
// pinned line, the two facts it links and an archive that ends in the marker
// of the line last archived.
func scopedHome(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	dataDir, stateDir := filepath.Join(root, "data"), filepath.Join(root, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeMemoryFile(t, dataDir, "overlord.md", []byte(scopedRulings))
	writeMemoryFile(t, dataDir, "memory/MEMORY.md", []byte("# Memory index\n\n- [Cache](cache.md) - where the cache lives <!--a:2026-10-30-->\n- [Owner](owner.md) - who approves a release <!--P-->\n"))
	writeMemoryFile(t, dataDir, "memory/cache.md", []byte("---\nname: cache\ndescription: The cache lives on the second drive\nmetadata:\n  type: project\n---\n\nUse the second drive.\n"))
	writeMemoryFile(t, dataDir, "memory/owner.md", []byte("---\nname: owner\ndescription: Releases are his alone\nmetadata:\n  type: user\n---\n\nHe approves each release.\n"))
	writeMemoryFile(t, dataDir, "memory-archive.md", []byte("## Archived 2026-09-01\r\n\r\n- An old port note <!--p:2026-08-01-->\r\n"))
	return dataDir, stateDir
}

func rulingID(t *testing.T, dataDir, stateDir, text string) string {
	t.Helper()
	index, err := BuildIndex(dataDir, stateDir, referenceDate)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range index.Items {
		if item.Kind == KindRuling && item.Text == text {
			return item.ID
		}
	}
	t.Fatalf("recall holds no ruling %q", text)
	return ""
}

func TestSyncCatalogsEveryByteAndEveryRulingUnderItsRecallID(t *testing.T) {
	// Arrange
	dataDir, stateDir := scopedHome(t)

	// Act
	report, err := Sync(dataDir, stateDir, referenceDate)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := Load(dataDir, referenceDate)
	if err != nil {
		t.Fatalf("the synced catalog does not load: %v", err)
	}
	entries := 0
	for _, source := range catalog.Sources {
		content, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(source.Path)))
		if err != nil {
			t.Fatal(err)
		}
		var covered bytes.Buffer
		for _, entry := range source.Entries {
			covered.Write(content[entry.Start:entry.End])
			entries++
		}
		if !bytes.Equal(covered.Bytes(), content) {
			t.Fatalf("%s lost bytes in the catalog: %q", source.Path, covered.String())
		}
	}
	if report.Entries != entries || report.Warm+report.Cold != entries || report.Added != entries {
		t.Fatalf("the report miscounts %d entries: %+v", entries, report)
	}
	index, err := BuildIndex(dataDir, stateDir, referenceDate)
	if err != nil {
		t.Fatal(err)
	}
	rulings := 0
	for _, item := range index.Items {
		if item.Kind != KindRuling {
			continue
		}
		rulings++
		exact, err := catalog.Retrieve(Query{ID: item.ID})
		if err != nil || !strings.Contains(exact, item.Text+"\n") {
			t.Fatalf("ruling %s is not retrievable verbatim by the id recall prints: %q, %v", item.ID, exact, err)
		}
	}
	// Recall holds a repeated line once; the catalog holds each one.
	if rulings != 4 || len(catalog.Sources[0].Entries) != 5 || catalog.Sources[0].Path != "overlord.md" {
		t.Fatalf("5 ruling lines, 4 of them distinct, became %d recall items and %d entries of %s", rulings, len(catalog.Sources[0].Entries), catalog.Sources[0].Path)
	}
}

func TestSyncLoadsRulingsTheIndexAndPinnedFactsAndLeavesOtherFactsAndTheArchiveCold(t *testing.T) {
	// Arrange
	dataDir, stateDir := scopedHome(t)

	// Act
	if _, err := Sync(dataDir, stateDir, referenceDate); err != nil {
		t.Fatal(err)
	}

	// Assert
	catalog, err := Load(dataDir, referenceDate)
	if err != nil {
		t.Fatal(err)
	}
	startup, err := catalog.Retrieve(Query{})
	if err != nil {
		t.Fatal(err)
	}
	for _, loaded := range []string{"The release needs approval.", "Harbor keeps port 4300 free.", "where the cache lives", "He approves each release."} {
		if !strings.Contains(startup, loaded) {
			t.Fatalf("startup lost %q:\n%s", loaded, startup)
		}
	}
	for _, left := range []string{"Use the second drive.", "An old port note"} {
		if strings.Contains(startup, left) {
			t.Fatalf("startup loads %q, which no startup loaded before the catalog:\n%s", left, startup)
		}
	}
	byTopic, err := catalog.Retrieve(Query{Topic: "project"})
	if err != nil || !strings.Contains(byTopic, "Use the second drive.") {
		t.Fatalf("a fact is not routed by its type: %q, %v", byTopic, err)
	}
	if routes := catalog.Routes(2000); !strings.Contains(routes, "cache: The cache lives on the second drive (") {
		t.Fatalf("a cold fact's route does not carry its description: %q", routes)
	}
	archive, err := catalog.Retrieve(Query{ID: "memory-archive"})
	if err != nil || !strings.Contains(archive, "- An old port note <!--p:2026-08-01-->\r\n") {
		t.Fatalf("the archive is not retrievable as it stands: %q, %v", archive, err)
	}
}

func TestScopeMovesARulingToItsProjectAndLeavesItsSourceAsItWas(t *testing.T) {
	// Arrange
	dataDir, stateDir := scopedHome(t)
	if _, err := Sync(dataDir, stateDir, referenceDate); err != nil {
		t.Fatal(err)
	}
	dusk := rulingID(t, dataDir, stateDir, "Harbor closes at dusk.")

	// Act
	report, err := Scope(dataDir, stateDir, referenceDate, Tag{IDs: []string{dusk}, Projects: []string{"harbor"}})

	// Assert
	if err != nil || report.Tagged != 1 {
		t.Fatalf("the ruling was not tagged: %+v, %v", report, err)
	}
	source, err := os.ReadFile(filepath.Join(dataDir, "overlord.md"))
	if err != nil || string(source) != scopedRulings {
		t.Fatalf("tagging rewrote the rulings: %q, %v", source, err)
	}
	catalog, err := Load(dataDir, referenceDate)
	if err != nil {
		t.Fatalf("a project ruling does not load without its own approval: %v", err)
	}
	startup, err := catalog.StartupMap()
	if err != nil || strings.Contains(startup, "Harbor closes at dusk.") || !strings.Contains(startup, "project harbor: rulings 1\n") {
		t.Fatalf("startup still loads the project's ruling, or does not say it left one out: %q, %v", startup, err)
	}
	scoped, err := catalog.Retrieve(Query{Project: "harbor", IsColdOnly: true})
	if err != nil || scoped != "\noverlord.md\n(under ## 2026-10-02 Ports)\n["+dusk+"] Harbor closes at dusk.\n" {
		t.Fatalf("the project's rulings do not come by its scope alone: %q, %v", scoped, err)
	}
	exact, err := catalog.Retrieve(Query{ID: dusk})
	if err != nil || !strings.Contains(exact, "] overlord.md bytes 169:192 ") || !strings.HasSuffix(exact, "\nHarbor closes at dusk.\n\n") {
		t.Fatalf("the ruling is not retrievable verbatim by id: %q, %v", exact, err)
	}
	if elsewhere, err := catalog.Retrieve(Query{Project: "lighthouse", IsColdOnly: true}); err != nil || elsewhere != "No cold memory is routed to that scope.\n" {
		t.Fatalf("a scope that holds nothing cold does not say so: %q, %v", elsewhere, err)
	}
}

func TestScopeRefusesWhatItCannotTagAndChangesNothing(t *testing.T) {
	for _, test := range []struct {
		name string
		tag  func(release string) Tag
		want string
	}{
		{"an id no entry has", func(string) Tag { return Tag{IDs: []string{"ruling-000000000000"}, Projects: []string{"harbor"}} }, "ruling-000000000000"},
		{"a blank project", func(release string) Tag { return Tag{IDs: []string{release}, Projects: []string{" "}} }, "routing keys"},
		{"a pinned fact", func(string) Tag { return Tag{IDs: []string{"owner"}, Projects: []string{"harbor"}} }, "approval"},
		{"nothing to set", func(release string) Tag { return Tag{IDs: []string{release}} }, "project"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dataDir, stateDir := scopedHome(t)
			if _, err := Sync(dataDir, stateDir, referenceDate); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(CatalogPath)))
			if err != nil {
				t.Fatal(err)
			}

			// Act
			_, err = Scope(dataDir, stateDir, referenceDate, test.tag(rulingID(t, dataDir, stateDir, "Harbor keeps port 4300 free.")))

			// Assert
			after, readErr := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(CatalogPath)))
			if err == nil || !strings.Contains(err.Error(), test.want) || readErr != nil || !bytes.Equal(before, after) {
				t.Fatalf("the tag was not refused with the catalog as it was: %v, %v", err, readErr)
			}
		})
	}
}

func TestScopeFleetMarksARulingClassifiedSoStartupCountsOnlyTheRest(t *testing.T) {
	// Arrange
	dataDir, stateDir := scopedHome(t)
	if _, err := Sync(dataDir, stateDir, referenceDate); err != nil {
		t.Fatal(err)
	}
	dusk := rulingID(t, dataDir, stateDir, "Harbor closes at dusk.")
	if _, err := Scope(dataDir, stateDir, referenceDate, Tag{IDs: []string{dusk}, Projects: []string{"harbor"}}); err != nil {
		t.Fatal(err)
	}

	// Act
	_, err := Scope(dataDir, stateDir, referenceDate, Tag{IDs: []string{dusk}, IsFleet: true})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := Load(dataDir, referenceDate)
	if err != nil {
		t.Fatal(err)
	}
	startup, err := catalog.StartupMap()
	if err != nil || !strings.Contains(startup, "] Harbor closes at dusk.\n") || strings.Contains(startup, "project harbor") || !strings.Contains(startup, "Rulings not classified yet, which load until they are: 4;") {
		t.Fatalf("a ruling ruled fleet-wide does not load, or still counts as unclassified: %q, %v", startup, err)
	}
	unclassified, err := catalog.Unclassified()
	if err != nil || strings.Contains(unclassified, "Harbor closes at dusk.") || strings.Count(unclassified, "[ruling-") != 4 {
		t.Fatalf("the rulings still to classify are not the four untouched ones: %q, %v", unclassified, err)
	}
}

func TestSyncAfterAnEditKeepsTagsByIDAndLoadsNewWordsWarm(t *testing.T) {
	// Arrange
	dataDir, stateDir := scopedHome(t)
	if _, err := Sync(dataDir, stateDir, referenceDate); err != nil {
		t.Fatal(err)
	}
	ports := rulingID(t, dataDir, stateDir, "Harbor keeps port 4300 free.")
	fridays := rulingID(t, dataDir, stateDir, "- Harbor ships on Fridays.")
	if _, err := Scope(dataDir, stateDir, referenceDate, Tag{IDs: []string{ports, fridays}, Projects: []string{"harbor"}}); err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(scopedRulings, "Harbor keeps port 4300 free.", "Harbor keeps port 4400 free.", 1)
	edited = "# Rulings\n\n## 2026-10-03 Tides\n\nHarbor waits for the tide.\n" + strings.TrimPrefix(edited, "# Rulings\n")
	writeMemoryFile(t, dataDir, "overlord.md", []byte(edited))
	if _, err := Load(dataDir, referenceDate); err == nil {
		t.Fatal("an edited source left its old catalog loadable, so this test proves nothing")
	}

	// Act
	report, err := Sync(dataDir, stateDir, referenceDate)

	// Assert
	if err != nil || report.Added != 2 || !slices.Equal(report.Dropped, []string{ports}) {
		t.Fatalf("the sync did not report two new rulings and the tagged one that is gone: %+v, %v", report, err)
	}
	catalog, err := Load(dataDir, referenceDate)
	if err != nil {
		t.Fatal(err)
	}
	startup, err := catalog.StartupMap()
	if err != nil {
		t.Fatal(err)
	}
	for _, loaded := range []string{"Harbor waits for the tide.", "Harbor keeps port 4400 free."} {
		if !strings.Contains(startup, loaded) {
			t.Fatalf("new words did not load at startup: %q", startup)
		}
	}
	if strings.Count(startup, "- Harbor ships on Fridays.") != 1 || !strings.Contains(startup, "project harbor: rulings 1\n") {
		t.Fatalf("a tag did not follow its ruling to the ruling's new place: %q", startup)
	}
}

func TestSyncKeepsAnUnchangedSourcesEntriesAsTheyWereSet(t *testing.T) {
	// Arrange
	dataDir, stateDir := scopedHome(t)
	if _, err := Sync(dataDir, stateDir, referenceDate); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(CatalogPath)))
	if err != nil {
		t.Fatal(err)
	}
	var catalog Catalog
	if err := Decode(content, &catalog); err != nil {
		t.Fatal(err)
	}
	for position, source := range catalog.Sources {
		if source.Path == "memory/cache.md" {
			catalog.Sources[position].Entries[0].ID = "lesson-0123456789ab"
			catalog.Sources[position].Entries[0].Projects = []string{"harbor"}
		}
	}
	writeCatalog(t, dataDir, catalog)
	writeMemoryFile(t, dataDir, "memory/tide.md", []byte("---\nname: tide\ndescription: The tide table is in the office\nmetadata:\n  type: reference\n---\n\nAsk the office.\n"))

	// Act
	if _, err := Sync(dataDir, stateDir, referenceDate); err != nil {
		t.Fatal(err)
	}

	// Assert
	loaded, err := Load(dataDir, referenceDate)
	if err != nil {
		t.Fatal(err)
	}
	lesson, err := loaded.Retrieve(Query{Project: "harbor", IsColdOnly: true})
	if err != nil || !strings.Contains(lesson, "[lesson-0123456789ab legacy]\n---\nname: cache\n") {
		t.Fatalf("an entry set by hand on an unchanged file was derived again: %q, %v", lesson, err)
	}
	if routes := loaded.Routes(2000); !strings.Contains(routes, "tide: The tide table is in the office (projects= tasks= topics=reference)") {
		t.Fatalf("a new fact file did not join the catalog cold: %q", routes)
	}
}

func TestSyncKeepsTheScopeOfAFactWhoseWordsChange(t *testing.T) {
	// Arrange
	dataDir, stateDir := scopedHome(t)
	if _, err := Scope(dataDir, stateDir, referenceDate, Tag{IDs: []string{"cache"}, Projects: []string{"harbor"}}); err != nil {
		t.Fatal(err)
	}
	cache, err := os.ReadFile(filepath.Join(dataDir, "memory", "cache.md"))
	if err != nil {
		t.Fatal(err)
	}
	writeMemoryFile(t, dataDir, "memory/cache.md", append(cache, []byte("Keep 20 GB free.\n")...))

	// Act
	report, err := Sync(dataDir, stateDir, referenceDate)

	// Assert
	if err != nil || report.Added != 0 || len(report.Dropped) != 0 {
		t.Fatalf("a fact that changed was taken for a new one: %+v, %v", report, err)
	}
	catalog, err := Load(dataDir, referenceDate)
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := catalog.Retrieve(Query{Project: "harbor", IsColdOnly: true})
	if err != nil || !strings.Contains(scoped, "[cache legacy]\n---\nname: cache\n") || !strings.Contains(scoped, "Use the second drive.\nKeep 20 GB free.\n") {
		t.Fatalf("the fact's scope did not stay with the file: %q, %v", scoped, err)
	}
}

func TestSyncCatalogsRulingsFilesOfEveryShape(t *testing.T) {
	for name, test := range map[string]struct {
		rulings string
		entries int
	}{
		"a heading below the last ruling": {"## 2026-10-01 Releases\n\nThe release needs approval.\n\n## 2026-10-02 Ports\n", 1},
		"headings and no ruling":          {"# Rulings\n\n## 2026-10-01 Releases\n", 1},
		"no line ending at the end":       {"The release needs approval.\nHarbor closes at dusk.", 2},
		"an empty file":                   {"", 0},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dataDir, stateDir := scopedHome(t)
			writeMemoryFile(t, dataDir, "overlord.md", []byte(test.rulings))

			// Act
			_, err := Sync(dataDir, stateDir, referenceDate)

			// Assert
			if err != nil {
				t.Fatalf("the rulings file did not sync: %v", err)
			}
			catalog, err := Load(dataDir, referenceDate)
			if err != nil {
				t.Fatal(err)
			}
			var covered bytes.Buffer
			for _, entry := range catalog.Sources[0].Entries {
				covered.WriteString(test.rulings[entry.Start:entry.End])
			}
			if catalog.Sources[0].Path != "overlord.md" || len(catalog.Sources[0].Entries) != test.entries || covered.String() != test.rulings {
				t.Fatalf("%d entries hold %q of %q", len(catalog.Sources[0].Entries), covered.String(), test.rulings)
			}
		})
	}
}

func TestSyncLoadsAChangedFactWhoseEntriesWereSetByHand(t *testing.T) {
	// Arrange: a fact file cataloged in two cold parts, which no longer fit
	// the file once it changes.
	dataDir, stateDir := scopedHome(t)
	if _, err := Sync(dataDir, stateDir, referenceDate); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(CatalogPath)))
	if err != nil {
		t.Fatal(err)
	}
	var catalog Catalog
	if err := Decode(content, &catalog); err != nil {
		t.Fatal(err)
	}
	cache, err := os.ReadFile(filepath.Join(dataDir, "memory", "cache.md"))
	if err != nil {
		t.Fatal(err)
	}
	split := bytes.Index(cache, []byte("Use the second drive."))
	for position, source := range catalog.Sources {
		if source.Path == "memory/cache.md" {
			catalog.Sources[position].Entries = []Entry{
				{ID: "cache-front", End: split, SHA256: Hash(cache[:split]), Authority: "fact", Location: "cold", Trigger: "cache", Topics: []string{"project"}},
				{ID: "cache-body", Start: split, End: len(cache), SHA256: Hash(cache[split:]), Authority: "fact", Location: "cold", Trigger: "cache", Topics: []string{"project"}},
			}
		}
	}
	writeCatalog(t, dataDir, catalog)
	if _, err := Load(dataDir, referenceDate); err != nil {
		t.Fatalf("the catalog set by hand does not load, so this test proves nothing: %v", err)
	}
	writeMemoryFile(t, dataDir, "memory/cache.md", append(cache, []byte("Keep 20 GB free.\n")...))

	// Act
	report, err := Sync(dataDir, stateDir, referenceDate)

	// Assert
	if err != nil || !slices.Equal(report.Dropped, []string{"cache-body", "cache-front"}) {
		t.Fatalf("the sync did not name the two parts that no longer fit: %+v, %v", report, err)
	}
	loaded, err := Load(dataDir, referenceDate)
	if err != nil {
		t.Fatal(err)
	}
	startup, err := loaded.StartupMap()
	if err != nil || !strings.Contains(startup, "[cache legacy]\n---\nname: cache\n") || !strings.Contains(startup, "Keep 20 GB free.\n") {
		t.Fatalf("a fact whose parts were lost does not load until they are set again: %q, %v", startup, err)
	}
}

func TestSyncLeavesTheCatalogAsItWasWhenTheResultWouldNotLoad(t *testing.T) {
	for _, hasCatalog := range []bool{true, false} {
		t.Run(map[bool]string{true: "a catalog", false: "no catalog"}[hasCatalog], func(t *testing.T) {
			// Arrange
			dataDir, stateDir := scopedHome(t)
			path := filepath.Join(dataDir, filepath.FromSlash(CatalogPath))
			var before []byte
			if hasCatalog {
				if _, err := Sync(dataDir, stateDir, referenceDate); err != nil {
					t.Fatal(err)
				}
				var err error
				if before, err = os.ReadFile(path); err != nil {
					t.Fatal(err)
				}
			}
			// A perishable line needs an expiry only the CEO can name.
			writeMemoryFile(t, dataDir, "memory/MEMORY.md", []byte("- [Cache](cache.md) - until task-7 lands <!--p:2026-10-30-->\n"))

			// Act
			_, err := Sync(dataDir, stateDir, referenceDate)

			// Assert
			after, readErr := os.ReadFile(path)
			if err == nil || !strings.Contains(err.Error(), "perishable memory needs its checkable expiry") || !strings.Contains(err.Error(), "--expiry") {
				t.Fatalf("a catalog that would not load was written, or the refusal does not say what to set: %v", err)
			}
			if hasCatalog && (readErr != nil || !bytes.Equal(before, after)) || !hasCatalog && !os.IsNotExist(readErr) {
				t.Fatalf("the catalog was not left as it was: %v", readErr)
			}
		})
	}
}

func TestScopeNamesAPerishableLinesExpirySoTheCatalogLoads(t *testing.T) {
	// Arrange
	dataDir, stateDir := scopedHome(t)
	writeMemoryFile(t, dataDir, "memory/MEMORY.md", []byte("- [Cache](cache.md) - until task-7 lands <!--p:2026-10-30-->\n"))
	_, err := Sync(dataDir, stateDir, referenceDate)
	if err == nil {
		t.Fatal("a perishable line with no expiry synced")
	}
	id := regexp.MustCompile(`index-[0-9a-f]{12}`).FindString(err.Error())

	// Act
	_, err = Scope(dataDir, stateDir, referenceDate, Tag{IDs: []string{id}, Expiry: "task-7"})

	// Assert
	if err != nil {
		t.Fatalf("the id the refusal named took no expiry: %v", err)
	}
	if _, err := Load(dataDir, referenceDate); err != nil {
		t.Fatal(err)
	}
}

func TestSyncWaitsItsTurnBehindAnotherMemoryWrite(t *testing.T) {
	// Arrange
	dataDir, stateDir := scopedHome(t)
	previousWait := lessonLockWait
	lessonLockWait = time.Second
	t.Cleanup(func() { lessonLockWait = previousWait })
	// Nobody lets the lock go, as a process deciding a lesson would hold it.
	if _, err := lock.AcquireNamedOwner(stateDir, lessonLockName, os.Getppid(), ""); err != nil {
		t.Fatal(err)
	}

	// Act
	_, err := Sync(dataDir, stateDir, referenceDate)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "another memory write is still under way") {
		t.Fatalf("a sync wrote the catalog under another writer's lock: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dataDir, filepath.FromSlash(CatalogPath))); !os.IsNotExist(statErr) {
		t.Fatalf("a refused sync left a catalog: %v", statErr)
	}
}
