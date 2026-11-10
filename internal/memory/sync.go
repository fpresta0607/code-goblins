package memory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/lock"
)

// Tag is a scope ruled for the entries its ids name: a ruling is a project's
// or the whole fleet's. Expiry names, in an entry's own words, when a
// perishable entry ends.
type Tag struct {
	IDs      []string
	Projects []string
	IsFleet  bool
	Expiry   string
}

// SyncReport counts what a rewrite of the catalog left in it. Dropped names
// the entries that carried a scope and whose words are no longer in memory.
type SyncReport struct {
	Entries int      `json:"entries"`
	Warm    int      `json:"warm"`
	Cold    int      `json:"cold"`
	Added   int      `json:"added"`
	Dropped []string `json:"dropped"`
	Tagged  int      `json:"tagged"`
}

// Sync derives the startup catalog again from the memory files, which stay
// the source of truth: every byte of each is listed once, a ruling line
// under the id recall prints for it. A file that has not changed keeps its
// entries as they were set, a line that is still there keeps its scope by
// its id, and words the catalog has not seen load at startup until a scope
// is ruled for them. A catalog that would not load is not written.
func Sync(dataDir, stateDir string, now time.Time) (SyncReport, error) {
	return rewriteCatalog(dataDir, stateDir, now, nil)
}

// Scope syncs the catalog and rules a scope for the entries tag names. A
// ruling of a project leaves startup and comes with that project's scope; it
// stays verbatim in its file, where its id still retrieves it.
func Scope(dataDir, stateDir string, now time.Time, tag Tag) (SyncReport, error) {
	if len(tag.IDs) == 0 {
		return SyncReport{}, errors.New("name each entry to tag by its id")
	}
	if tag.IsFleet && len(tag.Projects) > 0 {
		return SyncReport{}, errors.New("an entry is a project's or the whole fleet's, never both")
	}
	if !tag.IsFleet && len(tag.Projects) == 0 && tag.Expiry == "" {
		return SyncReport{}, errors.New("name a project, the whole fleet or an expiry to set")
	}
	return rewriteCatalog(dataDir, stateDir, now, &tag)
}

// lockCatalog takes the one lock every writer of the memory catalog holds,
// a decision on a lesson included, waiting out another writer.
func lockCatalog(stateDir, writer string) (func() error, error) {
	deciding.Lock()
	if _, err := lock.AcquireNamedOwnerWithin(stateDir, lessonLockName, os.Getpid(), writer, lessonLockWait); err != nil {
		deciding.Unlock()
		return nil, err
	}
	return func() error {
		defer deciding.Unlock()
		return lock.ReleaseNamed(stateDir, lessonLockName)
	}, nil
}

