package janitor

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// knownName is the projects root's entries the janitor has seen, kept in the
// home's state folder.
const knownName = "janitor-projects-root.json"

// reportProjectsRoot reports each folder or file in the projects root that is
// no git checkout: the folders and files goblins made beside the Overlord's
// checkouts. What is there at the first sweep is reported by that sweep alone,
// since much of it is the Overlord's own; what appears after it is reported by
// every sweep until it is gone. They are reported, never removed, because the
// projects root is the Overlord's.
func (cfg Config) reportProjectsRoot(record *Record) {
	if cfg.ProjectsRoot == "" {
		return
	}
	entries, err := os.ReadDir(cfg.ProjectsRoot)
	if err != nil {
		record.Notes = append(record.Notes, "the projects root could not be read: "+err.Error())
		return
	}
	path := filepath.Join(cfg.Home.State, knownName)
	known, err := readKnown(path)
	if err != nil {
		record.Notes = append(record.Notes, "the projects root's known entries could not be read: "+err.Error())
		return
	}
	first := known == nil
	if first {
		known = map[string]bool{}
	}
	for _, entry := range entries {
		name := strings.ToLower(entry.Name())
		if known[name] {
			continue
		}
		full := filepath.Join(cfg.ProjectsRoot, entry.Name())
		if first {
			known[name] = true
		}
		if _, err := os.Stat(filepath.Join(full, ".git")); err == nil {
			continue
		}
		detail := "a folder in the projects root that is no git checkout"
		if !entry.IsDir() {
			detail = "a file in the projects root"
		}
		if first {
			detail += "; there at the first sweep, so reported this once"
		}
		record.Strays = append(record.Strays, Item{Kind: "projects root", Path: full, Bytes: Size(full), Detail: detail})
	}
	if first {
		if err := writeKnown(path, known); err != nil {
			record.Notes = append(record.Notes, "the projects root's entries could not be recorded: "+err.Error())
		}
	}
}

func readKnown(path string) (map[string]bool, error) {
	data, err := fsx.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(names))
	for _, name := range names {
		known[strings.ToLower(name)] = true
	}
	return known, nil
}

func writeKnown(path string, known map[string]bool) error {
	names := make([]string, 0, len(known))
	for name := range known {
		names = append(names, name)
	}
	sort.Strings(names)
	data, err := json.MarshalIndent(names, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, append(data, '\n'))
}
