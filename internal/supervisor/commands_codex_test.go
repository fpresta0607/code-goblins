package supervisor

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// codexSkillFixture writes one SKILL.md with a made-up description.
func codexSkillFixture(t *testing.T, path string) {
	t.Helper()
	writeCommandFixture(t, filepath.Join(path, "SKILL.md"), skillBody)
}

// Codex offers every skill it loads for the session's folder, as Codex
// 0.160.0 loads them: the user's own and shared skills, the project's
// .codex/skills and .agents/skills, its bundled skills and its enabled
// plugins' skills from their active version, each behind the plugin's name.
// It leaves out a hidden or too deep folder, a SKILL.md without frontmatter
// or a description, a skill its configuration turns off, a name two skills
// hold, which Codex takes as no skill, a plugin turned off or never turned
// on, and its prompts folder, which Codex 0.160.0 no longer reads.
func TestCodexOffersTheSkillsItLoadsForTheSession(t *testing.T) {
	// Arrange
	board, profile, worktree := commandBoard(t, "codex")
	home := filepath.Join(profile, ".codex")
	skills := filepath.Join(home, "skills")
	for _, path := range []string{
		filepath.Join(skills, "alpha-notes"),
		filepath.Join(skills, "nested", "deeper", "gamma-tool"),
		filepath.Join(skills, "a", "b", "c", "d", "edge-depth"),
		filepath.Join(skills, "a", "b", "c", "d", "e", "too-deep"),
		filepath.Join(skills, ".hidden", "delta-hidden"),
		filepath.Join(skills, "off-by-name"),
		filepath.Join(skills, "twin"),
		filepath.Join(skills, ".system", "bundled-kit"),
		filepath.Join(profile, ".agents", "skills", "shared-kit"),
		filepath.Join(worktree, ".agents", "skills", "repo-root"),
		filepath.Join(worktree, ".agents", "skills", "twin"),
		filepath.Join(worktree, ".codex", "skills", "proj-codex"),
		filepath.Join(home, "plugins", "cache", "local-mkt", "sample-kit", "0.9.0", "skills", "old-search"),
		filepath.Join(home, "plugins", "cache", "local-mkt", "sample-kit", "1.0.0", "skills", "kit-search"),
		filepath.Join(home, "plugins", "cache", "local-mkt", "blank-kit", "1.0.0", "skills", "blank-skill"),
		filepath.Join(home, "plugins", "cache", "local-mkt", "off-kit", "1.0.0", "skills", "off-search"),
		filepath.Join(home, "plugins", "cache", "local-mkt", "never-kit", "1.0.0", "skills", "never-search"),
	} {
		codexSkillFixture(t, path)
	}
	for _, plugin := range []string{"sample-kit/0.9.0", "sample-kit/1.0.0", "blank-kit/1.0.0", "off-kit/1.0.0", "never-kit/1.0.0"} {
		writeCommandFixture(t, filepath.Join(home, "plugins", "cache", "local-mkt", filepath.FromSlash(plugin), ".codex-plugin", "plugin.json"), `{"name":"`+filepath.Dir(filepath.FromSlash(plugin))+`"}`)
	}
	writeCommandFixture(t, filepath.Join(skills, "no-desc", "SKILL.md"), "---\nname: no-desc\n---\nBody")
	writeCommandFixture(t, filepath.Join(skills, "no-front", "SKILL.md"), "Body")
	writeCommandFixture(t, filepath.Join(skills, "spaced", "SKILL.md"), "---\nname: two words\ndescription: A name no mention carries\n---\nBody")
	writeCommandFixture(t, filepath.Join(home, "prompts", "draft-note.md"), "---\ndescription: An old prompt\n---\nBody")
	writeCommandFixture(t, filepath.Join(home, "config.toml"), `model = "made-up-model"

[plugins."sample-kit@local-mkt"]
enabled = true

[plugins."blank-kit@local-mkt"]

[plugins."off-kit@local-mkt"]
enabled = false

[[skills.config]]
name = "off-by-name"
enabled = false
`)

	// Act
	entries := entriesIn(t, getCommands(board, "task=task-1&token=test-instance"))

	// Assert
	want := []string{"alpha-notes", "blank-kit:blank-skill", "bundled-kit", "edge-depth", "gamma-tool", "proj-codex", "repo-root", "sample-kit:kit-search", "shared-kit"}
	if names := ownNames(entries); !reflect.DeepEqual(names, want) {
		t.Fatalf("skills = %v, want %v", names, want)
	}
	for _, entry := range entries {
		if entry.Source != "builtin" && entry.Description != "A made-up skill" {
			t.Fatalf("skill %+v lacks its description", entry)
		}
	}
}

