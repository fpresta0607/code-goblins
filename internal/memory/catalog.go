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
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

const CatalogPath = "memory/startup.json"
const DefaultBudget = 10000

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
	Version int      `json:"version"`
	Sources []Source `json:"sources"`
	Layers  []Layer  `json:"layers,omitempty"`
	content map[string][]byte
	status  map[string]Status
}

type Query struct {
	ID      string
	Project string
	Task    string
	Topic   string
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
	catalog.content = make(map[string][]byte)
	catalog.status = make(map[string]Status)
	ids := make(map[string]Entry)
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
		covered := 0
		for _, entry := range source.Entries {
			if entry.ID == "" || strings.ContainsAny(entry.ID, " \t\r\n/\\\"") {
				return Catalog{}, fmt.Errorf("invalid memory id %q", entry.ID)
			}
			if _, exists := ids[entry.ID]; exists {
				return Catalog{}, fmt.Errorf("conflicting memory id %s", entry.ID)
			}
			if entry.Start != covered || entry.End <= entry.Start || entry.End > len(content) || !utf8.Valid(content[entry.Start:entry.End]) || Hash(content[entry.Start:entry.End]) != entry.SHA256 {
				return Catalog{}, fmt.Errorf("memory %s has a gap, overlap, invalid range or checksum", entry.ID)
			}
			covered = entry.End
			status, err := Inspect(content[entry.Start:entry.End], entry.Authority, now)
			if err != nil {
				return Catalog{}, fmt.Errorf("memory %s: %w", entry.ID, err)
			}
			if err := validateEntry(source.Path, entry, content[entry.Start:entry.End], status, now); err != nil {
				return Catalog{}, fmt.Errorf("memory %s: %w", entry.ID, err)
			}
			catalog.status[entry.ID] = status
			ids[entry.ID] = entry
		}
		if covered != len(content) {
			return Catalog{}, fmt.Errorf("memory source %s has uncovered bytes", source.Path)
		}
	}
	if len(catalog.content) != len(names) {
		return Catalog{}, errors.New("memory catalog omits governed sources; run cfo memory migration-plan")
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
	return catalog, nil
}

func validateEntry(source string, entry Entry, content []byte, status Status, now time.Time) error {
	if !slices.Contains([]string{"ruling", "fact", "project", "history"}, entry.Authority) || !slices.Contains([]string{"warm", "cold"}, entry.Location) {
		return errors.New("invalid authority or warm/cold location")
	}
	if status.Tier == "perishable" && (entry.Expiry == "" || !bytes.Contains(content, []byte(entry.Expiry))) {
		return errors.New("perishable memory needs its checkable expiry condition in the entry")
	}
	if entry.Location == "cold" {
		if entry.Trigger == "" || len(entry.Projects)+len(entry.Tasks)+len(entry.Topics) == 0 {
			return errors.New("cold memory needs a trigger and project, task or topic routing")
		}
		if status.PendingCapture != "" || status.IsGrace || status.IsLegacy || status.Tier == "perishable" {
			return errors.New("pending capture, grace, legacy or perishable memory must remain warm")
		}
		isExistingArchive := source == "memory-archive.md" && entry.Authority == "history"
		if (status.Tier == "pinned" || source == "overlord.md") && !isExistingArchive {
			approval := entry.Approval
			if approval == nil || approval.By != "Overlord" || approval.Quote == "" || approval.Destination != source+"#"+entry.ID || !strings.Contains(approval.Quote, entry.ID) {
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

func (catalog Catalog) Retrieve(query Query, now time.Time) (string, error) {
	var output strings.Builder
	if len(catalog.Sources) == 0 {
		output.WriteString("MEMORY SOURCES ABSENT: no governed source files; no authority was assumed read.\n")
	}
	for _, layer := range catalog.Layers {
		fmt.Fprintf(&output, "Memory tier %s owns %s; owner=%s\nCapture: %s\nCorrections: %s\nIf unreachable, keep pending capture explicit in warm memory and report it to the CFO.\n", layer.Name, strings.Join(layer.Classes, ", "), layer.Owner, layer.Capture, layer.Correction)
	}
	matched := false
	for _, source := range catalog.Sources {
		for _, entry := range source.Entries {
			isSelected := entry.Location == "warm" || slices.Contains(entry.Projects, query.Project) && query.Project != "" || slices.Contains(entry.Tasks, query.Task) && query.Task != "" || slices.Contains(entry.Topics, query.Topic) && query.Topic != ""
			if query.ID != "" {
				isSelected = entry.ID == query.ID
			}
			if !isSelected {
				continue
			}
			matched = true
			content := catalog.content[source.Path][entry.Start:entry.End]
			status, err := Inspect(content, entry.Authority, now)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&output, "\n[%s] %s bytes %d:%d sha256=%s authority=%s tier=%s\n", entry.ID, source.Path, entry.Start, entry.End, entry.SHA256, entry.Authority, status.Tier)
			if status.IsStale || status.IsLegacy || status.IsGrace || status.PendingCapture != "" {
				fmt.Fprintf(&output, "STOW REQUIRED: stale=%t legacy=%t grace=%t pending-capture=%s; retain until the owning pass resolves it.\n", status.IsStale, status.IsLegacy, status.IsGrace, status.PendingCapture)
			}
			if len(entry.Supersedes) > 0 {
				fmt.Fprintf(&output, "Supersession candidates (no automatic retirement): %s\n", strings.Join(entry.Supersedes, ", "))
			}
			output.Write(content)
			output.WriteByte('\n')
		}
	}
	if query.ID != "" && !matched {
		return "", fmt.Errorf("memory id %q is absent", query.ID)
	}
	return output.String(), nil
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
		fmt.Fprintf(&output, "%d additional cold routes: cfo memory routes lists all.\n", left)
	}
	return output.String()
}
