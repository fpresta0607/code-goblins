package uninstall

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harnessmap"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
)

// harnessNames are the harnesses by the names a person knows them by.
var harnessNames = map[string]string{"claude": "Claude Code", "codex": "Codex", "pi": "pi"}

// harnesses are what Code Goblins added to Claude Code, Codex and pi: the
// allow rules and hooks in Claude Code's settings, the board's own hooks in
// each, the skills it ships, the backups it kept of their files, its trust
// of each goblin's folder, and the agents' records of the goblins' and the
// CFO's conversations, which name folders that go with the home.
func (m Machine) harnesses(ctx context.Context) ([]Item, error) {
	var items []Item
	if wired, err := m.Install.UserSettingsWired(); err != nil {
		return nil, err
	} else if wired {
		items = append(items, Item{Action: Remove, Group: GroupHarnesses, What: "the allow rules and hooks it added to Claude Code's settings", Where: m.Install.UserSettings, do: func(context.Context) error {
			if err := m.Install.RemoveUserSettings(); err != nil {
				return err
			}
			if install.EmptyJSON(m.Install.UserSettings) {
				return os.Remove(m.Install.UserSettings)
			}
			return nil
		}})
	}
	for _, harness := range []string{"claude", "codex", "pi"} {
		dir, ok := m.Install.HarnessDirs[harness]
		if !ok {
			continue
		}
		if there, err := nativehook.Installed(harness, dir); err != nil {
			return nil, err
		} else if there {
			items = append(items, Item{Action: Remove, Group: GroupHarnesses, What: "the board's hooks in " + harnessNames[harness], Where: dir, do: func(context.Context) error {
				_, err := nativehook.Uninstall(harness, dir)
				return err
			}})
		}
	}
	if skills, err := m.skills(); err != nil {
		return nil, err
	} else {
		items = append(items, skills...)
	}
	records, err := m.conversations()
	if err != nil {
		return nil, err
	}
	items = append(items, records...)
	trust, err := m.trust()
	if err != nil {
		return nil, err
	}
	return append(items, trust...), nil
}

// skills are the skills Code Goblins ships, in the shared skills folder,
// with Claude Code's junctions to them.
func (m Machine) skills() ([]Item, error) {
	recorded, err := harnessmap.Read(m.Home.State)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var items []Item
	for _, name := range recorded.Installed {
		shared := filepath.Join(recorded.SharedSkills, name)
		if isThere, _ := there(filepath.Join(shared, "SKILL.md")); !isThere {
			continue
		}
		items = append(items, Item{Action: Remove, Group: GroupHarnesses, What: "the skill " + name, Where: shared, do: func(context.Context) error {
			_, err := harnessmap.RemoveSkills(recorded, []string{name})
			return err
		}})
	}
	return items, nil
}

// settingsFiles are the harnesses' settings and hooks files an install
// writes into.
func (m Machine) settingsFiles() []string {
	files := []string{m.Install.UserSettings}
	for _, harness := range []string{"claude", "codex"} {
		if dir, ok := m.Install.HarnessDirs[harness]; ok {
			files = append(files, filepath.Join(dir, "settings.json"), filepath.Join(dir, "hooks.json"))
		}
	}
	return unique(files)
}

// tidied are what the install's own edits leave in the harnesses' folders
// once its entries are out: the backups it kept of their files, a settings
// or hooks file left holding nothing, and a folder it made that holds
// nothing, or nothing but other such folders, now.
func (m Machine) tidied() []Item {
	var items []Item
	files := m.settingsFiles()
	for _, backup := range install.SettingsBackups(files...) {
		items = append(items, removal(GroupFolders, "a backup it kept of a settings file", backup))
	}
	for _, file := range files {
		if install.EmptyJSON(file) {
			items = append(items, removal(GroupFolders, "an empty settings file it left", file))
		}
	}
	made := m.madeFolders()
	for _, folder := range made {
		if emptyBut(folder, made) {
			items = append(items, Item{Action: Remove, Group: GroupFolders, What: "an empty folder it made", Where: folder, do: func(context.Context) error {
				for _, inner := range made {
					if inner != folder && within(inner, folder) {
						removeIfEmpty(inner)
					}
				}
				if !removeIfEmpty(folder) {
					return fmt.Errorf("%s is not empty", folder)
				}
				return nil
			}})
		}
	}
	return items
}

// emptyBut reports whether folder holds nothing but folders of made that are
// empty in the same way.
func emptyBut(folder string, made []string) bool {
	entries, err := os.ReadDir(folder)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		inner := filepath.Join(folder, entry.Name())
		if !entry.IsDir() || !slices.ContainsFunc(made, func(path string) bool { return strings.EqualFold(path, inner) }) || !emptyBut(inner, made) {
			return false
		}
	}
	return true
}