// A Codex skill is a $ mention and a built-in command a / command, so a
// skill of a built-in command's name leaves the built-in command in the list.
func TestACodexSkillIsAMentionBesideTheBuiltinOfItsName(t *testing.T) {
	// Arrange
	board, profile, _ := commandBoard(t, "codex")
	codexSkillFixture(t, filepath.Join(profile, ".codex", "skills", "model"))
	codexSkillFixture(t, filepath.Join(profile, ".codex", "skills", "alpha-notes"))

	// Act
	entries := entriesIn(t, getCommands(board, "task=task-1&token=test-instance"))

	// Assert
	for _, entry := range entries {
		if wantSigil := map[bool]string{true: "/", false: "$"}[entry.Source == "builtin"]; entry.Sigil != wantSigil {
			t.Fatalf("entry %+v, want sigil %q", entry, wantSigil)
		}
	}
	named := slices.DeleteFunc(entries, func(entry command) bool { return entry.Name != "model" })
	want := []command{{"model", "A made-up skill", "skill", "$"}, {"model", "choose what model and reasoning effort to use", "builtin", "/"}}
	if !reflect.DeepEqual(named, want) {
		t.Fatalf("model entries = %+v, want %+v", named, want)
	}
}

// Each way config.toml can say a setting turns the bundled skills or a
// plugin off is read, and neither a multi-line string nor a multi-line array
// that looks like a table is taken for one.
func TestCodexConfigurationIsReadInEachFormItTakes(t *testing.T) {
	const (
		bundled = "bundled-kit"
		plugin  = "kit:kit-search"
	)
	for _, c := range []struct {
		what   string
		config string
		want   []string
	}{
		{"no configuration", "", []string{bundled}},
		{"tables", "[plugins.\"kit@mkt\"]\nenabled = true\n[skills.bundled]\nenabled = false\n", []string{plugin}},
		{"dotted keys", "plugins.\"kit@mkt\".enabled = true # on\nskills.bundled.enabled = false\n", []string{plugin}},
		{"inline tables", "[plugins]\n'kit@mkt' = { enabled = true }\n[skills]\nbundled = { enabled = false }\n", []string{plugin}},
		{"a plugin turned back off", "[plugins.\"kit@mkt\"]\nenabled = true\n\n[plugins]\n\"kit@mkt\".enabled = false\n", []string{bundled}},
		{"a multi-line string", "instructions = \"\"\"\n[plugins.\"kit@mkt\"]\nenabled = true\n\"\"\"\n", []string{bundled}},
		{"a multi-line array", "notify = [\n  [\"plugins\"],\n  \"[skills.bundled]\",\n]\n[skills.bundled]\nenabled = true\n", []string{bundled}},
		{"an inline table that wraps an array", "[mcp_servers]\nfoo = { command = \"npx\", args = [\n \"-y\", \"pkg\"\n] }\n[plugins.\"kit@mkt\"]\n", []string{bundled, plugin}},
		{"a byte order mark", "\ufeff[plugins.\"kit@mkt\"]\nenabled = true\n[skills.bundled]\nenabled = false\n", []string{plugin}},
		{"values of other kinds", "started = 1979-05-27 07:32:00Z\nretries = 3\nnote = '''\n[skills.bundled]\n'''\nlist = [ # first\n 1, { a = [2] }, # more\n]\n[plugins.\"kit@mkt\"]\nenabled = true\n", []string{bundled, plugin}},
	} {
		t.Run(c.what, func(t *testing.T) {
			// Arrange
			board, profile, _ := commandBoard(t, "codex")
			home := filepath.Join(profile, ".codex")
			codexSkillFixture(t, filepath.Join(home, "skills", ".system", bundled))
			pluginRoot := filepath.Join(home, "plugins", "cache", "mkt", "kit", "local")
			writeCommandFixture(t, filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), `{"name":"kit"}`)
			codexSkillFixture(t, filepath.Join(pluginRoot, "skills", "kit-search"))
			if c.config != "" {
				writeCommandFixture(t, filepath.Join(home, "config.toml"), c.config)
			}

			// Act
			names := commandsIn(t, getCommands(board, "task=task-1&token=test-instance"))

			// Assert
			if !reflect.DeepEqual(names, c.want) {
				t.Fatalf("skills = %v, want %v", names, c.want)
			}
		})
	}
}

// [[skills.config]] rules turn a skill off by its name or its SKILL.md's
// path, a later rule winning over an earlier one, and a configuration Codex
// would refuse to start with offers no skills.
func TestCodexSkillRulesApplyInOrder(t *testing.T) {
	for _, c := range []struct {
		what  string
		rules string
		want  []string
	}{
		{"by name", "[[skills.config]]\nname = \"alpha\"\nenabled = false\n", []string{"beta"}},
		{"by path", "[[skills.config]]\npath = 'skills/beta/SKILL.md'\nenabled = false\n", []string{"alpha"}},
		{"back on by a later rule", "[[skills.config]]\nname = \"alpha\"\nenabled = false\n\n[[skills.config]]\npath = 'skills/alpha/SKILL.md'\nenabled = true\n", []string{"alpha", "beta"}},
		{"a rule that names no skill", "[[skills.config]]\nenabled = false\n", []string{"alpha", "beta"}},
		{"an array of inline tables", "skills.config = [\n { name = \"alpha\", enabled = false },\n]\n", []string{"beta"}},
		{"an array in the skills table", "[skills]\nconfig = [{ path = 'skills/beta/SKILL.md', enabled = false }]\n", []string{"alpha"}},
		{"an unreadable configuration", "[skills\n", []string{}},
	} {
		t.Run(c.what, func(t *testing.T) {
			// Arrange
			board, profile, _ := commandBoard(t, "codex")
			home := filepath.Join(profile, ".codex")
			codexSkillFixture(t, filepath.Join(home, "skills", "alpha"))
			codexSkillFixture(t, filepath.Join(home, "skills", "beta"))
			writeCommandFixture(t, filepath.Join(home, "config.toml"), c.rules)

			// Act
			names := commandsIn(t, getCommands(board, "task=task-1&token=test-instance"))

			// Assert
			if !reflect.DeepEqual(names, c.want) {
				t.Fatalf("skills = %v, want %v", names, c.want)
			}
		})
	}
}