func rewriteCatalog(dataDir, stateDir string, now time.Time, tag *Tag) (report SyncReport, err error) {
	if _, err := os.Stat(dataDir); err != nil {
		return SyncReport{}, err
	}
	unlock, err := lockCatalog(stateDir, "memory sync")
	if errors.Is(err, lock.ErrHeld) {
		return SyncReport{}, fmt.Errorf("another memory write is still under way, so the catalog was left as it was: %w", err)
	}
	if err != nil {
		return SyncReport{}, fmt.Errorf("the memory catalog's lock could not be taken, so the catalog was left as it was: %w", err)
	}
	defer func() { err = errors.Join(err, unlock()) }()

	var previous Catalog
	before, err := ReadData(dataDir, CatalogPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		before = nil
	case err != nil:
		return SyncReport{}, err
	default:
		if err := Decode(before, &previous); err != nil {
			return SyncReport{}, fmt.Errorf("memory catalog: %w", err)
		}
		if previous.Version != 1 {
			return SyncReport{}, fmt.Errorf("memory catalog version %d is unsupported", previous.Version)
		}
	}
	names, err := GovernedFiles(dataDir)
	if err != nil {
		return SyncReport{}, err
	}
	pinned, err := pinnedReferences(dataDir)
	if err != nil {
		return SyncReport{}, err
	}
	// Startup reads the catalog in its order: what it read first before a
	// catalog still comes first.
	slices.SortStableFunc(names, func(left, right string) int { return startupRank(left) - startupRank(right) })

	cataloged := map[string]Source{}
	known := map[string]Entry{}
	for _, source := range previous.Sources {
		cataloged[source.Path] = source
		for _, entry := range source.Entries {
			known[entry.ID] = entry
		}
	}
	// A file that has not changed keeps its entries, and a file that was one
	// entry keeps that entry's id, so those ids are taken before any other
	// is derived.
	contents, used := map[string][]byte{}, map[string]bool{}
	for _, name := range names {
		content, err := ReadData(dataDir, name)
		if err != nil {
			return SyncReport{}, fmt.Errorf("memory source %s: %w", name, err)
		}
		contents[name] = content
		if source, isCataloged := cataloged[name]; isCataloged && (source.SHA256 == Hash(content) || len(source.Entries) == 1) {
			for _, entry := range source.Entries {
				used[entry.ID] = true
			}
		}
	}
	next := Catalog{Version: 1, Layers: previous.Layers}
	for _, name := range names {
		content := contents[name]
		source, isCataloged := cataloged[name]
		if !isCataloged || source.SHA256 != Hash(content) {
			source = Source{Path: name, SHA256: Hash(content), Entries: deriveEntries(name, content, source.Entries, used)}
		}
		// A fact the index pins loads unless that entry's own offload was
		// approved, whatever the catalog said before the pin.
		for position := range source.Entries {
			if entry := &source.Entries[position]; pinned[name] && entry.Location == "cold" && entry.Approval == nil {
				entry.Location = "warm"
			}
		}
		next.Sources = append(next.Sources, source)
	}
	if tag != nil {
		if report.Tagged, err = applyTag(&next, contents, *tag); err != nil {
			return SyncReport{}, err
		}
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "memory"), 0o755); err != nil {
		return SyncReport{}, err
	}
	if err := replaceCatalog(dataDir, next, before, now); err != nil {
		if errors.Is(err, errExpiryMissing) {
			return SyncReport{}, fmt.Errorf("the catalog would not load, so it was left as it was: %w; name the entry's expiry in its own words with cfo memory scope --expiry \"<words>\" <id>", err)
		}
		return SyncReport{}, fmt.Errorf("the catalog would not load, so it was left as it was: %w", err)
	}
	for _, source := range next.Sources {
		for _, entry := range source.Entries {
			report.Entries++
			if entry.Location == "cold" {
				report.Cold++
			} else {
				report.Warm++
			}
			if _, isKnown := known[entry.ID]; !isKnown {
				report.Added++
			}
			delete(known, entry.ID)
		}
	}
	for id, entry := range known {
		if entry.Location == "cold" || len(entry.Projects)+len(entry.Tasks)+len(entry.Topics) > 0 {
			report.Dropped = append(report.Dropped, id)
		}
	}
	slices.Sort(report.Dropped)
	return report, nil
}

func startupRank(name string) int {
	if rank := slices.Index([]string{"projects.md", "overlord.md", "memory/MEMORY.md"}, name); rank >= 0 {
		return rank
	}
	return 3
}

// deriveEntries lists every byte of a file that is new to the catalog or has
// changed. The rulings and the fact index are one entry a line, since each
// line is ruled, tiered and scoped by itself; any other file is one entry,
// which keeps what the catalog said of it when it was one entry before.
func deriveEntries(name string, content []byte, previous []Entry, used map[string]bool) []Entry {
	switch {
	case len(content) == 0:
		return nil
	case name == "overlord.md":
		return lineEntries(content, KindRuling, "ruling", previous, used)
	case name == "memory/MEMORY.md":
		return lineEntries(content, "index", "fact", previous, used)
	}
	entry := Entry{}
	switch {
	case len(previous) == 1:
		entry = previous[0]
	case name == "memory-archive.md":
		entry = Entry{ID: uniqueID("memory-archive", used), Authority: "history", Location: "cold", Trigger: "archived memory, which recall searches line by line", Topics: []string{"archive"}}
	case name == "projects.md":
		entry = Entry{ID: uniqueID("projects", used), Authority: "project", Location: "warm"}
	default:
		front, _, _ := frontmatterOf(strings.ReplaceAll(string(content), "\r\n", "\n"))
		stem := strings.TrimSuffix(path.Base(name), path.Ext(name))
		entry = Entry{ID: uniqueID(entryID(stem), used), Authority: "fact", Location: "cold", Trigger: strings.Join(strings.Fields(front.Description), " "), Topics: []string{"fact"}}
		if entry.Trigger == "" {
			entry.Trigger = stem
		}
		entry.Projects = append(stringsOf(front.Metadata["projects"]), stringsOf(front.Metadata["project"])...)
		if kinds := stringsOf(front.Metadata["type"]); len(kinds) > 0 {
			entry.Topics = kinds[:1]
		}
		// Entries set by hand no longer fit the file, so it loads until they
		// are set again.
		if len(previous) > 1 {
			entry.Location = "warm"
		}
	}
	entry.Start, entry.End, entry.SHA256 = 0, len(content), Hash(content)
	return []Entry{entry}
}

