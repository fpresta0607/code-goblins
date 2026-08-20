package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// backupSuffix names the copy taken before this package writes a settings
// file. The file it guards is someone's personal Claude Code configuration,
// and an adopter who loses their own hooks to our installer has been
// betrayed, so the copy is taken before every write, not only the first.
const backupSuffix = ".cfo-install.bak"

// settingsFile is one Claude Code settings document held as decoded JSON.
// Every key is kept, known or not: this package only ever adds and removes
// its own hook entries, and rewrites everything else exactly as it read it.
type settingsFile struct {
	path   string
	values map[string]any
	exists bool
	// raw is the file exactly as it was read, which is what a backup must
	// contain; baseline is that same content re-rendered, which is what a
	// change is measured against. Comparing renderings rather than bytes is
	// what keeps a rerun a no-op on a file an adopter formats differently
	// from the way encoding/json would.
	raw      []byte
	baseline []byte
}

// loadSettings reads a settings document. A missing file is not an error -
// an adopter with no user settings yet is the ordinary first-install case -
// but a file that is not a JSON object is, because merging into it would
// mean guessing at what to preserve.
func loadSettings(path string) (*settingsFile, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &settingsFile{path: path, values: map[string]any{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("install: read %s: %w", path, err)
	}
	values := map[string]any{}
	// A settings file that has been through Notepad carries a UTF-8 BOM,
	// which encoding/json rejects. Refusing over it would be a confusing
	// answer to a file that is otherwise perfectly good JSON.
	body := bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
	if len(bytes.TrimSpace(body)) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(body))
		// Numbers stay as their source literals so a timeout an adopter
		// wrote as 28800 is rewritten as 28800 and not as 2.88e+04.
		decoder.UseNumber()
		if err := decoder.Decode(&values); err != nil {
			return nil, fmt.Errorf("install: %s is not a JSON object: %w", path, err)
		}
	}
	file := &settingsFile{path: path, values: values, exists: true, raw: raw}
	baseline, err := file.render()
	if err != nil {
		return nil, err
	}
	file.baseline = baseline
	return file, nil
}

// render serializes the document the way it will be written.
func (f *settingsFile) render() ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(f.values); err != nil {
		return nil, fmt.Errorf("install: render %s: %w", f.path, err)
	}
	return buffer.Bytes(), nil
}

// save backs the file up and writes it, and reports whether it wrote at all.
// An unchanged document is left alone entirely, which is what makes a second
// `cfo install` a genuine no-op rather than a rewrite that happens to land
// on the same bytes.
func (f *settingsFile) save() (changed bool, backup string, err error) {
	if !f.exists && len(f.values) == 0 {
		// An uninstall on a machine that never had a settings file must not
		// leave one behind.
		return false, "", nil
	}
	rendered, err := f.render()
	if err != nil {
		return false, "", err
	}
	if f.exists && bytes.Equal(rendered, f.baseline) {
		return false, "", nil
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		return false, "", fmt.Errorf("install: create %s: %w", filepath.Dir(f.path), err)
	}
	if f.exists {
		backup = f.path + backupSuffix
		if err := fsx.AtomicWriteFile(backup, f.raw); err != nil {
			return false, "", fmt.Errorf("install: back up %s: %w", f.path, err)
		}
	}
	if err := fsx.AtomicWriteFile(f.path, rendered); err != nil {
		return false, "", fmt.Errorf("install: write %s: %w", f.path, err)
	}
	f.exists = true
	f.raw = rendered
	f.baseline = rendered
	return true, backup, nil
}

// hookEvents returns the document's hooks object, or nil when it has none.
// A `hooks` key that is not an object is a malformed document: refusing is
// the only safe answer, because the alternative is overwriting whatever an
// adopter actually meant by it.
func (f *settingsFile) hookEvents() (map[string]any, error) {
	raw, ok := f.values["hooks"]
	if !ok || raw == nil {
		return nil, nil
	}
	events, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("install: %s has a \"hooks\" key that is not an object", f.path)
	}
	return events, nil
}

// pruneCFOHooks removes every hook entry this package wrote, and nothing
// else. Groups and events left empty by the removal are dropped so an
// uninstall leaves no hollow scaffolding behind.
func (f *settingsFile) pruneCFOHooks() error {
	events, err := f.hookEvents()
	if err != nil || events == nil {
		return err
	}
	for event, raw := range events {
		groups, ok := raw.([]any)
		if !ok {
			continue
		}
		kept := make([]any, 0, len(groups))
		for _, rawGroup := range groups {
			group, ok := rawGroup.(map[string]any)
			if !ok {
				kept = append(kept, rawGroup)
				continue
			}
			entries, ok := group["hooks"].([]any)
			if !ok {
				kept = append(kept, rawGroup)
				continue
			}
			keptEntries := make([]any, 0, len(entries))
			for _, rawEntry := range entries {
				if isCFOEntry(rawEntry) {
					continue
				}
				keptEntries = append(keptEntries, rawEntry)
			}
			if len(keptEntries) == 0 {
				continue
			}
			group["hooks"] = keptEntries
			kept = append(kept, group)
		}
		if len(kept) == 0 {
			delete(events, event)
			continue
		}
		events[event] = kept
	}
	if len(events) == 0 {
		delete(f.values, "hooks")
	}
	return nil
}

// addCFOHooks appends the CFO hook groups, leaving every group already in
// the document in place and ahead of them.
func (f *settingsFile) addCFOHooks() error {
	events, err := f.hookEvents()
	if err != nil {
		return err
	}
	if events == nil {
		events = map[string]any{}
		f.values["hooks"] = events
	}
	for _, group := range cfoHookGroups() {
		existing, ok := events[group.event].([]any)
		if !ok && events[group.event] != nil {
			return fmt.Errorf("install: %s has a %q hook list that is not an array", f.path, group.event)
		}
		rendered := map[string]any{}
		if group.matcher != "" {
			rendered["matcher"] = group.matcher
		}
		entries := make([]any, 0, len(group.entries))
		for _, entry := range group.entries {
			entries = append(entries, entry)
		}
		rendered["hooks"] = entries
		events[group.event] = append(existing, rendered)
	}
	return nil
}

// foreignHookCount counts the hook entries that are not ours, which is what
// the install report means by "left alone".
func (f *settingsFile) foreignHookCount() int {
	events, err := f.hookEvents()
	if err != nil || events == nil {
		return 0
	}
	count := 0
	for _, raw := range events {
		groups, ok := raw.([]any)
		if !ok {
			continue
		}
		for _, rawGroup := range groups {
			group, ok := rawGroup.(map[string]any)
			if !ok {
				continue
			}
			entries, ok := group["hooks"].([]any)
			if !ok {
				continue
			}
			for _, rawEntry := range entries {
				if !isCFOEntry(rawEntry) {
					count++
				}
			}
		}
	}
	return count
}

// isCFOEntry reports whether one hook entry is one this package wrote.
func isCFOEntry(raw any) bool {
	entry, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	command, ok := entry["command"].(string)
	return ok && strings.HasPrefix(command, rootPrefix)
}
