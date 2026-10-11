package uninstall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// trust are the entries Claude Code and Codex keep for each folder a goblin
// or the CFO worked in, such as whether its trust dialog was accepted, which
// name folders that go with the home.
func (m Machine) trust() ([]Item, error) {
	var items []Item
	folders := m.homeFolders()
	inHome := func(path string) bool {
		return slices.ContainsFunc(folders, func(folder string) bool { return within(filepath.FromSlash(path), folder) })
	}
	if dir, ok := m.Install.HarnessDirs["claude"]; ok {
		file := filepath.Join(dir, ".claude.json")
		if strings.EqualFold(filepath.Clean(dir), filepath.Join(m.Profile, ".claude")) {
			file = filepath.Join(m.Profile, ".claude.json")
		}
		if n, err := claudeProjects(file, inHome, false); err != nil {
			return nil, err
		} else if n > 0 {
			items = append(items, Item{Action: Remove, Group: GroupHarnesses, What: "Claude Code's entries for " + plural(n, "folder") + " of the home", Where: file, do: func(context.Context) error {
				_, err := claudeProjects(file, inHome, true)
				return err
			}})
		}
	}
	if dir, ok := m.Install.HarnessDirs["codex"]; ok {
		file := filepath.Join(dir, "config.toml")
		if n, err := codexProjects(file, inHome, false); err != nil {
			return nil, err
		} else if n > 0 {
			items = append(items, Item{Action: Remove, Group: GroupHarnesses, What: "Codex's entries for " + plural(n, "folder") + " of the home", Where: file, do: func(context.Context) error {
				_, err := codexProjects(file, inHome, true)
				return err
			}})
		}
	}
	return items, nil
}

// claudeProjects counts the entries of Claude Code's configuration file
// whose folder inHome takes, and with remove takes them out, leaving every
// other key as it is.
func claudeProjects(file string, inHome func(string) bool, remove bool) (int, error) {
	data, err := fsx.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}), &top); err != nil {
		return 0, errors.New("uninstall: " + file + " is not a JSON object, so its entries for the home's folders were left")
	}
	var projects map[string]json.RawMessage
	if raw, ok := top["projects"]; ok {
		if err := json.Unmarshal(raw, &projects); err != nil {
			return 0, errors.New("uninstall: the projects in " + file + " are not a JSON object, so its entries for the home's folders were left")
		}
	}
	n := 0
	for folder := range projects {
		if inHome(folder) {
			n++
			delete(projects, folder)
		}
	}
	if !remove || n == 0 {
		return n, nil
	}
	if top["projects"], err = json.Marshal(projects); err != nil {
		return 0, err
	}
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return 0, err
	}
	return n, fsx.AtomicWriteFile(file, append(out, '\n'))
}

// codexProjects counts the [projects.'<folder>'] tables of Codex's
// configuration whose folder inHome takes, and with remove takes each out,
// down to the next table, leaving every other line as it is.
func codexProjects(file string, inHome func(string) bool, remove bool) (int, error) {
	data, err := fsx.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	newline := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		newline = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	var kept []string
	n, skipping := 0, false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			skipping = false
			if folder, ok := codexProjectHeader(trimmed); ok && inHome(folder) {
				n++
				skipping = true
			}
		}
		if !skipping {
			kept = append(kept, line)
		}
	}
	if !remove || n == 0 {
		return n, nil
	}
	return n, fsx.AtomicWriteFile(file, []byte(strings.Join(kept, newline)))
}

// codexProjectHeader reads the folder of a [projects.'<folder>'] or
// [projects."<folder>"] table header.
func codexProjectHeader(header string) (string, bool) {
	inner, ok := strings.CutPrefix(header, "[projects.")
	if !ok {
		return "", false
	}
	inner, ok = strings.CutSuffix(inner, "]")
	if !ok || len(inner) < 2 {
		return "", false
	}
	quote := inner[0]
	if (quote != '\'' && quote != '"') || inner[len(inner)-1] != quote {
		return "", false
	}
	folder := inner[1 : len(inner)-1]
	if quote == '"' {
		folder = strings.ReplaceAll(folder, `\\`, `\`)
	}
	return folder, true
}
