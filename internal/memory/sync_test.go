package memory

import (
	"bytes"
	"errors"
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

// catalogWritten is a fixed past instant no test runs at, so a catalog file
// still dated then was never written again.
var catalogWritten = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// agedCatalog dates the catalog file at catalogWritten and returns its bytes.
func agedCatalog(t *testing.T, dataDir string) []byte {
	t.Helper()
	path := filepath.Join(dataDir, filepath.FromSlash(CatalogPath))
	if err := os.Chtimes(path, catalogWritten, catalogWritten); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

// requireCatalogNeverWritten fails unless the catalog file holds the bytes
// agedCatalog returned and is still dated as it left it: a catalog written
// and then put back has the same bytes and a new date.
func requireCatalogNeverWritten(t *testing.T, dataDir string, before []byte) {
	t.Helper()
	path := filepath.Join(dataDir, filepath.FromSlash(CatalogPath))
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the catalog was not left as it was: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || !info.ModTime().Equal(catalogWritten) {
		t.Fatalf("the catalog file was written again: %+v, %v", info, err)
	}
}

// entryOf is the catalog's entry of that id.
func entryOf(t *testing.T, catalog Catalog, id string) Entry {
	t.Helper()
	for _, source := range catalog.Sources {
		for _, entry := range source.Entries {
			if entry.ID == id {
				return entry
			}
		}
	}
	t.Fatalf("the catalog holds no entry %s", id)
	return Entry{}
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
			before := agedCatalog(t, dataDir)

			// Act
			_, err := Scope(dataDir, stateDir, referenceDate, test.tag(rulingID(t, dataDir, stateDir, "Harbor keeps port 4300 free.")))

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("the tag was not refused: %v", err)
			}
			requireCatalogNeverWritten(t, dataDir, before)
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
				before = agedCatalog(t, dataDir)
			}
			// A perishable line needs an expiry only the CEO can name.
			writeMemoryFile(t, dataDir, "memory/MEMORY.md", []byte("- [Cache](cache.md) - until task-7 lands <!--p:2026-10-30-->\n"))

			// Act
			_, err := Sync(dataDir, stateDir, referenceDate)

			// Assert
			if err == nil || !strings.Contains(err.Error(), "perishable memory needs its checkable expiry") || !strings.Contains(err.Error(), "--expiry") {
				t.Fatalf("a catalog that would not load was written, or the refusal does not say what to set: %v", err)
			}
			if hasCatalog {
				requireCatalogNeverWritten(t, dataDir, before)
			} else if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
				t.Fatalf("a refused first sync left a catalog: %v", statErr)
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
	_, err = Scope(dataDir, stateDir, referenceDate, Tag{Expiries: map[string]string{id: "task-7"}})

	// Assert
	if err != nil {
		t.Fatalf("the id the refusal named took no expiry: %v", err)
	}
	if _, err := Load(dataDir, referenceDate); err != nil {
		t.Fatal(err)
	}
}

