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
// or the whole fleet's. Expiries names, by a perishable entry's id and in
// that entry's own words, when it ends; an id it names is tagged as one of
// IDs is, so it need not be among them.
type Tag struct {
	IDs      []string
	Projects []string
	IsFleet  bool
	Expiries map[string]string
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
// is ruled for them. A catalog that would not load is never written.
func Sync(dataDir, stateDir string, now time.Time) (SyncReport, error) {
	return rewriteCatalog(dataDir, stateDir, now, nil)
}

// Scope syncs the catalog and rules a scope for the entries tag names, naming
// the expiry of each entry tag gives one. A ruling of a project leaves
// startup and comes with that project's scope; it stays verbatim in its file,
// where its id still retrieves it.
func Scope(dataDir, stateDir string, now time.Time, tag Tag) (SyncReport, error) {
	if len(tag.IDs)+len(tag.Expiries) == 0 {
		return SyncReport{}, errors.New("name each entry to tag by its id")
	}
	if tag.IsFleet && len(tag.Projects) > 0 {
		return SyncReport{}, errors.New("an entry is a project's or the whole fleet's, never both")
	}
	if !tag.IsFleet && len(tag.Projects) == 0 && slices.ContainsFunc(tag.IDs, func(id string) bool { return tag.Expiries[id] == "" }) {
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
	switch written, err := ReadData(dataDir, CatalogPath); {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return SyncReport{}, err
	default:
		if err := Decode(written, &previous); err != nil {
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
	// A file that has not changed keeps its entries, and a file cataloged
	// whole that was one entry keeps that entry's id, so those ids are taken
	// before any other is derived. A file cataloged a line at a time takes
	// each id from its line's words, so a changed one holds none back.
	contents, used := map[string][]byte{}, map[string]bool{}
	for _, name := range names {
		content, err := ReadData(dataDir, name)
		if err != nil {
			return SyncReport{}, fmt.Errorf("memory source %s: %w", name, err)
		}
		contents[name] = content
		isOneEntryFile := name != "overlord.md" && name != "memory/MEMORY.md"
		if source, isCataloged := cataloged[name]; isCataloged && (source.SHA256 == Hash(content) || isOneEntryFile && len(source.Entries) == 1) {
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
			source = Source{Path: name, SHA256: Hash(content), Entries: deriveEntries(name, content, source.Entries, used, now)}
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
	if err := replaceCatalog(dataDir, next, now); err != nil {
		var missing *expiryMissingError
		if errors.As(err, &missing) {
			var pairs strings.Builder
			for _, id := range missing.ids {
				fmt.Fprintf(&pairs, " --expiry %s=\"<words>\"", id)
			}
			return SyncReport{}, fmt.Errorf("the catalog would not load, so it was left as it was: %w; name each one's expiry in that entry's own words, all in one command: cfo memory scope%s", err, pairs.String())
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
// which keeps what the catalog said of it when it was one entry before. A
// file that ends in a marker the contract keeps warm loads, and so does one
// its own marker pins unless its entry carries an approval of its own,
// whatever the catalog said of either before, so no sync stops on a file
// nobody tagged.
func deriveEntries(name string, content []byte, previous []Entry, used map[string]bool, now time.Time) []Entry {
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
	// A marker that does not read is left for the load to report.
	if status, err := statusOf(name, entry, content, literalBytes(content), now); err == nil && (status.keepsWarm() || entry.Approval == nil && needsApproval(name, entry, status)) {
		entry.Location = "warm"
	}
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

// applyTag rules the tag's scope for each entry it names and names the
// expiry of each it gives one, and refuses the whole tag when an id names no
// entry.
func applyTag(catalog *Catalog, contents map[string][]byte, tag Tag) (int, error) {
	wanted := map[string]bool{}
	for _, id := range tag.IDs {
		wanted[id] = true
	}
	for id := range tag.Expiries {
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
			if expiry := tag.Expiries[entry.ID]; expiry != "" {
				entry.Expiry = expiry
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

// replaceCatalog writes the catalog once it is known to load against the
// memory files as they stand, so one that would not load is never written
// and whatever was there, or nothing, stays as it was.
func replaceCatalog(dataDir string, catalog Catalog, now time.Time) error {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(catalog); err != nil {
		return err
	}
	if _, err := validate(dataDir, encoded.Bytes(), now); err != nil {
		return err
	}
	catalogPath := filepath.Join(dataDir, filepath.FromSlash(CatalogPath))
	if err := os.MkdirAll(filepath.Dir(catalogPath), 0o755); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(catalogPath, encoded.Bytes())
}
