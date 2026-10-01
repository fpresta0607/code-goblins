package layout

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// Manifest maps every file under a data folder, by its slash-separated path
// there, to the SHA-256 of its content.
type Manifest map[string]string

// Write is one file a migration creates or changes, with its whole new
// content. Before is the hash of the content it replaces, empty for a new
// file; a changed file only ever gains lines.
type Write struct {
	Path    string
	Content []byte
	Before  string
	Why     string
}

// Migration is everything laying out a home's data would do, worked out
// without changing anything, with the proof that it keeps every file.
type Migration struct {
	Home home.Home
	Now  time.Time
	// Folders are the layout's folders to create, slash-separated.
	Folders []string
	// Moves are the task folders filed, exactly as the watcher files them.
	Moves []Move
	// Kept are the task folders left where they are, and why.
	Kept []Kept
	// Writes are the files created or changed, the layout marker last.
	Writes []Write
	// NotImported are memory files left out of the import, and why.
	NotImported []string
	// MemoryFrom is the harness memory folder imported from, empty when
	// there was none.
	MemoryFrom string
	// Before is the data folder now and After what the migration leaves.
	Before, After Manifest
	// Moved maps each moved file's path before to its path after.
	Moved map[string]string
}

var indexLink = regexp.MustCompile(`\]\(([^)\s]+)\)`)

// PlanMigration works out how to lay out h's data: the layout's folders,
// the filing of every finished task folder and stale brief, the backlog's
// Parked section, an import of the harness memory folder memoryFrom (empty
// for none), and the marker. It reads the harness folder and changes
// nothing anywhere. It fails, rather than returning a plan, if the plan
// would leave out any file there is now.
func PlanMigration(h home.Home, memoryFrom string, now time.Time) (Migration, error) {
	m := Migration{Home: h, Now: now, MemoryFrom: memoryFrom, Moved: map[string]string{}}
	var err error
	if m.Before, err = HashTree(h.Data); err != nil {
		return Migration{}, err
	}
	for _, folder := range folders {
		if info, err := os.Stat(filepath.Join(h.Data, filepath.FromSlash(folder))); err != nil || !info.IsDir() {
			m.Folders = append(m.Folders, folder)
		}
	}
	// The memory folder imported from is the harness memory whose text keeps
	// a finished folder in place; with none named, the watcher's own.
	harnessMemory := memoryFrom
	if harnessMemory == "" {
		harnessMemory = ClaudeMemoryFolder(h.Root)
	}
	if m.Moves, m.Kept, err = plan(h, harnessMemory, now); err != nil {
		return Migration{}, err
	}

	backlog, err := readOptional(filepath.Join(h.Data, Backlog))
	if err != nil {
		return Migration{}, err
	}
	next := backlog
	if next == nil {
		next = []byte(seeds[0].content)
	}
	var log []byte
	for _, move := range m.Moves {
		if move.Row != "" {
			next = withParkedRow(next, move.Row)
		}
		log = append(log, logLine(h, move, now)...)
	}
	next = withParkedSection(next)
	m.add(Backlog, next, "the backlog with its Queued, Parked and Done sections")
	if len(log) > 0 {
		current, err := readOptional(filepath.Join(h.Data, filepath.FromSlash(FilingLog)))
		if err != nil {
			return Migration{}, err
		}
		m.add(FilingLog, append(current, log...), "the record of every folder filed")
	}
	if err := m.planMemory(); err != nil {
		return Migration{}, err
	}
	if _, ok := m.Before[Marker]; !ok {
		m.add(Marker, []byte(markerText), "marks the data as laid out, so the watcher files it from now on")
	}

	m.After = m.predict()
	if dropped := m.dropped(); len(dropped) > 0 {
		return Migration{}, fmt.Errorf("layout: the migration plan would drop %d files, starting with %s", len(dropped), dropped[0])
	}
	return m, nil
}

// add records a write when content differs from what the file holds now.
func (m *Migration) add(rel string, content []byte, why string) {
	before := m.Before[rel]
	if before == hashBytes(content) {
		return
	}
	m.Writes = append(m.Writes, Write{Path: rel, Content: content, Before: before, Why: why})
}

