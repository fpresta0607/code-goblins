package memory

import (
	"strings"
	"testing"
)

// oneEntry catalogs a file as one entry over every byte of it.
func oneEntry(path, id, text string, entry Entry) Source {
	entry.ID, entry.End, entry.SHA256 = id, len(text), Hash([]byte(text))
	return Source{Path: path, SHA256: Hash([]byte(text)), Entries: []Entry{entry}}
}

func TestOnlyARulingRoutedToAProjectLoadsColdWithoutItsOwnApproval(t *testing.T) {
	const ruling = "Harbor keeps port 4300 free.\n"
	for _, test := range []struct {
		name    string
		path    string
		entry   Entry
		isValid bool
	}{
		{"a ruling routed to a project", "overlord.md", Entry{Authority: "ruling", Location: "cold", Trigger: "ports", Projects: []string{"harbor"}}, true},
		{"a ruling routed to a topic alone", "overlord.md", Entry{Authority: "ruling", Location: "cold", Trigger: "ports", Topics: []string{"ports"}}, false},
		{"a ruling routed to a task alone", "overlord.md", Entry{Authority: "ruling", Location: "cold", Trigger: "ports", Tasks: []string{"harbor-ports"}}, false},
		{"a ruling in another file routed to a project", "memory/ports.md", Entry{Authority: "ruling", Location: "cold", Trigger: "ports", Projects: []string{"harbor"}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			writeMemoryFile(t, dir, test.path, []byte(ruling))
			writeCatalog(t, dir, Catalog{Version: 1, Sources: []Source{oneEntry(test.path, "ports", ruling, test.entry)}})

			// Act
			catalog, err := Load(dir, referenceDate)

			// Assert
			if (err == nil) != test.isValid {
				t.Fatalf("loaded=%t, want %t: %v", err == nil, test.isValid, err)
			}
			if !test.isValid {
				return
			}
			exact, err := catalog.Retrieve(Query{ID: "ports"})
			if err != nil || !strings.HasSuffix(exact, "\n"+ruling+"\n") {
				t.Fatalf("a project's ruling is not retrievable verbatim by id: %q, %v", exact, err)
			}
		})
	}
}

func TestUnmarkedMemoryMayBeColdOnlyWhereNoStartupLoadedIt(t *testing.T) {
	const text = "A cache fact of unknown age.\n"
	for _, test := range []struct {
		path      string
		authority string
		isValid   bool
	}{
		{"memory/cache.md", "fact", true},
		{"memory/MEMORY.md", "fact", false},
		{"projects.md", "project", false},
	} {
		t.Run(test.path, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			writeMemoryFile(t, dir, test.path, []byte(text))
			writeCatalog(t, dir, Catalog{Version: 1, Sources: []Source{oneEntry(test.path, "cache", text, Entry{Authority: test.authority, Location: "cold", Trigger: "cache", Topics: []string{"cache"}})}})

			// Act
			catalog, err := Load(dir, referenceDate)

			// Assert
			if (err == nil) != test.isValid {
				t.Fatalf("loaded=%t, want %t: %v", err == nil, test.isValid, err)
			}
			if !test.isValid {
				return
			}
			scoped, err := catalog.Retrieve(Query{Topic: "cache"})
			if err != nil || !strings.Contains(scoped, "STOW REQUIRED: stale=false legacy=true") {
				t.Fatalf("a cold fact of unknown age lost its Stow diagnostic: %q, %v", scoped, err)
			}
		})
	}
}

func TestTheArchivesOwnMarkersTierNothingNow(t *testing.T) {
	for _, marker := range []string{"<!--p:2026-08-01-->", "<!--g-->", "<!--t:team-->", "<!--a:2099-01-01-->"} {
		t.Run(marker, func(t *testing.T) {
			// Arrange
			text := "- An old port note " + marker + "\n"
			dir := t.TempDir()
			writeMemoryFile(t, dir, "memory-archive.md", []byte(text))
			writeCatalog(t, dir, Catalog{Version: 1, Sources: []Source{oneEntry("memory-archive.md", "archive", text, Entry{Authority: "history", Location: "cold", Trigger: "archive recovery", Topics: []string{"archive"}})}})

			// Act
			catalog, err := Load(dir, referenceDate)

			// Assert
			if err != nil {
				t.Fatalf("the marker of an archived line stopped the archive loading cold: %v", err)
			}
			recovered, err := catalog.Retrieve(Query{ID: "archive"})
			if err != nil || !strings.Contains(recovered, text) || strings.Contains(recovered, "STOW REQUIRED") {
				t.Fatalf("the archive is not recovered as it stands: %q, %v", recovered, err)
			}
		})
	}
}

