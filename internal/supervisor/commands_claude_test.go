package supervisor

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// claudePlugin is one plugin a fixture installs: its id, name@marketplace,
// the scope it is installed for, the folder a non-user install is for, and
// its files by their path below the plugin's folder.
type claudePlugin struct {
	id, scope, projectPath string
	files                  map[string]string
}

// installClaudePlugins installs plugins under pluginsRoot as Claude Code
// records them in installed_plugins.json, each in its own cache folder.
func installClaudePlugins(t *testing.T, pluginsRoot string, plugins ...claudePlugin) {
	t.Helper()
	type install struct {
		Scope       string `json:"scope"`
		InstallPath string `json:"installPath"`
		Version     string `json:"version"`
		ProjectPath string `json:"projectPath,omitempty"`
	}
	installed := map[string][]install{}
	for _, plugin := range plugins {
		name, marketplace, _ := strings.Cut(plugin.id, "@")
		path := filepath.Join(pluginsRoot, "cache", marketplace, name, "1.0.0")
		for file, body := range plugin.files {
			writeCommandFixture(t, filepath.Join(path, filepath.FromSlash(file)), body)
		}
		installed[plugin.id] = append(installed[plugin.id], install{Scope: plugin.scope, InstallPath: path, Version: "1.0.0", ProjectPath: plugin.projectPath})
	}
	data, err := json.Marshal(map[string]any{"version": 2, "plugins": installed})
	if err != nil {
		t.Fatal(err)
	}
	writeCommandFixture(t, filepath.Join(pluginsRoot, "installed_plugins.json"), string(data))
}

// enabledPlugins is a Claude Code settings file that turns plugins on or off.
func enabledPlugins(t *testing.T, enabled map[string]bool) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"enabledPlugins": enabled})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

const skillBody = "---\ndescription: A made-up skill\n---\nBody"

// Claude Code offers each enabled plugin's commands and skills behind the
// plugin's name and a colon: its commands folder unless its manifest names
// commands of its own, its skills folder and the skill folders its manifest
// adds, the plugin itself when it is one skill, and the name it is installed
// under when it has no manifest. A disabled plugin and a skill only Claude may
// invoke are left out.
func TestClaudeCodeOffersItsEnabledPluginsCommandsAndSkills(t *testing.T) {
	// Arrange
	board, profile, _ := commandBoard(t, "claude")
	claudeRoot := filepath.Join(profile, ".claude")
	installClaudePlugins(t, filepath.Join(claudeRoot, "plugins"),
		claudePlugin{id: "kit-one@sample-market", scope: "user", files: map[string]string{
			".claude-plugin/plugin.json":  `{"name":"kit-one"}`,
			"commands/tidy.md":            "---\ndescription: Tidy up\n---\nx",
			"commands/group/nested.md":    "x",
			"skills/find-notes/SKILL.md":  skillBody,
			"skills/only-claude/SKILL.md": "---\ndescription: Not for people\nuser-invocable: false\n---\nx",
		}},
		claudePlugin{id: "kit-two@sample-market", scope: "user", files: map[string]string{
			".claude-plugin/plugin.json": `{"name":"kit-two-ns","skills":["./extra"],"commands":{"about":{"content":"Explain.","description":"About kit two"},"status":{"source":"./cmds/status.md"}}}`,
			"cmds/status.md":             "---\ndescription: Show status\n---\nx",
			"commands/replaced.md":       "x",
			"extra/more/SKILL.md":        skillBody,
			"skills/plain/SKILL.md":      skillBody,
		}},
		claudePlugin{id: "kit-bare@sample-market", scope: "user", files: map[string]string{
			"commands/bare-cmd.md":        "x",
			"skills/bare-skill/SKILL.md":  skillBody,
			"not-a-skill/notes/README.md": "x",
		}},
		claudePlugin{id: "kit-root@sample-market", scope: "user", files: map[string]string{
			".claude-plugin/plugin.json": `{"name":"kit-root"}`,
			"SKILL.md":                   skillBody,
			"docs/inner/SKILL.md":        skillBody,
		}},
		claudePlugin{id: "kit-off@sample-market", scope: "user", files: map[string]string{
			"skills/off-skill/SKILL.md": skillBody,
		}},
	)
	writeCommandFixture(t, filepath.Join(claudeRoot, "settings.json"), enabledPlugins(t, map[string]bool{"kit-one@sample-market": true, "kit-two@sample-market": true, "kit-bare@sample-market": true, "kit-root@sample-market": true, "kit-off@sample-market": false}))

	// Act
	entries := entriesIn(t, getCommands(board, "task=task-1&token=test-instance"))

	// Assert
	want := []string{"kit-bare:bare-cmd", "kit-bare:bare-skill", "kit-one:find-notes", "kit-one:group:nested", "kit-one:tidy", "kit-root:kit-root", "kit-two-ns:about", "kit-two-ns:more", "kit-two-ns:plain", "kit-two-ns:status"}
	if names := ownNames(entries); !reflect.DeepEqual(names, want) {
		t.Fatalf("commands = %v, want %v", names, want)
	}
	for _, entry := range entries {
		described := map[string]string{"kit-one:tidy": "Tidy up", "kit-two-ns:about": "About kit two", "kit-two-ns:status": "Show status", "kit-one:find-notes": "A made-up skill"}
		if description, ok := described[entry.Name]; ok && entry.Description != description {
			t.Fatalf("%s describes itself as %q, want %q", entry.Name, entry.Description, description)
		}
	}
}