func TestScopeNamesEveryPerishableEntrysExpiryInOneCall(t *testing.T) {
	// Oracle: the catalog schema: an expiry is a condition quoted in a
	// perishable entry's own text, so two entries need two sets of words.
	// Arrange
	dataDir, stateDir := scopedHome(t)
	writeMemoryFile(t, dataDir, "memory/MEMORY.md", []byte("- [Cache](cache.md) - until task-7 lands <!--p:2026-10-30-->\n- [Owner](owner.md) - until v2 ships <!--p:2026-10-30-->\n"))
	path := filepath.Join(dataDir, filepath.FromSlash(CatalogPath))
	_, err := Sync(dataDir, stateDir, referenceDate)
	if err == nil {
		t.Fatal("two perishable lines with no expiry synced")
	}
	named, _, _ := strings.Cut(err.Error(), ": perishable")
	ids := regexp.MustCompile(`index-[0-9a-f]{12}`).FindAllString(named, -1)
	if len(ids) != 2 || ids[0] == ids[1] || !errors.Is(err, errExpiryMissing) || !strings.Contains(err.Error(), `ceo memory scope --expiry `+ids[0]+`="<words>" --expiry `+ids[1]+`="<words>"`) {
		t.Fatalf("the refusal does not name both entries and the one command that names their expiries: %v", err)
	}

	// Act
	_, oneErr := Scope(dataDir, stateDir, referenceDate, Tag{Expiries: map[string]string{ids[0]: "task-7"}})
	_, statErr := os.Stat(path)
	report, bothErr := Scope(dataDir, stateDir, referenceDate, Tag{Expiries: map[string]string{ids[0]: "task-7", ids[1]: "v2"}})

	// Assert
	if oneErr == nil || !strings.Contains(oneErr.Error(), `ceo memory scope --expiry `+ids[1]+`="<words>"`) || strings.Contains(oneErr.Error(), ids[0]) || !os.IsNotExist(statErr) {
		t.Fatalf("a call naming one expiry was not refused naming the other entry alone, with no catalog written: %v, %v", oneErr, statErr)
	}
	if bothErr != nil || report.Tagged != 2 {
		t.Fatalf("one call naming both expiries did not write the catalog: %+v, %v", report, bothErr)
	}
	catalog, err := Load(dataDir, referenceDate)
	if err != nil {
		t.Fatalf("the catalog with both expiries named does not load: %v", err)
	}
	if first, second := entryOf(t, catalog, ids[0]), entryOf(t, catalog, ids[1]); first.Expiry != "task-7" || second.Expiry != "v2" || first.Location != "warm" || second.Location != "warm" {
		t.Fatalf("the entries do not each carry their own expiry and stay warm: %+v, %+v", first, second)
	}
}

func TestSyncKeepsALineFilesOnlyEntryUnderItsIDWhenALineJoinsIt(t *testing.T) {
	for name, test := range map[string]struct{ file, first, second string }{
		"a ruling":      {"overlord.md", "Harbor closes at dusk.\n", "Harbor opens at dawn.\n"},
		"an index line": {"memory/MEMORY.md", "- [Cache](cache.md) - where the cache lives <!--a:2026-10-30-->\n", "- [Owner](owner.md) - who approves a release <!--a:2026-10-30-->\n"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dataDir, stateDir := scopedHome(t)
			writeMemoryFile(t, dataDir, test.file, []byte(test.first))
			if _, err := Sync(dataDir, stateDir, referenceDate); err != nil {
				t.Fatal(err)
			}
			synced, err := Load(dataDir, referenceDate)
			if err != nil {
				t.Fatal(err)
			}
			position := slices.IndexFunc(synced.Sources, func(source Source) bool { return source.Path == test.file })
			if position < 0 || len(synced.Sources[position].Entries) != 1 {
				t.Fatalf("%s is not one entry, so this test proves nothing: %+v", test.file, synced.Sources)
			}
			id := synced.Sources[position].Entries[0].ID
			if _, err := Scope(dataDir, stateDir, referenceDate, Tag{IDs: []string{id}, Projects: []string{"harbor"}}); err != nil {
				t.Fatal(err)
			}
			writeMemoryFile(t, dataDir, test.file, []byte(test.first+test.second))

			// Act
			report, err := Sync(dataDir, stateDir, referenceDate)

			// Assert
			if err != nil || report.Added != 1 || len(report.Dropped) != 0 {
				t.Fatalf("the sync did not add the one new line and drop nothing: %+v, %v", report, err)
			}
			catalog, err := Load(dataDir, referenceDate)
			if err != nil {
				t.Fatal(err)
			}
			if kept := entryOf(t, catalog, id); kept.Location != "cold" || !slices.Equal(kept.Projects, []string{"harbor"}) {
				t.Fatalf("the line that is still there lost its scope: %+v", kept)
			}
			exact, err := catalog.Retrieve(Query{ID: id})
			if err != nil || !strings.HasSuffix(exact, "\n"+test.first+"\n") {
				t.Fatalf("the line is not retrievable verbatim by the id it had: %q, %v", exact, err)
			}
		})
	}
}

