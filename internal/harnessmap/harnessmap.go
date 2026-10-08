// Package harnessmap finds where each agent harness keeps its configuration
// and skills on this machine, the same way on every machine, and records it in
// one file in the CFO home that the CFO and goblins read. It also installs the
// skills Code Goblins ships the way the Overlord keeps his own: one real copy
// in the shared skills folder, which Codex and Pi read themselves, and a
// junction to it in Claude Code's skills folder, which does not. Nothing in a
// harness folder that Code Goblins did not put there is changed.
package harnessmap

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Root is one harness's configuration folder.
type Root struct {
	// Harness is claude, codex or pi.
	Harness string `json:"harness"`
	// Path is the folder the harness reads its configuration from.
	Path string `json:"path"`
	// Source is the variable that named it, or "default" for the harness's
	// own default under the user's profile.
	Source string `json:"source"`
	// Holds names what the harness keeps there.
	Holds []string `json:"holds"`
	// Skills is the harness's own skills folder.
	Skills string `json:"skills"`
	// Present is whether the folder exists.
	Present bool `json:"present"`
}

// Map is where every harness keeps its configuration on this machine.
type Map struct {
	Roots []Root `json:"roots"`
	// SharedSkills is the skills folder Codex and Pi read themselves, where a
	// skill is kept once for every harness.
	SharedSkills string `json:"shared_skills"`
	// Installed are the skills Code Goblins installed, by name.
	Installed []string `json:"installed,omitempty"`
}

// Find reads the harness folders the environment and the user's profile name:
// Claude Code's CLAUDE_CONFIG_DIR or ~/.claude, Codex's CODEX_HOME or
// ~/.codex, Pi's PI_CODING_AGENT_DIR or ~/.pi/agent, and the shared
// ~/.agents/skills.
func Find(getenv func(string) string, userHome string) Map {
	root := func(harness, variable, fallback string, holds []string) Root {
		r := Root{Harness: harness, Path: fallback, Source: "default", Holds: holds}
		if value := strings.TrimSpace(getenv(variable)); value != "" {
			r.Path, r.Source = filepath.Clean(value), variable
		}
		r.Skills = filepath.Join(r.Path, "skills")
		if info, err := os.Stat(r.Path); err == nil && info.IsDir() {
			r.Present = true
		}
		return r
	}
	return Map{
		Roots: []Root{
			root("claude", "CLAUDE_CONFIG_DIR", filepath.Join(userHome, ".claude"), []string{"settings.json", "CLAUDE.md", "skills", "commands", "agents", "plugins"}),
			root("codex", "CODEX_HOME", filepath.Join(userHome, ".codex"), []string{"config.toml", "AGENTS.md", "skills"}),
			root("pi", "PI_CODING_AGENT_DIR", filepath.Join(userHome, ".pi", "agent"), []string{"settings.json", "skills"}),
		},
		SharedSkills: filepath.Join(userHome, ".agents", "skills"),
	}
}

// Harness returns the root of the named harness.
func (m Map) Harness(name string) (Root, bool) {
	for _, root := range m.Roots {
		if root.Harness == name {
			return root, true
		}
	}
	return Root{}, false
}

// FileName is the map's file in the home's state folder.
const FileName = "harnesses.json"

// Write records the map in stateDir.
func Write(stateDir string, m Map) error {
	data, err := json.MarshalIndent(struct {
		Map
		Written time.Time `json:"written"`
	}{m, time.Now().UTC().Truncate(time.Second)}, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(stateDir, FileName), append(data, '\n'))
}

// Read reads the map Write recorded in stateDir.
func Read(stateDir string) (Map, error) {
	data, err := fsx.ReadFile(filepath.Join(stateDir, FileName))
	if err != nil {
		return Map{}, err
	}
	var m Map
	if err := json.Unmarshal(data, &m); err != nil {
		return Map{}, fmt.Errorf("harnessmap: read %s: %w", FileName, err)
	}
	return m, nil
}

// ownerFile marks a skill folder Code Goblins wrote, and lists the files it
// wrote there, so a reinstall updates only its own folders and removes only
// the files it no longer ships.
const ownerFile = ".code-goblins"

const ownerText = "Code Goblins installed this skill and keeps it up to date; a folder without this file is never touched.\r\nThe files it wrote:\r\n"

// Linker makes a directory junction at link pointing at target.
type Linker func(link, target string) error

// InstallSkills installs each skill folder in skills, a tree whose top-level
// folders are skills, as one real copy under m.SharedSkills and a junction to
// it in Claude Code's skills folder. A skill folder there that Code Goblins
// did not write is left as it is and named in the result, as is an entry in
// Claude Code's folder that is not a junction to the shared copy. It returns
// the skills it installed and what it left alone and why.
func InstallSkills(skills fs.FS, m Map, link Linker) (SkillInstall, error) {
	var result SkillInstall
	names, err := fs.ReadDir(skills, ".")
	if err != nil {
		return result, err
	}
	claude, _ := m.Harness("claude")
	for _, entry := range names {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		shared := filepath.Join(m.SharedSkills, name)
		ours, err := isOurs(shared)
		if err != nil {
			return result, err
		}
		if !ours {
			result.Kept = append(result.Kept, shared+": a skill of that name is already there and Code Goblins did not put it there")
			continue
		}
		changed, err := writeSkill(skills, name, shared)
		if err != nil {
			return result, err
		}
		result.Names = append(result.Names, name)
		if claude.Skills != "" {
			junction := filepath.Join(claude.Skills, name)
			switch target, err := os.Readlink(junction); {
			case err == nil && sameDir(target, shared):
			case errors.Is(err, fs.ErrNotExist):
				if err := os.MkdirAll(claude.Skills, 0o755); err != nil {
					return result, err
				}
				if err := link(junction, shared); err != nil {
					return result, fmt.Errorf("harnessmap: link %s to %s: %w", junction, shared, err)
				}
				changed = true
			default:
				result.Kept = append(result.Kept, junction+": something other than a junction to "+shared+" is there, so Claude Code reads that instead")
			}
		}
		if changed {
			result.Changed = append(result.Changed, name)
		}
	}
	return result, nil
}

