package supervisor

import (
	"cmp"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// claudePluginCommands reads the commands and skills of the plugins Claude
// Code has enabled for a session running in folder, each behind its plugin's
// name and a colon, as Claude Code offers them: the plugins installed for the
// user, or for that folder alone, that the user's settings, then the folder's
// shared and then its local settings leave enabled, from where each was
// installed under pluginsRoot.
func claudePluginCommands(folder, claudeRoot, pluginsRoot string) []command {
	data, err := fsx.ReadFile(filepath.Join(pluginsRoot, "installed_plugins.json"))
	if err != nil {
		return nil
	}
	var installed struct {
		Plugins map[string][]struct {
			Scope       string `json:"scope"`
			InstallPath string `json:"installPath"`
			ProjectPath string `json:"projectPath"`
		} `json:"plugins"`
	}
	if json.Unmarshal(data, &installed) != nil {
		return nil
	}
	enabled := map[string]bool{}
	for _, settings := range []string{filepath.Join(claudeRoot, "settings.json"), filepath.Join(folder, ".claude", "settings.json"), filepath.Join(folder, ".claude", "settings.local.json")} {
		var read struct {
			EnabledPlugins map[string]bool `json:"enabledPlugins"`
		}
		data, err := fsx.ReadFile(settings)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || json.Unmarshal(data, &read) != nil {
			return nil
		}
		for id, isEnabled := range read.EnabledPlugins {
			enabled[id] = isEnabled
		}
	}
	var commands []command
	for _, id := range slices.Sorted(maps.Keys(installed.Plugins)) {
		installs := installed.Plugins[id]
		// An install for this folder alone is the one Claude Code loads here.
		path := ""
		for _, install := range installs {
			switch {
			case install.Scope == "user" && path == "":
				path = install.InstallPath
			case install.Scope != "user" && install.ProjectPath != "" && strings.EqualFold(filepath.Clean(install.ProjectPath), filepath.Clean(folder)):
				path = install.InstallPath
			}
		}
		if path == "" {
			continue
		}
		manifest, err := readClaudePluginManifest(path)
		if err != nil {
			continue
		}
		isEnabled, isSet := enabled[id]
		if !isSet {
			isEnabled = manifest.DefaultEnabled == nil || *manifest.DefaultEnabled
		}
		if !isEnabled {
			continue
		}
		marketplaceName, _, _ := strings.Cut(id, "@")
		commands = append(commands, claudePluginEntries(path, cmp.Or(manifest.Name, marketplaceName), manifest)...)
	}
	return commands
}

// claudePluginManifest is what a plugin's .claude-plugin/plugin.json says
// about the commands and skills it offers.
type claudePluginManifest struct {
	Name           string          `json:"name"`
	DefaultEnabled *bool           `json:"defaultEnabled"`
	Commands       json.RawMessage `json:"commands"`
	Skills         json.RawMessage `json:"skills"`
}

// readClaudePluginManifest reads a plugin's manifest; a plugin without one
// offers the folders Claude Code reads by default.
func readClaudePluginManifest(root string) (claudePluginManifest, error) {
	var manifest claudePluginManifest
	data, err := fsx.ReadFile(filepath.Join(root, ".claude-plugin", "plugin.json"))
	if errors.Is(err, os.ErrNotExist) {
		return manifest, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &manifest)
	}
	return manifest, err
}

// claudePluginEntries reads the commands and skills one plugin offers, each
// behind namespace and a colon. The manifest's commands replace the
// plugin's commands folder: one path or a list of them, each a folder of
// command files or one file, or a map from each command's name to its file
// or its text. Its skills add to the plugin's skills folder: one path or a
// list of them, each a folder of skill folders or one skill's own folder. A
// plugin with neither a skills folder nor skills in its manifest, but a
// SKILL.md of its own, is that one skill.
func claudePluginEntries(root, namespace string, manifest claudePluginManifest) []command {
	prefix := namespace + ":"
	var entries []command
	var commandMap map[string]struct {
		Source      string `json:"source"`
		Content     string `json:"content"`
		Description string `json:"description"`
	}
	switch {
	case len(manifest.Commands) == 0:
		entries = append(entries, commandFiles(filepath.Join(root, "commands"), prefix)...)
	case json.Unmarshal(manifest.Commands, &commandMap) == nil && commandMap != nil:
		for name, entry := range commandMap {
			description := entry.Description
			if entry.Source != "" {
				fields, err := commandFrontmatter(filepath.Join(root, entry.Source))
				if err != nil || (fields.UserInvocable != nil && !*fields.UserInvocable) {
					continue
				}
				description = cmp.Or(description, fields.Description)
			} else if entry.Content == "" {
				continue
			}
			entries = append(entries, command{Name: prefix + name, Description: description, Source: "command"})
		}
	default:
		for _, path := range claudeManifestPaths(root, manifest.Commands) {
			if info, err := os.Stat(path); err == nil && info.IsDir() {
				entries = append(entries, commandFiles(path, prefix)...)
			} else if fields, err := commandFrontmatter(path); err == nil && strings.EqualFold(filepath.Ext(path), ".md") && (fields.UserInvocable == nil || *fields.UserInvocable) {
				entries = append(entries, command{Name: prefix + strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), Description: fields.Description, Source: "command"})
			}
		}
	}
	skillFolders := claudeManifestPaths(root, manifest.Skills)
	defaultSkills := filepath.Join(root, "skills")
	isOneSkill := false
	if info, err := os.Stat(defaultSkills); err == nil && info.IsDir() {
		skillFolders = append(skillFolders, defaultSkills)
	} else if len(skillFolders) == 0 {
		skillFolders, isOneSkill = []string{root}, true
	}
	for _, folder := range skillFolders {
		var skills []command
		skillFile := filepath.Join(folder, "SKILL.md")
		if _, err := os.Stat(skillFile); err == nil {
			if fields, err := commandFrontmatter(skillFile); err == nil && (fields.UserInvocable == nil || *fields.UserInvocable) {
				name := filepath.Base(folder)
				if folder == root {
					name = namespace
				}
				skills = []command{{Name: cmp.Or(fields.Name, name), Description: fields.Description, Source: "skill"}}
			}
		} else if !isOneSkill {
			skills = skillCommands(folder, false)
		}
		for _, skill := range skills {
			skill.Name = prefix + skill.Name
			entries = append(entries, skill)
		}
	}
	return entries
}

// claudeManifestPaths reads a manifest field that names one path or a list
// of them, each relative to the plugin's root.
func claudeManifestPaths(root string, field json.RawMessage) []string {
	var paths []string
	var one string
	if json.Unmarshal(field, &one) == nil {
		paths = []string{one}
	} else if json.Unmarshal(field, &paths) != nil {
		return nil
	}
	var resolved []string
	for _, path := range paths {
		if path != "" {
			resolved = append(resolved, filepath.Join(root, filepath.FromSlash(path)))
		}
	}
	return resolved
}