func TestSyncLoadsAFactFileWhoseMarkerTheContractKeepsWarm(t *testing.T) {
	// Oracle: the catalog schema: pending capture, grace and perishable
	// entries stay warm and a pinned item is cold only on its own approval,
	// so a sync derives them warm and stops on none.
	for name, test := range map[string]struct{ marker, expiry, refusal string }{
		"pending capture": {"<!--t:team-->", "", "must remain warm"},
		"grace":           {"<!--g-->", "", "must remain warm"},
		"perishable":      {"<!--p:2026-10-30-->", "until task-7 lands", "must remain warm"},
		"pinned":          {"<!--P-->", "", "pinned offload requires this named item's verbatim Overlord approval"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dataDir, stateDir := scopedHome(t)
			writeMemoryFile(t, dataDir, "memory/tide.md", []byte("---\nname: tide\ndescription: The tide table is in the office\nmetadata:\n  type: reference\n---\n\nAsk the office until task-7 lands. "+test.marker+"\n"))

			// Act
			_, err := Sync(dataDir, stateDir, referenceDate)
			if test.expiry != "" {
				if err == nil || !strings.Contains(err.Error(), `--expiry tide="<words>"`) {
					t.Fatalf("a perishable fact with no expiry was not refused naming it: %v", err)
				}
				_, err = Scope(dataDir, stateDir, referenceDate, Tag{Expiries: map[string]string{"tide": test.expiry}})
			}

			// Assert
			if err != nil {
				t.Fatalf("a fact file nobody tagged stopped the sync: %v", err)
			}
			catalog, err := Load(dataDir, referenceDate)
			if err != nil {
				t.Fatal(err)
			}
			startup, err := catalog.StartupMap()
			if err != nil || !strings.Contains(startup, "Ask the office until task-7 lands.") {
				t.Fatalf("the fact does not load at startup: %q, %v", startup, err)
			}
			before := agedCatalog(t, dataDir)
			_, err = Scope(dataDir, stateDir, referenceDate, Tag{IDs: []string{"tide"}, Projects: []string{"harbor"}})
			if err == nil || !strings.Contains(err.Error(), "memory tide: ") || !strings.Contains(err.Error(), test.refusal) {
				t.Fatalf("ruling the fact for a project was not refused with the entry named: %v", err)
			}
			requireCatalogNeverWritten(t, dataDir, before)
		})
	}
}

func TestSyncLoadsAColdFactThatGainsAMarkerTheContractKeepsWarm(t *testing.T) {
	for name, marker := range map[string]string{"grace": "<!--g-->", "pinned": "<!--P-->"} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dataDir, stateDir := scopedHome(t)
			if _, err := Sync(dataDir, stateDir, referenceDate); err != nil {
				t.Fatal(err)
			}
			synced, err := Load(dataDir, referenceDate)
			if err != nil {
				t.Fatal(err)
			}
			if cache := entryOf(t, synced, "cache"); cache.Location != "cold" {
				t.Fatalf("the fact was not synced cold, so this test proves nothing: %+v", cache)
			}
			cache, err := os.ReadFile(filepath.Join(dataDir, "memory", "cache.md"))
			if err != nil {
				t.Fatal(err)
			}
			writeMemoryFile(t, dataDir, "memory/cache.md", append(cache, []byte(marker+"\n")...))

			// Act
			report, err := Sync(dataDir, stateDir, referenceDate)

			// Assert
			if err != nil || report.Added != 0 || len(report.Dropped) != 0 {
				t.Fatalf("a fact that gained the marker stopped the sync or was taken for a new one: %+v, %v", report, err)
			}
			catalog, err := Load(dataDir, referenceDate)
			if err != nil {
				t.Fatal(err)
			}
			startup, err := catalog.StartupMap()
			if err != nil || !strings.Contains(startup, "Use the second drive.") {
				t.Fatalf("the fact does not load at startup once it carries the marker: %q, %v", startup, err)
			}
		})
	}
}