// SkillInstall is what InstallSkills did: the skills it installed, those of
// them whose files or junction it wrote, and what it found and kept as it was.
type SkillInstall struct {
	Names   []string
	Changed []string
	Kept    []string
}

// RemoveSkills removes each named skill Code Goblins installed: Claude Code's
// junction to it, when it is one, and the shared copy, when it holds the owner
// file. A folder that is not Code Goblins' is left as it is.
func RemoveSkills(m Map, names []string) ([]string, error) {
	claude, _ := m.Harness("claude")
	var removed []string
	for _, name := range names {
		shared := filepath.Join(m.SharedSkills, name)
		if _, err := os.Stat(filepath.Join(shared, ownerFile)); err != nil {
			continue
		}
		if claude.Skills != "" {
			junction := filepath.Join(claude.Skills, name)
			if target, err := os.Readlink(junction); err == nil && sameDir(target, shared) {
				if err := os.Remove(junction); err != nil {
					return removed, err
				}
			}
		}
		if err := os.RemoveAll(shared); err != nil {
			return removed, err
		}
		removed = append(removed, name)
	}
	return removed, nil
}

// isOurs reports whether the skill folder at path is Code Goblins' to write:
// it is missing, or it holds the owner file.
func isOurs(path string) (bool, error) {
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return true, nil
	} else if err != nil {
		return false, err
	}
	_, err := os.Stat(filepath.Join(path, ownerFile))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// writeSkill writes the skill folder name of skills to dest, removes the files
// an earlier install wrote there that this one does not, and writes the owner
// file last, and reports whether it wrote or removed anything.
func writeSkill(skills fs.FS, name, dest string) (bool, error) {
	changed := false
	previous := map[string]bool{}
	owner, ownerErr := fsx.ReadFile(filepath.Join(dest, ownerFile))
	if ownerErr == nil {
		_, list, _ := strings.Cut(strings.ReplaceAll(string(owner), "\r\n", "\n"), "wrote:\n")
		for _, line := range strings.Split(list, "\n") {
			if line != "" {
				previous[line] = true
			}
		}
	}
	var written []string
	err := fs.WalkDir(skills, name, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(path, name+"/")
		data, err := fs.ReadFile(skills, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, filepath.FromSlash(rel))
		if current, err := fsx.ReadFile(target); err != nil || string(current) != string(data) {
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := fsx.AtomicWriteFile(target, data); err != nil {
				return err
			}
			changed = true
		}
		written = append(written, rel)
		delete(previous, rel)
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("harnessmap: install the skill %s: %w", name, err)
	}
	for rel := range previous {
		local := filepath.FromSlash(rel)
		if filepath.IsLocal(local) {
			if err := os.Remove(filepath.Join(dest, local)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return false, err
			}
			changed = true
		}
	}
	sort.Strings(written)
	text := ownerText + strings.Join(written, "\r\n") + "\r\n"
	if ownerErr == nil && string(owner) == text {
		return changed, nil
	}
	return true, fsx.AtomicWriteFile(filepath.Join(dest, ownerFile), []byte(text))
}

func sameDir(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// Problem is one thing wrong with the harness folders.
type Problem struct {
	Kind   string
	Detail string
}

// Check reports what is wrong with the harness folders: a harness root that
// is missing, a junction in a skills folder whose target is gone, and a skill
// name kept as a real folder in more than one skills folder, which leaves a
// harness reading a copy that drifts from the other.
func Check(m Map) []Problem {
	var problems []Problem
	folders := []string{m.SharedSkills}
	for _, root := range m.Roots {
		if !root.Present {
			problems = append(problems, Problem{Kind: "missing", Detail: fmt.Sprintf("%s's configuration folder %s (%s) does not exist", root.Harness, root.Path, root.Source)})
		}
		folders = append(folders, root.Skills)
	}
	copies := map[string][]string{}
	for _, folder := range folders {
		entries, err := os.ReadDir(folder)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			path := filepath.Join(folder, entry.Name())
			if target, err := os.Readlink(path); err == nil {
				if !filepath.IsAbs(target) {
					target = filepath.Join(folder, target)
				}
				if _, err := os.Stat(target); err != nil {
					problems = append(problems, Problem{Kind: "broken", Detail: fmt.Sprintf("%s links to %s, which is gone", path, target)})
				}
				continue
			}
			if entry.IsDir() {
				key := strings.ToLower(entry.Name())
				copies[key] = append(copies[key], path)
			}
		}
	}
	keys := make([]string, 0, len(copies))
	for key := range copies {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if paths := copies[key]; len(paths) > 1 {
			problems = append(problems, Problem{Kind: "duplicate", Detail: fmt.Sprintf("the skill %s is kept %d times: %s", key, len(paths), strings.Join(paths, ", "))})
		}
	}
	return problems
}