// planMemory imports a harness memory folder into data/memory, once: each
// fact file the home does not have is copied byte for byte, the index gains a
// line for each fact it does not list, and a file the home already holds with
// other content is left as the home has it and reported. The index is
// stamped with the folder and the time it came from, and the folder itself
// is only read.
func (m *Migration) planMemory() error {
	if m.MemoryFrom == "" {
		return nil
	}
	stamp := "Imported once from " + m.MemoryFrom + " at " + m.Now.UTC().Format(time.RFC3339) + "; that folder was only read and is left as it was."
	entries, err := os.ReadDir(m.MemoryFrom)
	if err != nil {
		return fmt.Errorf("layout: read the memory folder to import: %w", err)
	}
	indexDir := path.Dir(MemoryIndex)
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() {
			m.NotImported = append(m.NotImported, name+": not a file")
			continue
		}
		source, err := fsx.ReadFile(filepath.Join(m.MemoryFrom, name))
		if err != nil {
			return err
		}
		rel := indexDir + "/" + name
		current, err := readOptional(filepath.Join(m.Home.Data, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		switch {
		case strings.EqualFold(rel, MemoryIndex) && current != nil:
			m.add(rel, withIndexLines(current, source, stamp), "the memory index, with a line for every imported fact")
		case current == nil && strings.EqualFold(rel, MemoryIndex):
			m.add(rel, stamped(source, stamp), "the memory index, copied from "+m.MemoryFrom+" and stamped with where and when")
		case current == nil:
			m.add(rel, source, "a memory fact, copied from "+m.MemoryFrom)
		case !bytes.Equal(current, source):
			m.NotImported = append(m.NotImported, name+": the home already has a different "+rel+", which stays as it is")
		}
	}
	return nil
}

// stamped returns an imported index with stamp as a paragraph of its own
// under its heading, or first when it has none.
func stamped(index []byte, stamp string) []byte {
	lines, newline := backlogLines(index)
	if strings.HasPrefix(lines[0], "#") {
		return joinLines(slices.Insert(lines, 1, "", stamp), newline)
	}
	return joinLines(slices.Insert(lines, 0, stamp, ""), newline)
}

// withIndexLines returns index with every line of source's index whose fact
// it does not link yet appended after stamp, so the index only ever gains
// lines.
func withIndexLines(index, source []byte, stamp string) []byte {
	linked := map[string]bool{}
	for _, match := range indexLink.FindAllSubmatch(index, -1) {
		linked[strings.ToLower(string(match[1]))] = true
	}
	next := bytes.TrimRight(index, "\r\n")
	newline := []byte("\n")
	if bytes.Contains(index, []byte("\r\n")) {
		newline = []byte("\r\n")
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(source), "\r\n", "\n"), "\n") {
		match := indexLink.FindStringSubmatch(line)
		if !strings.HasPrefix(strings.TrimSpace(line), "- ") || match == nil || linked[strings.ToLower(match[1])] {
			continue
		}
		linked[strings.ToLower(match[1])] = true
		if stamp != "" {
			next = append(append(next, newline...), stamp...)
			stamp = ""
		}
		next = append(append(next, newline...), line...)
	}
	return append(next, newline...)
}

// predict applies the plan to Before without touching the disk.
func (m *Migration) predict() Manifest {
	after := Manifest{}
	for rel, sum := range m.Before {
		to := rel
		for _, move := range m.Moves {
			from, dest := dataRel(m.Home, move.From), dataRel(m.Home, move.To)
			if strings.HasPrefix(strings.ToLower(rel), strings.ToLower(from)+"/") {
				to = dest + rel[len(from):]
				m.Moved[rel] = to
				break
			}
		}
		after[to] = sum
	}
	for _, write := range m.Writes {
		after[write.Path] = hashBytes(write.Content)
	}
	return after
}

// dropped lists every file there is now that the plan would not keep: it is
// kept when it is in After, at its new path if it moves, with the same hash,
// or when it is a written file whose new content keeps every line it had.
func (m *Migration) dropped() []string {
	written := map[string][]byte{}
	for _, write := range m.Writes {
		written[write.Path] = write.Content
	}
	var dropped []string
	for rel, sum := range m.Before {
		to := rel
		if moved, ok := m.Moved[rel]; ok {
			to = moved
		}
		if content, ok := written[to]; ok {
			old, err := fsx.ReadFile(filepath.Join(m.Home.Data, filepath.FromSlash(rel)))
			if err != nil || !keepsEveryLine(old, content) {
				dropped = append(dropped, rel)
			}
			continue
		}
		if m.After[to] != sum {
			dropped = append(dropped, rel)
		}
	}
	sort.Strings(dropped)
	return dropped
}

// Proof is the evidence that a migration keeps every file: how many files
// there are before and after and what happens to each, and a digest over
// the hashes of every file whose content the migration keeps, sorted, taken
// from the folder as it is and from the folder as the plan leaves it. Equal
// digests mean those files come through with the same contents, wherever
// they end up; a changed file is counted apart and keeps every line it had.
type Proof struct {
	Before, After                      int
	Moved, Changed, Created, Unchanged int
	BeforeDigest, AfterDigest          string
	Dropped                            []string
}