func TestSyncLoadsASelfPinnedFactFileOnItsFirstSyncAndLeavesTheArchiveCold(t *testing.T) {
	// Oracle: the catalog schema: a pinned item is cold only on its own
	// approval, and the archive's range is cold whatever its lines carry.
	// Arrange: a fact that ends in the pinned marker, which no index line
	// pins, and an archive whose last line was pinned when it was archived.
	dataDir, stateDir := scopedHome(t)
	const tide = "---\nname: tide\ndescription: The tide table is in the office\nmetadata:\n  type: reference\n---\n\nAsk the office for the tide table. <!--P-->\n"
	writeMemoryFile(t, dataDir, "memory/tide.md", []byte(tide))
	writeMemoryFile(t, dataDir, "memory-archive.md", []byte("## Archived 2026-09-01\n\n- An old port note <!--P-->\n"))

	// Act
	_, err := Sync(dataDir, stateDir, referenceDate)

	// Assert
	if err != nil {
		t.Fatalf("a self-pinned fact file stopped the first sync: %v", err)
	}
	catalog, err := Load(dataDir, referenceDate)
	if err != nil {
		t.Fatal(err)
	}
	startup, err := catalog.StartupMap()
	if err != nil || !strings.Contains(startup, "[tide]\n"+tide) {
		t.Fatalf("the startup map does not hold the fact's id and exact bytes: %q, %v", startup, err)
	}
	if archive := entryOf(t, catalog, "memory-archive"); archive.Location != "cold" || strings.Contains(startup, "An old port note") {
		t.Fatalf("the archive did not sync cold: %+v, %q", archive, startup)
	}
}

func TestSyncKeepsAPinnedFactWhereTheCatalogPutItWhenItsEntryCarriesItsOwnApproval(t *testing.T) {
	for _, location := range []string{"cold", "warm"} {
		t.Run(location, func(t *testing.T) {
			// Arrange: the fixture's approved offload, as a fact whose file
			// then gains the pinned marker.
			dataDir, catalog := catalogFixture(t)
			stateDir := t.TempDir()
			catalog.Sources[3].Entries[0].Authority, catalog.Sources[3].Entries[0].Location = "fact", location
			writeCatalog(t, dataDir, catalog)
			if _, err := Load(dataDir, referenceDate); err != nil {
				t.Fatalf("the fixture does not load, so this test proves nothing: %v", err)
			}
			const pinned = "A completed investigation. <!--P-->\n"
			writeMemoryFile(t, dataDir, "memory/history.md", []byte(pinned))

			// Act
			_, err := Sync(dataDir, stateDir, referenceDate)

			// Assert
			if err != nil {
				t.Fatalf("an approved pinned fact stopped the sync: %v", err)
			}
			synced, err := Load(dataDir, referenceDate)
			if err != nil {
				t.Fatal(err)
			}
			startup, err := synced.StartupMap()
			if kept := entryOf(t, synced, "history"); err != nil || kept.Location != location || kept.Approval == nil || strings.Contains(startup, "A completed investigation.") != (location == "warm") {
				t.Fatalf("an approved pinned fact did not stay %s with its approval: %+v, %q, %v", location, kept, startup, err)
			}
			exact, err := synced.Retrieve(Query{ID: "history"})
			if err != nil || !strings.Contains(exact, " tier=pinned ") || !strings.HasSuffix(exact, "\n"+pinned+"\n") {
				t.Fatalf("the approved pinned fact is not retrievable verbatim by its id: %q, %v", exact, err)
			}
		})
	}
}