// lineEntries is one entry for each item line, under the id recall gives
// that line. A heading and what follows it belong to the entry of the first
// item below, and blank lines and comments to the item above, so every byte
// is in one entry; a repeated line takes a number after its id. An entry
// whose id the catalog already knew keeps what the catalog said of it.
func lineEntries(content []byte, kind, authority string, previous []Entry, used map[string]bool) []Entry {
	carried := make(map[string]Entry, len(previous))
	for _, entry := range previous {
		carried[entry.ID] = entry
	}
	var entries []Entry
	start, id := 0, ""
	closeAt := func(end int) {
		entry := Entry{ID: id, Authority: authority, Location: "warm"}
		if known, isCarried := carried[id]; isCarried {
			entry = known
		}
		entry.Start, entry.End, entry.SHA256 = start, end, Hash(content[start:end])
		entries = append(entries, entry)
		start, id = end, ""
	}
	for _, line := range linesOf(content) {
		if !line.IsItem && !line.IsHeading {
			continue
		}
		if id != "" {
			closeAt(line.Start)
		}
		if line.IsItem {
			id = uniqueID(lineID(kind, line.Heading, line.Text), used)
		}
	}
	switch {
	case id != "":
		closeAt(len(content))
	case len(entries) > 0:
		// Headings below the last item stay with it.
		last := &entries[len(entries)-1]
		last.End, last.SHA256 = len(content), Hash(content[last.Start:])
	default:
		id = uniqueID(kind+"-"+Hash(content)[:12], used)
		closeAt(len(content))
	}
	return entries
}

// entryID is a fact file's name as a catalog id.
func entryID(stem string) string {
	id := strings.Map(func(character rune) rune {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-' {
			return character
		}
		return '-'
	}, stem)
	if !memoryID.MatchString(id) {
		return "fact-" + id
	}
	return id
}

func uniqueID(id string, used map[string]bool) string {
	candidate := id
	for number := 2; used[candidate]; number++ {
		candidate = fmt.Sprintf("%s-%d", id, number)
	}
	used[candidate] = true
	return candidate
}

// applyTag rules the tag's scope for each entry it names, and refuses the
// whole tag when an id names no entry.
func applyTag(catalog *Catalog, contents map[string][]byte, tag Tag) (int, error) {
	wanted := map[string]bool{}
	for _, id := range tag.IDs {
		wanted[id] = true
	}
	tagged := 0
	for _, source := range catalog.Sources {
		for position := range source.Entries {
			entry := &source.Entries[position]
			if !wanted[entry.ID] {
				continue
			}
			delete(wanted, entry.ID)
			tagged++
			if tag.Expiry != "" {
				entry.Expiry = tag.Expiry
			}
			switch {
			case tag.IsFleet:
				entry.Location, entry.Projects = "warm", nil
				if !slices.Contains(entry.Topics, FleetTopic) {
					entry.Topics = append(slices.Clone(entry.Topics), FleetTopic)
				}
			case len(tag.Projects) > 0:
				entry.Location, entry.Projects = "cold", slices.Clone(tag.Projects)
				entry.Topics = slices.DeleteFunc(slices.Clone(entry.Topics), func(topic string) bool { return topic == FleetTopic })
				if strings.TrimSpace(entry.Trigger) == "" {
					entry.Trigger = cueOf(contents[source.Path], *entry)
				}
			}
		}
	}
	if len(wanted) > 0 {
		missing := make([]string, 0, len(wanted))
		for id := range wanted {
			missing = append(missing, id)
		}
		slices.Sort(missing)
		return 0, fmt.Errorf("no memory entry has the id %s; cfo memory scope --list shows the rulings still to classify, and cfo memory routes every cold entry", strings.Join(missing, ", "))
	}
	return tagged, nil
}

// cueOf is what a cold entry is found by: the heading its first item stands
// under, or that item's first words when it stands under none.
func cueOf(content []byte, entry Entry) string {
	for _, line := range linesOf(content) {
		if line.Start >= entry.End {
			break
		}
		if !line.IsItem || line.Start < entry.Start {
			continue
		}
		if line.Heading != "" {
			return line.Heading
		}
		if words := []rune(line.Text); len(words) > 80 {
			return string(words[:80])
		}
		return line.Text
	}
	return entry.ID
}

// replaceCatalog writes the catalog and loads it, and puts back what was
// there, or nothing when there was none, if it does not load.
func replaceCatalog(dataDir string, catalog Catalog, before []byte, now time.Time) error {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(catalog); err != nil {
		return err
	}
	catalogPath := filepath.Join(dataDir, filepath.FromSlash(CatalogPath))
	if err := fsx.AtomicWriteFile(catalogPath, encoded.Bytes()); err != nil {
		return err
	}
	if _, err := Load(dataDir, now); err != nil {
		if before == nil {
			return errors.Join(err, os.Remove(catalogPath))
		}
		return errors.Join(err, fsx.AtomicWriteFile(catalogPath, before))
	}
	return nil
}