func TestAFactCatalogedInTheArchiveStillAnswersForItsMarker(t *testing.T) {
	// Arrange: only the archive's history is past tiering; a marker dated in
	// the future is an error wherever a live fact carries it.
	text := "- A port note <!--a:2099-01-01-->\n"
	dir := t.TempDir()
	writeMemoryFile(t, dir, "memory-archive.md", []byte(text))
	writeCatalog(t, dir, Catalog{Version: 1, Sources: []Source{oneEntry("memory-archive.md", "note", text, Entry{Authority: "fact", Location: "warm"})}})

	// Act
	_, err := Load(dir, referenceDate)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "memory note: ") {
		t.Fatalf("a live fact's marker went unread because of the file it is in: %v", err)
	}
}

func TestStartupMapNamesEachSourceOnceAndCountsWhatEachScopeHolds(t *testing.T) {
	// Arrange
	rulings := "## 2026-10-01 Releases\n\nThe release needs approval.\nHarbor ships on Fridays.\n\n## 2026-10-02 Ports\n\nHarbor keeps port 4300 free.\nEvery port is logged.\nEvery berth is numbered.\n"
	index := "- [Cache](cache.md) - where the cache lives <!--a:2026-09-01-->\n"
	cache := "Use the second drive.\n"
	dir := t.TempDir()
	writeMemoryFile(t, dir, "overlord.md", []byte(rulings))
	writeMemoryFile(t, dir, "memory/MEMORY.md", []byte(index))
	writeMemoryFile(t, dir, "memory/cache.md", []byte(cache))
	ranges := []Entry{
		{ID: "release", Start: 0, End: 52, Authority: "ruling", Location: "warm", Topics: []string{FleetTopic}},
		{ID: "fridays", Start: 52, End: 78, Authority: "ruling", Location: "cold", Trigger: "2026-10-01 Releases", Projects: []string{"harbor"}},
		{ID: "ports", Start: 78, End: 128, Authority: "ruling", Location: "cold", Trigger: "2026-10-02 Ports", Projects: []string{"harbor"}},
		{ID: "logged", Start: 128, End: 150, Authority: "ruling", Location: "warm"},
		{ID: "numbered", Start: 150, End: 175, Authority: "ruling", Location: "warm"},
	}
	for position, entry := range ranges {
		ranges[position].SHA256 = Hash([]byte(rulings[entry.Start:entry.End]))
	}
	writeCatalog(t, dir, Catalog{Version: 1, Sources: []Source{
		{Path: "overlord.md", SHA256: Hash([]byte(rulings)), Entries: ranges},
		oneEntry("memory/MEMORY.md", "index-cache", index, Entry{Authority: "fact", Location: "warm"}),
		oneEntry("memory/cache.md", "cache", cache, Entry{Authority: "fact", Location: "cold", Trigger: "where the cache lives", Topics: []string{"project"}}),
	}})
	catalog, err := Load(dir, referenceDate)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	startup, err := catalog.StartupMap()

	// Assert
	expected := "\noverlord.md\n" +
		"[release]\n## 2026-10-01 Releases\n\nThe release needs approval.\n" +
		"(under ## 2026-10-02 Ports)\n[logged] Every port is logged.\n[numbered] Every berth is numbered.\n" +
		"\nmemory/MEMORY.md\n" +
		"[index-cache stale] - [Cache](cache.md) - where the cache lives <!--a:2026-09-01-->\n" +
		"STOW REQUIRED: an entry marked stale, legacy, grace or pending-capture stays until the owning pass resolves it.\n" +
		"Left out above, by scope; each entry stays verbatim in its source:\n" +
		"project harbor: rulings 2\n" +
		"topic project: facts 1; Stow required for 1\n" +
		"Rulings not classified yet, which load until they are: 2; goblins memory scope --list shows them.\n"
	if err != nil || startup != expected {
		t.Fatalf("the startup map is not the independently written one:\n%q\nwant\n%q\n%v", startup, expected, err)
	}
}

func TestScopesAreBoundedWithTheRestCounted(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	catalog := Catalog{Version: 1}
	for _, name := range []string{"anchor", "berth", "crane"} {
		text := "A note on the " + name + ". <!--a:2026-10-30-->\n"
		writeMemoryFile(t, dir, "memory/"+name+".md", []byte(text))
		catalog.Sources = append(catalog.Sources, oneEntry("memory/"+name+".md", name, text, Entry{Authority: "fact", Location: "cold", Trigger: name, Projects: []string{name}}))
	}
	writeCatalog(t, dir, catalog)
	loaded, err := Load(dir, referenceDate)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	scopes := loaded.Scopes(len("Left out above, by scope; each entry stays verbatim in its source:\nproject anchor: facts 1\n"))

	// Assert
	if scopes != "Left out above, by scope; each entry stays verbatim in its source:\nproject anchor: facts 1\n2 more scopes: goblins memory routes lists every cold entry.\n" {
		t.Fatalf("scopes past the room were dropped uncounted: %q", scopes)
	}
}