func TestSyncSendsAFactBackToItsScopeOnceTheMarkerThatLoadedItIsGone(t *testing.T) {
	// Oracle: rulings by scope: a fact file loads only for a reason a sync
	// can read, so one a marker loaded comes by scope again, with the
	// routing it had, once its words change and the marker is gone.
	for name, test := range map[string]struct{ loaded, left, expiry string }{
		"pending capture": {"<!--a:2026-10-30--><!--t:team-->", "<!--a:2026-10-30-->", ""},
		"grace":           {"<!--g-->", "<!--a:2026-10-30-->", ""},
		"perishable":      {"<!--p:2026-10-30-->", "<!--a:2026-10-30-->", "the second drive"},
		"pinned":          {"<!--P-->", "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange: a fact synced cold, then loaded for its marker.
			dataDir, stateDir := scopedHome(t)
			if _, err := Sync(dataDir, stateDir, referenceDate); err != nil {
				t.Fatal(err)
			}
			synced, err := Load(dataDir, referenceDate)
			if err != nil {
				t.Fatal(err)
			}
			routed := entryOf(t, synced, "cache")
			cache, err := os.ReadFile(filepath.Join(dataDir, "memory", "cache.md"))
			if err != nil {
				t.Fatal(err)
			}
			writeMemoryFile(t, dataDir, "memory/cache.md", append(slices.Clone(cache), []byte(test.loaded+"\n")...))
			_, err = Sync(dataDir, stateDir, referenceDate)
			if test.expiry != "" {
				_, err = Scope(dataDir, stateDir, referenceDate, Tag{Expiries: map[string]string{"cache": test.expiry}})
			}
			if err != nil {
				t.Fatal(err)
			}
			marked, err := Load(dataDir, referenceDate)
			if err != nil {
				t.Fatal(err)
			}
			if held := entryOf(t, marked, "cache"); routed.Location != "cold" || held.Location != "warm" {
				t.Fatalf("the fact was not synced cold and then loaded for its marker, so this test proves nothing: %+v, %+v", routed, held)
			}
			writeMemoryFile(t, dataDir, "memory/cache.md", append(slices.Clone(cache), []byte(test.left+"\n")...))

			// Act
			report, err := Sync(dataDir, stateDir, referenceDate)

			// Assert
			if err != nil || report.Added != 0 || len(report.Dropped) != 0 {
				t.Fatalf("the fact whose marker left stopped the sync or was taken for a new one: %+v, %v", report, err)
			}
			catalog, err := Load(dataDir, referenceDate)
			if err != nil {
				t.Fatal(err)
			}
			back := entryOf(t, catalog, "cache")
			if back.Location != "cold" || back.Trigger != routed.Trigger || !slices.Equal(back.Topics, routed.Topics) || !slices.Equal(back.Projects, routed.Projects) {
				t.Fatalf("the fact did not come by scope again with the routing it had: %+v, which was %+v", back, routed)
			}
			startup, err := catalog.StartupMap()
			if err != nil || strings.Contains(startup, "Use the second drive.") {
				t.Fatalf("the fact still loads at startup once its marker is gone: %q, %v", startup, err)
			}
			scoped, err := catalog.Retrieve(Query{Topic: "project", IsColdOnly: true})
			if err != nil || !strings.Contains(scoped, "Use the second drive.") {
				t.Fatalf("the fact does not come with its scope: %q, %v", scoped, err)
			}
		})
	}
}