// A plugin is enabled by the user's settings, then the session folder's
// shared settings, then its local settings, the last to name it winning, and
// by its manifest's default when none names it.
func TestClaudePluginsFollowTheSettingsThatNameThemLast(t *testing.T) {
	const id = "kit-one@sample-market"
	for _, c := range []struct {
		what                 string
		user, project, local map[string]bool
		manifest             string
		isOffered            bool
		hasUserByteOrderMark bool
	}{
		{"no settings name it", nil, nil, nil, `{"name":"kit-one"}`, true, false},
		{"no settings name it and it starts off", nil, nil, nil, `{"name":"kit-one","defaultEnabled":false}`, false, false},
		{"the user turns it off", map[string]bool{id: false}, nil, nil, `{"name":"kit-one"}`, false, false},
		{"the project turns it back on", map[string]bool{id: false}, map[string]bool{id: true}, nil, `{"name":"kit-one"}`, true, false},
		{"the project turns it off", map[string]bool{id: true}, map[string]bool{id: false}, nil, `{"name":"kit-one"}`, false, false},
		{"the local settings turn it back on", map[string]bool{id: true}, map[string]bool{id: false}, map[string]bool{id: true}, `{"name":"kit-one"}`, true, false},
		{"the local settings turn it off", nil, map[string]bool{id: true}, map[string]bool{id: false}, `{"name":"kit-one","defaultEnabled":true}`, false, false},
		{"the user's settings start with a byte order mark", map[string]bool{id: true}, nil, nil, `{"name":"kit-one","defaultEnabled":false}`, true, true},
	} {
		t.Run(c.what, func(t *testing.T) {
			// Arrange
			board, profile, worktree := commandBoard(t, "claude")
			claudeRoot := filepath.Join(profile, ".claude")
			installClaudePlugins(t, filepath.Join(claudeRoot, "plugins"), claudePlugin{id: id, scope: "user", files: map[string]string{
				".claude-plugin/plugin.json": c.manifest,
				"commands/tidy.md":           "x",
			}})
			userSettings := filepath.Join(claudeRoot, "settings.json")
			for path, enabled := range map[string]map[string]bool{
				userSettings: c.user,
				filepath.Join(worktree, ".claude", "settings.json"):       c.project,
				filepath.Join(worktree, ".claude", "settings.local.json"): c.local,
			} {
				if enabled == nil {
					continue
				}
				settings := enabledPlugins(t, enabled)
				if path == userSettings && c.hasUserByteOrderMark {
					settings = "\ufeff" + settings
				}
				writeCommandFixture(t, path, settings)
			}

			// Act
			names := commandsIn(t, getCommands(board, "task=task-1&token=test-instance"))

			// Assert
			want := []string{}
			if c.isOffered {
				want = []string{"kit-one:tidy"}
			}
			if !reflect.DeepEqual(names, want) {
				t.Fatalf("commands = %v, want %v", names, want)
			}
		})
	}
}

// A plugin installed for one folder alone is offered to a session in that
// folder and to no other, and the plugins folder the environment names is
// read in place of the default.
func TestClaudePluginsAreReadWhereTheyAreInstalledFor(t *testing.T) {
	for _, c := range []struct {
		what            string
		isOwnFolder     bool
		isCacheVariable bool
		want            []string
	}{
		{"an install for another folder", false, false, []string{}},
		{"an install for the session's folder", true, false, []string{"kit-local:tidy"}},
		{"an install where the environment keeps plugins", true, true, []string{"kit-local:tidy"}},
	} {
		t.Run(c.what, func(t *testing.T) {
			// Arrange
			board, profile, worktree := commandBoard(t, "claude")
			pluginsRoot := filepath.Join(profile, ".claude", "plugins")
			if c.isCacheVariable {
				pluginsRoot = filepath.Join(profile, "elsewhere-plugins")
				t.Setenv("CLAUDE_CODE_PLUGIN_CACHE_DIR", pluginsRoot)
			}
			projectPath := filepath.Join(profile, "another-project")
			if c.isOwnFolder {
				projectPath = worktree
			}
			installClaudePlugins(t, pluginsRoot, claudePlugin{id: "kit-local@sample-market", scope: "local", projectPath: projectPath, files: map[string]string{"commands/tidy.md": "x"}})

			// Act
			names := commandsIn(t, getCommands(board, "task=task-1&token=test-instance"))

			// Assert
			if !reflect.DeepEqual(names, c.want) {
				t.Fatalf("commands = %v, want %v", names, c.want)
			}
		})
	}
}
