// Package memory validates source-backed startup memory and retrieves exact bytes.
package memory

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

const CatalogPath = "memory/startup.json"
const DefaultBudget = 15000

// FleetTopic is the routing key of a ruling ruled fleet-wide, which tells it
// from one no scope has been ruled for yet; both load at startup.
const FleetTopic = "fleet"

var memoryID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// errExpiryMissing is a perishable entry with no expiry named in the catalog,
// which only whoever curates the catalog can name.
var errExpiryMissing = errors.New("perishable memory needs its checkable expiry condition in the entry")

// expiryMissingError names every perishable entry a catalog names no expiry
// for, in the catalog's order, so one refusal names all there are to name.
type expiryMissingError struct{ ids []string }

func (missing *expiryMissingError) Error() string {
	return fmt.Sprintf("memory %s: %v", strings.Join(missing.ids, ", "), errExpiryMissing)
}

func (missing *expiryMissingError) Unwrap() error { return errExpiryMissing }

type Approval struct {
	By          string `json:"by"`
	Quote       string `json:"quote"`
	Date        string `json:"date"`
	Destination string `json:"destination"`
}

type Entry struct {
	ID         string    `json:"id"`
	Start      int       `json:"start"`
	End        int       `json:"end"`
	SHA256     string    `json:"sha256"`
	Authority  string    `json:"authority"`
	Location   string    `json:"location"`
	Trigger    string    `json:"trigger,omitempty"`
	Projects   []string  `json:"projects,omitempty"`
	Tasks      []string  `json:"tasks,omitempty"`
	Topics     []string  `json:"topics,omitempty"`
	Expiry     string    `json:"expiry,omitempty"`
	Supersedes []string  `json:"supersedes,omitempty"`
	Approval   *Approval `json:"approval,omitempty"`
	TaskState  string    `json:"task_state,omitempty"`
}

type Source struct {
	Path    string  `json:"path"`
	SHA256  string  `json:"sha256"`
	Entries []Entry `json:"entries"`
}

type Layer struct {
	Name       string   `json:"name"`
	Classes    []string `json:"classes"`
	Owner      string   `json:"owner"`
	Capture    string   `json:"capture"`
	Correction string   `json:"correction"`
}

type Catalog struct {
	Version  int      `json:"version"`
	Sources  []Source `json:"sources"`
	Layers   []Layer  `json:"layers,omitempty"`
	content  map[string][]byte
	status   map[string]Status
	dataDir  string
	checksum string
}

type Query struct {
	ID               string
	Project          string
	Task             string
	Topic            string
	Search           string
	Limit            int
	IncludeCompleted bool
	Delivered        []string
	// IsColdOnly leaves the warm entries out and prints what the scope or
	// search selects in the startup map's form, for a conversation that
	// holds the map.
	IsColdOnly bool
}

func Hash(content []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(content))
}

func Decode(data []byte, destination any) error {
	if err := uniqueJSONKeys(json.NewDecoder(bytes.NewReader(data))); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return errors.New("expected exactly one JSON value")
	}
	return nil
}