func TestSyncKeepsAChangedFactLoadedWhileASyncCanReadWhy(t *testing.T) {
	// Oracle: rulings by scope: a ruling for the whole fleet, an index pin
	// and an active or held task state each keep a fact loaded when its
	// words change, and so does having nothing a scope would find it by; an
	// entry set by hand as something other than a fact stays as it was set.
	setByHand := func(t *testing.T, dataDir, id string, change func(*Entry)) {
		t.Helper()
		content, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(CatalogPath)))
		if err != nil {
			t.Fatal(err)
		}
		var catalog Catalog
		if err := Decode(content, &catalog); err != nil {
			t.Fatal(err)
		}
		for _, source := range catalog.Sources {
			for position := range source.Entries {
				if source.Entries[position].ID == id {
					change(&source.Entries[position])
				}
			}
		}
		writeCatalog(t, dataDir, catalog)
	}
	for name, test := range map[string]struct {
		fact, words string
		hold        func(t *testing.T, dataDir, stateDir string)
	}{
		"ruled for the whole fleet": {"cache", "Use the second drive.", func(t *testing.T, dataDir, stateDir string) {
			if _, err := Scope(dataDir, stateDir, referenceDate, Tag{IDs: []string{"cache"}, IsFleet: true}); err != nil {
				t.Fatal(err)
			}
		}},
		"pinned by the index": {"owner", "He approves each release.", func(*testing.T, string, string) {}},
		"an active task's constraint": {"cache", "Use the second drive.", func(t *testing.T, dataDir, _ string) {
			setByHand(t, dataDir, "cache", func(entry *Entry) { entry.Location, entry.TaskState = "warm", "active" })
		}},
		"a held task's constraint": {"cache", "Use the second drive.", func(t *testing.T, dataDir, _ string) {
			setByHand(t, dataDir, "cache", func(entry *Entry) { entry.Location, entry.TaskState = "warm", "held" })
		}},
		"a project note set to load by hand": {"cache", "Use the second drive.", func(t *testing.T, dataDir, _ string) {
			setByHand(t, dataDir, "cache", func(entry *Entry) { entry.Location, entry.Authority = "warm", "project" })
		}},
		"no trigger to find it by": {"cache", "Use the second drive.", func(t *testing.T, dataDir, _ string) {
			setByHand(t, dataDir, "cache", func(entry *Entry) { entry.Location, entry.Trigger = "warm", "" })
		}},
		"no routing key to find it by": {"cache", "Use the second drive.", func(t *testing.T, dataDir, _ string) {
			setByHand(t, dataDir, "cache", func(entry *Entry) { entry.Location, entry.Topics = "warm", nil })
		}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dataDir, stateDir := scopedHome(t)
			if _, err := Sync(dataDir, stateDir, referenceDate); err != nil {
				t.Fatal(err)
			}
			test.hold(t, dataDir, stateDir)
			held, err := Load(dataDir, referenceDate)
			if err != nil {
				t.Fatal(err)
			}
			if entry := entryOf(t, held, test.fact); entry.Location != "warm" {
				t.Fatalf("the fact does not load before its words change, so this test proves nothing: %+v", entry)
			}
			path := filepath.Join(dataDir, "memory", test.fact+".md")
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			writeMemoryFile(t, dataDir, "memory/"+test.fact+".md", append(content, []byte("Checked again in November.\n")...))

			// Act
			_, err = Sync(dataDir, stateDir, referenceDate)

			// Assert
			if err != nil {
				t.Fatalf("the changed fact stopped the sync: %v", err)
			}
			catalog, err := Load(dataDir, referenceDate)
			if err != nil {
				t.Fatal(err)
			}
			startup, err := catalog.StartupMap()
			if entry := entryOf(t, catalog, test.fact); err != nil || entry.Location != "warm" || !strings.Contains(startup, test.words) || !strings.Contains(startup, "Checked again in November.") {
				t.Fatalf("the fact left startup when its words changed: %+v, %q, %v", entry, startup, err)
			}
		})
	}
}

func TestSyncNumbersANewFactWhoseNameAnotherFilesEntryAlreadyHas(t *testing.T) {
	// Oracle: the catalog schema: an id is unique, so a second entry of the
	// same name takes a number and the first keeps the id it is retrieved by.
	for name, edited := range map[string]string{
		"the first file unchanged": "",
		"the first file edited":    "- lighthouse: the lighthouse service\n",
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dataDir, stateDir := scopedHome(t)
			const projects = "# Projects\n\n- harbor: the harbor service\n"
			writeMemoryFile(t, dataDir, "projects.md", []byte(projects))
			if _, err := Sync(dataDir, stateDir, referenceDate); err != nil {
				t.Fatal(err)
			}
			writeMemoryFile(t, dataDir, "projects.md", []byte(projects+edited))
			writeMemoryFile(t, dataDir, "memory/projects.md", []byte("---\nname: projects\ndescription: How the project map is kept\nmetadata:\n  type: reference\n---\n\nOne line a project.\n"))

			// Act
			report, err := Sync(dataDir, stateDir, referenceDate)

			// Assert
			if err != nil || report.Added != 1 || len(report.Dropped) != 0 {
				t.Fatalf("the sync did not add the one new fact and drop nothing: %+v, %v", report, err)
			}
			catalog, err := Load(dataDir, referenceDate)
			if err != nil {
				t.Fatal(err)
			}
			ids := map[string]string{}
			for _, source := range catalog.Sources {
				if len(source.Entries) == 1 {
					ids[source.Path] = source.Entries[0].ID
				}
			}
			if ids["projects.md"] != "projects" || ids["memory/projects.md"] != "projects-2" {
				t.Fatalf("the project map did not keep its id with the new fact numbered after it: %v", ids)
			}
			exact, err := catalog.Retrieve(Query{ID: "projects"})
			if err != nil || !strings.HasSuffix(exact, "\n"+projects+edited+"\n") {
				t.Fatalf("the id no longer retrieves the project map: %q, %v", exact, err)
			}
		})
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