// Proof works out the migration's proof.
func (m Migration) Proof() Proof {
	proof := Proof{Before: len(m.Before), After: len(m.After), Moved: len(m.Moved), Dropped: m.dropped()}
	written := map[string]bool{}
	for _, write := range m.Writes {
		written[write.Path] = true
		if write.Before == "" {
			proof.Created++
		} else {
			proof.Changed++
		}
	}
	var before, after []string
	for rel, sum := range m.Before {
		to := rel
		if moved, ok := m.Moved[rel]; ok {
			to = moved
		}
		if !written[to] {
			before = append(before, sum)
		}
	}
	for rel, sum := range m.After {
		if !written[rel] {
			after = append(after, sum)
		}
	}
	proof.Unchanged = len(before) - proof.Moved
	proof.BeforeDigest, proof.AfterDigest = digest(before), digest(after)
	return proof
}

func digest(sums []string) string {
	sort.Strings(sums)
	return hashBytes([]byte(strings.Join(sums, "\n")))
}

// PlanTimePrefix starts a listing's first line, which gives the time the
// plan was made, so the same plan can be made again from it.
const PlanTimePrefix = "plan made at "

// Listing is the plan as lines: first the time it was made, then, sorted,
// every folder it creates, every file it moves with its SHA-256, every file
// it writes with the SHA-256 of its new content, and every task folder it
// leaves in place with why.
func (m Migration) Listing() []string {
	var lines []string
	for _, folder := range m.Folders {
		lines = append(lines, "create folder data/"+folder)
	}
	for from, to := range m.Moved {
		lines = append(lines, "move data/"+from+" -> data/"+to+" "+m.Before[from])
	}
	for _, write := range m.Writes {
		lines = append(lines, "write data/"+write.Path+" "+hashBytes(write.Content))
	}
	for _, kept := range m.Kept {
		lines = append(lines, "stays data/"+kept.ID+" ("+kept.Reason+")")
	}
	sort.Strings(lines)
	return append([]string{PlanTimePrefix + m.Now.UTC().Format(time.RFC3339)}, lines...)
}

// PlanDigest is the SHA-256 of the listing, one line after another. The same
// plan made again at the same time has the same digest only when it makes
// exactly the same change.
func (m Migration) PlanDigest() string {
	return hashBytes([]byte(strings.Join(m.Listing(), "\n")))
}

// keepsEveryLine reports whether next holds every line of old, in order.
func keepsEveryLine(old, next []byte) bool {
	lines := strings.Split(strings.ReplaceAll(string(next), "\r\n", "\n"), "\n")
	at := 0
	for _, line := range strings.Split(strings.TrimRight(strings.ReplaceAll(string(old), "\r\n", "\n"), "\n"), "\n") {
		for at < len(lines) && lines[at] != line {
			at++
		}
		if at == len(lines) {
			return false
		}
		at++
	}
	return true
}

// Result is what an applied migration did.
type Result struct {
	// BackedUp is every file the backup holds, as it was copied.
	BackedUp Manifest
	// Unexpected are files the migration did not touch that differ from
	// the plan afterwards, because something else, such as a live goblin
	// writing its own folder, changed them meanwhile.
	Unexpected []string
}