// A plugin's skills are read from the folders its manifest names, in place
// of its skills folder, with the skills its install made of its commands but
// for one of a name its own skill holds, and a plugin folder inside a skills
// folder names the skills below it.
func TestCodexPluginSkillsComeFromWhereItsManifestSays(t *testing.T) {
	// Arrange
	board, profile, _ := commandBoard(t, "codex")
	home := filepath.Join(profile, ".codex")
	pluginRoot := filepath.Join(home, "plugins", "cache", "mkt", "kit", "2.0.0")
	writeCommandFixture(t, filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), `{"name":"kit-ns","skills":["./more","../outside"]}`)
	codexSkillFixture(t, filepath.Join(pluginRoot, "more", "listed"))
	codexSkillFixture(t, filepath.Join(pluginRoot, "skills", "unlisted"))
	codexSkillFixture(t, filepath.Join(pluginRoot, ".codex-plugin", "migrated-command-skills", "tidy"))
	codexSkillFixture(t, filepath.Join(pluginRoot, ".codex-plugin", "migrated-command-skills", "listed"))
	codexSkillFixture(t, filepath.Join(home, "plugins", "cache", "mkt", "outside", "listed-outside"))
	vendor := filepath.Join(home, "skills", "vendor-folder")
	writeCommandFixture(t, filepath.Join(vendor, ".claude-plugin", "plugin.json"), `{"name":"vendor"}`)
	codexSkillFixture(t, filepath.Join(vendor, "skills", "look"))
	writeCommandFixture(t, filepath.Join(home, "config.toml"), "[plugins.\"kit@mkt\"]\n")

	// Act
	names := commandsIn(t, getCommands(board, "task=task-1&token=test-instance"))

	// Assert
	if want := []string{"kit-ns:listed", "kit-ns:tidy", "vendor:look"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("skills = %v, want %v", names, want)
	}
}

// Codex loads the version folder named local when there is one, and else the
// latest by semantic version, or by name when a folder is not one.
func TestCodexLoadsAPluginsActiveVersion(t *testing.T) {
	for _, c := range []struct {
		versions []string
		want     string
	}{
		{[]string{"1.0.0", "local", "2.0.0"}, "local"},
		{[]string{"1.9.0", "1.10.0"}, "1.10.0"},
		{[]string{"1.0.0", "1.0.0-rc.1"}, "1.0.0"},
		{[]string{"1.0.0-rc.2", "1.0.0-rc.10", "1.0.0-beta"}, "1.0.0-rc.10"},
		{[]string{"1.0.0-alpha", "1.0.0-alpha.1"}, "1.0.0-alpha.1"},
		{[]string{"abc", "abd"}, "abd"},
		{[]string{"bad name"}, ""},
		{nil, ""},
	} {
		t.Run(c.want, func(t *testing.T) {
			// Arrange
			base := t.TempDir()
			for _, version := range c.versions {
				if err := os.MkdirAll(filepath.Join(base, version), 0o700); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			version := codexActivePluginVersion(base)

			// Assert
			if version != c.want {
				t.Fatalf("active version of %v = %q, want %q", c.versions, version, c.want)
			}
		})
	}
}

// Codex reads project skills from each folder from the project's root down
// to the session's folder, and from the session's folder alone outside a
// project.
func TestCodexReadsProjectSkillsFromTheRootDown(t *testing.T) {
	// Arrange
	root := t.TempDir()
	inner := filepath.Join(root, "project", "inner")
	if err := os.MkdirAll(inner, 0o700); err != nil {
		t.Fatal(err)
	}

	// Act
	outside := codexProjectFolders(inner)
	writeCommandFixture(t, filepath.Join(root, "project", ".git"), "")
	inside := codexProjectFolders(inner)

	// Assert
	if want := []string{inner}; !reflect.DeepEqual(outside, want) {
		t.Fatalf("folders outside a project = %v, want %v", outside, want)
	}
	if want := []string{filepath.Join(root, "project"), inner}; !reflect.DeepEqual(inside, want) {
		t.Fatalf("folders inside a project = %v, want %v", inside, want)
	}
}