func uniqueJSONKeys(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('{') && token != json.Delim('[') {
		return nil
	}
	keys := make(map[string]bool)
	for decoder.More() {
		if token == json.Delim('{') {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, isString := keyToken.(string)
			if !isString || keys[key] {
				return fmt.Errorf("duplicate or invalid JSON key %q", key)
			}
			keys[key] = true
		}
		if err := uniqueJSONKeys(decoder); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

// ReadData refuses paths and symlinks outside the private data home.
func ReadData(dataDir, name string) ([]byte, error) {
	if !filepath.IsLocal(filepath.FromSlash(name)) || strings.Contains(name, "\\") || strings.Contains(name, ":") {
		return nil, fmt.Errorf("invalid data-relative path %q", name)
	}
	root, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(dataDir, filepath.FromSlash(name)))
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || !filepath.IsLocal(relative) {
		return nil, fmt.Errorf("memory path %q leaves the data home", name)
	}
	return fsx.ReadFile(resolved)
}

// GovernedFiles enumerates all source memory, including the recoverable archive.
func GovernedFiles(dataDir string) ([]string, error) {
	var names []string
	for _, name := range []string{"overlord.md", "projects.md", "memory-archive.md"} {
		_, err := os.Lstat(filepath.Join(dataDir, name))
		if err == nil {
			names = append(names, name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	err := filepath.WalkDir(filepath.Join(dataDir, "memory"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			relative, err := filepath.Rel(dataDir, path)
			if err != nil {
				return err
			}
			names = append(names, filepath.ToSlash(relative))
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	slices.Sort(names)
	return names, nil
}

func Load(dataDir string, now time.Time) (Catalog, error) {
	data, err := ReadData(dataDir, CatalogPath)
	if err != nil {
		return Catalog{}, err
	}
	catalog, err := validate(dataDir, data, now)
	if err != nil {
		return Catalog{}, err
	}
	if err := catalog.checkFresh(); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

// validate checks a catalog's bytes against the memory files as they stand,
// whether or not those bytes are on disk yet, so a writer learns that a
// catalog would not load before it writes it. Every perishable entry with no
// expiry named is reported in one error, after every other check has passed.
func validate(dataDir string, data []byte, now time.Time) (Catalog, error) {
	var catalog Catalog
	if err := Decode(data, &catalog); err != nil {
		return Catalog{}, fmt.Errorf("memory catalog: %w", err)
	}
	if catalog.Version != 1 {
		return Catalog{}, fmt.Errorf("memory catalog version %d is unsupported", catalog.Version)
	}
	names, err := GovernedFiles(dataDir)
	if err != nil {
		return Catalog{}, err
	}
	pinned, err := pinnedReferences(dataDir)
	if err != nil {
		return Catalog{}, err
	}
	catalog.content = make(map[string][]byte)
	catalog.status = make(map[string]Status)
	ids := make(map[string]Entry)
	var unexpiring []string
	for _, source := range catalog.Sources {
		if _, exists := catalog.content[source.Path]; exists || !slices.Contains(names, source.Path) {
			return Catalog{}, fmt.Errorf("duplicate or ungoverned source %q", source.Path)
		}
		content, err := ReadData(dataDir, source.Path)
		if err != nil {
			return Catalog{}, fmt.Errorf("memory source %s: %w", source.Path, err)
		}
		if !utf8.Valid(content) || Hash(content) != source.SHA256 {
			return Catalog{}, fmt.Errorf("memory source %s is corrupt or changed (sha256)", source.Path)
		}
		catalog.content[source.Path] = content
		isLiteral := literalBytes(content)
		covered := 0
		for _, entry := range source.Entries {
			if !memoryID.MatchString(entry.ID) {
				return Catalog{}, fmt.Errorf("invalid memory id %q", entry.ID)
			}
			if _, exists := ids[entry.ID]; exists {
				return Catalog{}, fmt.Errorf("conflicting memory id %s", entry.ID)
			}
			if entry.Start != covered || entry.End <= entry.Start || entry.End > len(content) || !utf8.Valid(content[entry.Start:entry.End]) || Hash(content[entry.Start:entry.End]) != entry.SHA256 {
				return Catalog{}, fmt.Errorf("memory %s has a gap, overlap, invalid range or checksum", entry.ID)
			}
			covered = entry.End
			status, err := statusOf(source.Path, entry, content, isLiteral, now)
			if err != nil {
				return Catalog{}, fmt.Errorf("memory %s: %w", entry.ID, err)
			}
			if pinned[source.Path] {
				if status.Tier != "pinned" && !status.IsLegacy {
					return Catalog{}, fmt.Errorf("memory %s has a tier marker conflicting with its index pin", entry.ID)
				}
				status.Tier = "pinned"
				status.IsLegacy, status.IsStale = false, false
			}
			switch err := validateEntry(source.Path, entry, content[entry.Start:entry.End], status, now); {
			case errors.Is(err, errExpiryMissing):
				unexpiring = append(unexpiring, entry.ID)
			case err != nil:
				return Catalog{}, fmt.Errorf("memory %s: %w", entry.ID, err)
			}
			catalog.status[entry.ID] = status
			ids[entry.ID] = entry
		}
		if covered != len(content) {
			return Catalog{}, fmt.Errorf("memory source %s has uncovered bytes", source.Path)
		}
	}
	var omitted []string
	for _, name := range names {
		if _, exists := catalog.content[name]; !exists {
			omitted = append(omitted, name)
		}
	}
	if len(omitted) > 0 {
		return Catalog{}, fmt.Errorf("memory catalog omits governed sources %s; catalog each one in %s", strings.Join(omitted, ", "), CatalogPath)
	}
	for _, entry := range ids {
		for _, previous := range entry.Supersedes {
			if _, exists := ids[previous]; !exists || previous == entry.ID {
				return Catalog{}, fmt.Errorf("memory %s supersedes missing or self entry %s", entry.ID, previous)
			}
		}
	}
	visited, active := make(map[string]bool), make(map[string]bool)
	var visit func(string) error
	visit = func(id string) error {
		if active[id] {
			return fmt.Errorf("conflicting supersession cycle at %s", id)
		}
		if visited[id] {
			return nil
		}
		active[id] = true
		for _, previous := range ids[id].Supersedes {
			if err := visit(previous); err != nil {
				return err
			}
		}
		active[id], visited[id] = false, true
		return nil
	}
	for id := range ids {
		if err := visit(id); err != nil {
			return Catalog{}, err
		}
	}
	if err := validateLayers(catalog.Layers); err != nil {
		return Catalog{}, err
	}
	if len(unexpiring) > 0 {
		return Catalog{}, &expiryMissingError{ids: unexpiring}
	}
	catalog.dataDir, catalog.checksum = dataDir, Hash(data)
	return catalog, nil
}

// statusOf is the tier an entry's own trailing markers give it. A marker in
// the archive is the tier a line had when it was archived: it is history now
// and tiers nothing.
func statusOf(source string, entry Entry, content []byte, isLiteral []bool, now time.Time) (Status, error) {
	if source == "memory-archive.md" && entry.Authority == "history" {
		return Status{Tier: "pinned"}, nil
	}
	return Inspect(content[entry.Start:entry.End], isLiteral[entry.Start:entry.End], entry.Authority, now)
}

// keepsWarm reports a trailing marker the contract never lets leave startup:
// pending capture, grace or perishable.
func (status Status) keepsWarm() bool {
	return status.PendingCapture != "" || status.IsGrace || status.Tier == "perishable"
}

// needsApproval reports an entry that leaves startup only on its own
// approval: a pinned item, and a ruling of overlord.md. History the archive
// already owns is cold without one, and so is a ruling routed to a project,
// which stays verbatim at its range and one retrieval away, a class the
// contract admits.
func needsApproval(source string, entry Entry, status Status) bool {
	isExistingArchive := source == "memory-archive.md" && entry.Authority == "history"
	isProjectRuling := source == "overlord.md" && len(entry.Projects) > 0
	return (status.Tier == "pinned" || source == "overlord.md") && !isExistingArchive && !isProjectRuling
}

func validateEntry(source string, entry Entry, content []byte, status Status, now time.Time) error {
	if !slices.Contains([]string{"", "active", "held", "completed"}, entry.TaskState) {
		return errors.New("invalid task_state; use active, held or completed")
	}
	if (entry.TaskState == "active" || entry.TaskState == "held") && entry.Location != "warm" {
		return errors.New("active and held task constraints must remain warm")
	}
	if !slices.Contains([]string{"ruling", "fact", "project", "history"}, entry.Authority) || !slices.Contains([]string{"warm", "cold"}, entry.Location) {
		return errors.New("invalid authority or warm/cold location")
	}
	if status.Tier == "perishable" && (entry.Expiry == "" || !bytes.Contains(content, []byte(entry.Expiry))) {
		return errExpiryMissing
	}
	for _, keys := range [][]string{entry.Projects, entry.Tasks, entry.Topics} {
		for _, key := range keys {
			if strings.TrimSpace(key) == "" {
				return errors.New("memory routing keys must contain nonblank text")
			}
		}
	}
	if entry.Location == "cold" {
		if strings.TrimSpace(entry.Trigger) == "" || len(entry.Projects)+len(entry.Tasks)+len(entry.Topics) == 0 {
			return errors.New("cold memory needs a trigger and project, task or topic routing")
		}
		// Before a catalog, startup loaded these three sources in full and no
		// other, so unmarked text stays loaded only where startup loaded it.
		isLoadedWithoutACatalog := source == "overlord.md" || source == "projects.md" || source == "memory/MEMORY.md"
		if status.keepsWarm() || status.IsLegacy && isLoadedWithoutACatalog {
			return errors.New("pending capture, grace, legacy or perishable memory must remain warm")
		}
		if needsApproval(source, entry, status) {
			approval := entry.Approval
			if approval == nil || approval.By != "Overlord" || approval.Destination != source+"#"+entry.ID {
				return errors.New("pinned offload requires this named item's verbatim Overlord approval and live source destination")
			}
			words := strings.FieldsFunc(approval.Quote, func(character rune) bool {
				return unicode.IsSpace(character) || strings.ContainsRune("`\"'()[]{}<>,;!?", character)
			})
			if !slices.ContainsFunc(words, func(word string) bool {
				candidate := strings.TrimSuffix(word, ".")
				return memoryID.MatchString(candidate) && candidate == entry.ID
			}) {
				return errors.New("pinned offload requires this named item's verbatim Overlord approval and live source destination")
			}
			date, err := time.Parse(time.DateOnly, approval.Date)
			if err != nil || date.After(now) {
				return errors.New("pinned offload approval has an invalid date")
			}
		}
	}
	return nil
}

func validateLayers(layers []Layer) error {
	names, classes := map[string]bool{}, map[string]bool{}
	for _, layer := range layers {
		if layer.Name == "" || names[layer.Name] || len(layer.Classes) == 0 || layer.Owner == "" || layer.Capture == "" || layer.Correction == "" {
			return errors.New("memory layer needs a unique name, classes, owner, capture and correction rules")
		}
		names[layer.Name] = true
		for _, class := range layer.Classes {
			if class == "" || classes[class] {
				return fmt.Errorf("conflicting memory layer class %q", class)
			}
			classes[class] = true
		}
	}
	return nil
}

func (catalog Catalog) Retrieve(query Query) (string, error) {
	if err := catalog.checkFresh(); err != nil {
		return "", err
	}
	selected, err := catalog.selectEntries(query)
	if err != nil {
		return "", err
	}
	var output strings.Builder
	catalog.writePreamble(&output)
	isShort := query.IsColdOnly && query.ID == ""
	if isShort && catalog.writeShort(&output, selected) == 0 && query.Search == "" {
		output.WriteString("No cold memory is routed to that scope.\n")
	}
	matched := false
	for _, source := range catalog.Sources {
		for _, entry := range source.Entries {
			if !selected[entry.ID] || isShort {
				continue
			}
			matched = true
			content := catalog.content[source.Path][entry.Start:entry.End]
			status := catalog.status[entry.ID]
			fingerprint := catalog.deliveryKey(entry.ID)
			isDelivered := query.ID == "" && slices.Contains(query.Delivered, fingerprint)
			if isDelivered {
				fmt.Fprintf(&output, "[%s] already delivered.\n", entry.ID)
			} else {
				fmt.Fprintf(&output, "\n[%s] %s bytes %d:%d sha256=%s authority=%s tier=%s delivery=%s", entry.ID, source.Path, entry.Start, entry.End, entry.SHA256, entry.Authority, status.Tier, fingerprint)
				if entry.TaskState != "" {
					fmt.Fprintf(&output, " task-state=%s", entry.TaskState)
				}
				output.WriteByte('\n')
			}
			if status.IsStale || status.IsLegacy || status.IsGrace || status.PendingCapture != "" {
				fmt.Fprintf(&output, "STOW REQUIRED: stale=%t legacy=%t grace=%t pending-capture=%s; retain until the owning pass resolves it.\n", status.IsStale, status.IsLegacy, status.IsGrace, status.PendingCapture)
			}
			if len(entry.Supersedes) > 0 {
				fmt.Fprintf(&output, "Supersession candidates (no automatic retirement): %s\n", strings.Join(entry.Supersedes, ", "))
			}
			if !isDelivered {
				output.Write(content)
				output.WriteByte('\n')
			}
		}
	}
	if query.ID != "" && !matched {
		return "", fmt.Errorf("memory id %q is absent", query.ID)
	}
	if query.Search != "" {
		cold := 0
		for _, source := range catalog.Sources {
			for _, entry := range source.Entries {
				if selected[entry.ID] && entry.Location == "cold" {
					cold++
				}
			}
		}
		if cold == 0 {
			output.WriteString("No cold search matches; try routing keys or an exact --id.\n")
		}
		output.WriteString("Search discovers source ranges, never resolves authority conflicts or retires superseded rulings.\n")
	}
	if err := catalog.checkFresh(); err != nil {
		return "", err
	}
	return output.String(), nil
}

// writePreamble says what every retrieval says first: that a home holds no
// memory, and who owns each declared tier.
func (catalog Catalog) writePreamble(output *strings.Builder) {
	if len(catalog.Sources) == 0 {
		output.WriteString("MEMORY SOURCES ABSENT: no governed source files; no authority was assumed read.\n")
	}
	for _, layer := range catalog.Layers {
		fmt.Fprintf(output, "Memory tier %s owns %s; owner=%s\nCapture: %s\nCorrections: %s\nIf unreachable, keep pending capture explicit in warm memory and report it to the CEO.\n", layer.Name, strings.Join(layer.Classes, ", "), layer.Owner, layer.Capture, layer.Correction)
	}
}

// Routes is bounded independently of the cold corpus, with an explicit remainder.
func (catalog Catalog) Routes(limit int) string {
	var output strings.Builder
	left := 0
	for _, source := range catalog.Sources {
		for _, entry := range source.Entries {
			if entry.Location != "cold" {
				continue
			}
			line := fmt.Sprintf("%s: %s (projects=%s tasks=%s topics=%s)\n", entry.ID, entry.Trigger, strings.Join(entry.Projects, ","), strings.Join(entry.Tasks, ","), strings.Join(entry.Topics, ","))
			if catalog.status[entry.ID].IsStale {
				line = "STALE; STOW REQUIRED: " + line
			}
			if output.Len()+len(line) > limit {
				left++
			} else {
				output.WriteString(line)
			}
		}
	}
	if left > 0 {
		fmt.Fprintf(&output, "%d additional cold routes: ceo memory routes lists all.\n", left)
	}
	return output.String()
}