// madeFolders are the folders an install makes in the harnesses' own
// folders when they are missing, which go once nothing is left in them,
// the inner ones first.
func (m Machine) madeFolders() []string {
	folders := []string{filepath.Join(m.Profile, ".agents", "skills"), filepath.Join(m.Profile, ".agents")}
	if dir, ok := m.Install.HarnessDirs["claude"]; ok {
		folders = append(folders, filepath.Join(dir, "skills"))
	}
	if dir, ok := m.Install.HarnessDirs["pi"]; ok {
		folders = append(folders, filepath.Join(dir, "extensions"))
	}
	return folders
}

// homeFolders are the folders goblins and the CFO work in: the home itself,
// where the CFO runs, and each folder goblin worktrees are in.
func (m Machine) homeFolders() []string {
	return append([]string{m.Home.Root}, m.Home.WorktreeRoots()...)
}

// slug is a folder as Claude Code names the folder of its records of the
// conversations held in it: every character but a letter or a digit is a
// dash.
func slug(folder string) string {
	return strings.Map(func(r rune) rune {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return r
		}
		return '-'
	}, filepath.Clean(folder))
}

// piSlug is a folder as pi names its sessions' folder: the path with its
// separators and colon as dashes, between two dashes on each side.
func piSlug(folder string) string {
	return "--" + strings.NewReplacer(`\`, "-", "/", "-", ":", "-").Replace(filepath.Clean(folder)) + "--"
}

// conversations are the agents' records of the conversations held in the
// home's folders: Claude Code's and pi's, a folder of records for each
// folder a conversation was held in, and Codex's, a file for each session
// whose first record names the folder.
func (m Machine) conversations() ([]Item, error) {
	var items []Item
	folders := m.homeFolders()
	if dir, ok := m.Install.HarnessDirs["claude"]; ok {
		projects := filepath.Join(dir, "projects")
		entries, _ := os.ReadDir(projects)
		for _, entry := range entries {
			for _, folder := range folders {
				name, root := strings.ToLower(entry.Name()), strings.ToLower(slug(folder))
				if name == root && folder == m.Home.Root || folder != m.Home.Root && strings.HasPrefix(name, root+"-") {
					items = append(items, removal(GroupHarnesses, "Claude Code's record of "+whose(entry.Name()[len(root):]), filepath.Join(projects, entry.Name())))
					break
				}
			}
		}
	}
	if dir, ok := m.Install.HarnessDirs["pi"]; ok {
		sessions := filepath.Join(dir, "sessions")
		entries, _ := os.ReadDir(sessions)
		for _, entry := range entries {
			for _, folder := range folders {
				name, root := strings.ToLower(entry.Name()), strings.ToLower(piSlug(folder))
				if name == root || folder != m.Home.Root && strings.HasPrefix(name, strings.TrimSuffix(root, "--")+"-") {
					rest := strings.TrimSuffix(entry.Name()[min(len(entry.Name()), len(strings.TrimSuffix(root, "--"))):], "--")
					items = append(items, removal(GroupHarnesses, "pi's record of "+whose(rest), filepath.Join(sessions, entry.Name())))
					break
				}
			}
		}
	}
	if dir, ok := m.Install.HarnessDirs["codex"]; ok {
		_ = filepath.WalkDir(filepath.Join(dir, "sessions"), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				return nil
			}
			if cwd := codexFolder(path); cwd != "" && slices.ContainsFunc(folders, func(folder string) bool { return within(cwd, folder) }) {
				name := "-" + filepath.Base(cwd)
				if within(m.Home.Root, cwd) {
					name = ""
				}
				items = append(items, removal(GroupHarnesses, "Codex's record of "+whose(name), path))
			}
			return nil
		})
	}
	return items, nil
}

// whose names whose conversations a record holds from what is left of its
// folder's name past the home's own: nothing for the CFO's, held in the home,
// and the goblin's folder for a goblin's.
func whose(rest string) string {
	rest = strings.TrimPrefix(rest, "-")
	if rest == "" {
		return "the CFO's conversations"
	}
	return rest + "'s conversations"
}

// codexFolder is the folder a Codex session was held in, which its first
// record names.
func codexFolder(path string) string {
	file, err := fsx.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	lines := bufio.NewReader(file)
	first, err := lines.ReadBytes('\n')
	if err != nil && len(first) == 0 {
		return ""
	}
	var record struct {
		Payload struct {
			Cwd string `json:"cwd"`
		} `json:"payload"`
	}
	if json.Unmarshal(first, &record) != nil {
		return ""
	}
	return record.Payload.Cwd
}

// unique is paths without the ones named twice, compared as Windows compares
// paths.
func unique(paths []string) []string {
	var kept []string
	for _, path := range paths {
		if !slices.ContainsFunc(kept, func(known string) bool { return strings.EqualFold(filepath.Clean(known), filepath.Clean(path)) }) {
			kept = append(kept, path)
		}
	}
	return kept
}