// Apply makes the migration after a full copy of the data folder into
// backupDir, each copy verified against its source. It refuses before
// changing anything when a file it writes, or any file in a folder it moves,
// is not what the plan read, checking both before and after the backup.
// A move that fails stops it: the moves made before it get their parked rows
// and filing log lines, nothing else is written, and a run after it plans the
// rest. Afterwards it reads the data folder back and fails when anything it
// moved or wrote differs from what the plan predicted.
func (m Migration) Apply(backupDir string) (Result, error) {
	var result Result
	if err := m.unchangedSincePlan(); err != nil {
		return result, err
	}
	var err error
	if result.BackedUp, err = backup(m.Home.Data, backupDir); err != nil {
		return result, fmt.Errorf("layout: back up %s: %w", m.Home.Data, err)
	}
	if err := m.unchangedSincePlan(); err != nil {
		return result, err
	}
	for _, folder := range m.Folders {
		if err := os.MkdirAll(filepath.Join(m.Home.Data, filepath.FromSlash(folder)), 0o755); err != nil {
			return result, err
		}
	}
	for i, move := range m.Moves {
		err := os.MkdirAll(filepath.Dir(move.To), 0o755)
		if err == nil {
			err = os.Rename(move.From, move.To)
		}
		if err != nil {
			return result, errors.Join(fmt.Errorf("layout: move %s: %w; the backup is %s", move.ID, err, backupDir), m.record(m.Moves[:i]))
		}
	}
	for _, write := range m.Writes {
		target := filepath.Join(m.Home.Data, filepath.FromSlash(write.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return result, err
		}
		if err := fsx.AtomicWriteFile(target, write.Content); err != nil {
			return result, fmt.Errorf("layout: write %s: %w; the backup is %s", write.Path, err, backupDir)
		}
	}
	actual, err := HashTree(m.Home.Data)
	if err != nil {
		return result, err
	}
	touched := map[string]bool{}
	for _, to := range m.Moved {
		touched[to] = true
	}
	for _, write := range m.Writes {
		touched[write.Path] = true
	}
	var differ []string
	for rel, sum := range m.After {
		switch {
		case actual[rel] == sum:
		case touched[rel]:
			differ = append(differ, rel)
		default:
			result.Unexpected = append(result.Unexpected, rel)
		}
	}
	for rel := range actual {
		if _, ok := m.After[rel]; !ok {
			result.Unexpected = append(result.Unexpected, rel)
		}
	}
	sort.Strings(differ)
	sort.Strings(result.Unexpected)
	if len(differ) > 0 {
		return result, fmt.Errorf("layout: after the migration %d files differ from the plan, starting with %s; the backup is %s", len(differ), differ[0], backupDir)
	}
	return result, nil
}

// record adds each move's parked row to the backlog and its line to the
// filing log, as the watcher does for each move it makes.
func (m Migration) record(moves []Move) error {
	for _, move := range moves {
		if move.Row != "" {
			if err := addParkedRow(filepath.Join(m.Home.Data, Backlog), move.Row); err != nil {
				return err
			}
		}
		if err := appendLog(filepath.Join(m.Home.Data, filepath.FromSlash(FilingLog)), logLine(m.Home, move, m.Now)); err != nil {
			return err
		}
	}
	return nil
}

// unchangedSincePlan refuses when a file the migration writes, or any file
// in a folder it moves, is not what the plan read.
func (m Migration) unchangedSincePlan() error {
	check := func(rel, want string) error {
		content, err := readOptional(filepath.Join(m.Home.Data, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		got := ""
		if content != nil {
			got = hashBytes(content)
		}
		if got != want {
			return fmt.Errorf("layout: %s changed since the plan; nothing was changed, run the migration again", rel)
		}
		return nil
	}
	for _, write := range m.Writes {
		if err := check(write.Path, write.Before); err != nil {
			return err
		}
	}
	for _, move := range m.Moves {
		now, err := HashTree(move.From)
		if err != nil {
			return err
		}
		from := dataRel(m.Home, move.From) + "/"
		planned := Manifest{}
		for rel, sum := range m.Before {
			if strings.HasPrefix(strings.ToLower(rel), strings.ToLower(from)) {
				planned[rel[len(from):]] = sum
			}
		}
		if !maps.Equal(now, planned) {
			return fmt.Errorf("layout: %s changed since the plan; nothing was changed, run the migration again", dataPath(m.Home, move.From))
		}
		if _, err := os.Stat(move.To); !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("layout: %s already exists; nothing was changed, run the migration again", dataPath(m.Home, move.To))
		}
	}
	return nil
}

// HashTree hashes every file under root. A link or any other entry that is
// neither a file nor a folder is refused, since copying or moving it is not
// something the migration can prove.
func HashTree(root string) (Manifest, error) {
	manifest := Manifest{}
	err := filepath.WalkDir(root, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p == root {
				return filepath.SkipDir
			}
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("layout: %s is neither a file nor a folder", p)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		sum, err := hashFile(p)
		if err != nil {
			return err
		}
		manifest[filepath.ToSlash(rel)] = sum
		return nil
	})
	return manifest, err
}

// backup copies every file under root into dir/data, keeping each file's
// modification time, verifies each copy against the bytes it was copied
// from, and returns what it holds.
func backup(root, dir string) (Manifest, error) {
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s already exists", dir)
	}
	current, err := HashTree(root)
	if err != nil {
		return nil, err
	}
	copied := Manifest{}
	for rel := range current {
		target := filepath.Join(dir, "data", filepath.FromSlash(rel))
		sum, err := copyFile(filepath.Join(root, filepath.FromSlash(rel)), target)
		if err != nil {
			return nil, err
		}
		check, err := hashFile(target)
		if err != nil {
			return nil, err
		}
		if check != sum {
			return nil, fmt.Errorf("the copy of %s does not match what was read", rel)
		}
		copied[rel] = sum
	}
	return copied, nil
}

// copyFile copies source to a new file target, keeping its modification
// time, and returns the hash of the bytes it copied.
func copyFile(source, target string) (string, error) {
	info, err := os.Stat(source)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	in, err := fsx.Open(source)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	sum := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, sum), in); err != nil {
		out.Close()
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), os.Chtimes(target, info.ModTime(), info.ModTime())
}

func hashFile(p string) (string, error) {
	file, err := fsx.Open(p)
	if err != nil {
		return "", err
	}
	defer file.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func readOptional(p string) ([]byte, error) {
	data, err := fsx.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

func dataRel(h home.Home, p string) string {
	return strings.TrimPrefix(dataPath(h, p), "data/")
}
