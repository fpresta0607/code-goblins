package install

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// adopterSettings is a stand-in for a real ~/.claude/settings.json: unrelated
// hooks on four events, plus top-level keys this package knows nothing about.
// Every assertion about "an adopter's configuration survives" is made against
// this, never against a machine's actual file.
const adopterSettings = `{
  "env": {"CLAUDE_AUTOCOMPACT_PCT_OVERRIDE": "50"},
  "model": "claude-opus-5",
  "permissions": {"allow": ["Bash(git:*)"]},
  "hooks": {
    "PostToolUse": [
      {"matcher": "Write|Edit", "hooks": [{"type": "command", "command": "node track-edit.js", "async": true}]}
    ],
    "PreToolUse": [
      {"matcher": "Write", "hooks": [{"type": "command", "command": "bash -c 'markdown write guard'"}]}
    ],
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "node session-start.js"}]},
      {"matcher": "", "hooks": [{"type": "command", "command": "lavish-axi", "timeout": 10}]}
    ],
    "SessionEnd": [
      {"hooks": [{"type": "command", "command": "clock-in-hook.exe --event session-end"}]}
    ],
    "Stop": [
      {"hooks": [{"type": "command", "command": "node session-end.js"}]}
    ]
  }
}`

// foreignCommands are the adopter's own hook commands, none of which install
// or uninstall may ever touch.
var foreignCommands = []string{
	"node track-edit.js",
	"bash -c 'markdown write guard'",
	"node session-start.js",
	"lavish-axi",
	"clock-in-hook.exe --event session-end",
	"node session-end.js",
}

// fakeEnv is the user-scope environment as a map, so no test ever reads or
// writes the machine's own registry.
type fakeEnv struct {
	values     map[string]string
	broadcasts int
	setCalls   []string
	getErr     error
}

func newFakeEnv(values map[string]string) *fakeEnv {
	copied := map[string]string{}
	for name, value := range values {
		copied[name] = value
	}
	return &fakeEnv{values: copied}
}

func (e *fakeEnv) Get(name string) (string, bool, error) {
	if e.getErr != nil {
		return "", false, e.getErr
	}
	value, ok := e.values[name]
	return value, ok, nil
}

func (e *fakeEnv) Set(name, value string) error {
	e.values[name] = value
	e.setCalls = append(e.setCalls, name)
	return nil
}

func (e *fakeEnv) Unset(name string) error {
	delete(e.values, name)
	e.setCalls = append(e.setCalls, "-"+name)
	return nil
}

func (e *fakeEnv) Broadcast() error {
	e.broadcasts++
	return nil
}

// fixture builds a temp machine: a checkout root, a user settings file
// holding adopterSettings, and a fake user environment.
type fixture struct {
	t       *testing.T
	root    string
	user    string
	repo    string
	env     *fakeEnv
	service Service
}

func newFixture(t *testing.T, userSettings string, env map[string]string) *fixture {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "code-goblins")
	repo := filepath.Join(root, ".claude", "settings.json")
	user := filepath.Join(dir, "home", ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(user), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(repo), 0o755); err != nil {
		t.Fatal(err)
	}
	if userSettings != "" {
		if err := os.WriteFile(user, []byte(userSettings), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f := &fixture{t: t, root: root, user: user, repo: repo, env: newFakeEnv(env)}
	f.service = Service{Root: root, UserSettings: user, RepoSettings: repo, Env: f.env}
	return f
}

func (f *fixture) writeRepoSettings(content string) {
	f.t.Helper()
	if err := os.WriteFile(f.repo, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) install() string {
	f.t.Helper()
	var out strings.Builder
	if err := f.service.Install(&out); err != nil {
		f.t.Fatalf("Install: %v\n%s", err, out.String())
	}
	return out.String()
}

func (f *fixture) uninstall() string {
	f.t.Helper()
	var out strings.Builder
	if err := f.service.Uninstall(&out); err != nil {
		f.t.Fatalf("Uninstall: %v\n%s", err, out.String())
	}
	return out.String()
}

// hookCommands returns every hook command in a settings file, in sorted
// order, so a test can assert on the whole set rather than on one path
// through it.
func hookCommands(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("%s is not valid JSON: %v\n%s", path, err, raw)
	}
	commands := []string{}
	for _, groups := range document.Hooks {
		for _, group := range groups {
			for _, entry := range group.Hooks {
				commands = append(commands, entry.Command)
			}
		}
	}
	sort.Strings(commands)
	return commands
}

func count(values []string, want string) int {
	total := 0
	for _, value := range values {
		if value == want {
			total++
		}
	}
	return total
}

func TestInstallMergesIntoAnAdoptersSettings(t *testing.T) {
	f := newFixture(t, adopterSettings, map[string]string{"Path": `C:\Windows;C:\Windows\System32`})
	output := f.install()

	commands := hookCommands(t, f.user)
	for _, foreign := range foreignCommands {
		if count(commands, foreign) != 1 {
			t.Errorf("adopter hook %q appears %d times, want 1\n%s", foreign, count(commands, foreign), strings.Join(commands, "\n"))
		}
	}
	for _, group := range cfoHookGroups() {
		for _, entry := range group.entries {
			want := entry["command"].(string)
			if count(commands, want) != 1 {
				t.Errorf("CFO hook %q appears %d times, want 1", want, count(commands, want))
			}
		}
	}

	// Unknown keys and unknown hook events survive untouched.
	raw, err := os.ReadFile(f.user)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"env", "model", "permissions"} {
		if _, ok := document[key]; !ok {
			t.Errorf("top-level key %q was dropped", key)
		}
	}

	if !strings.Contains(output, "left 6 hook(s) that are not the CFO's") {
		t.Errorf("output does not report what it left alone:\n%s", output)
	}
}

func TestInstallBacksUpBeforeWriting(t *testing.T) {
	f := newFixture(t, adopterSettings, nil)
	f.install()

	backup, err := os.ReadFile(f.user + backupSuffix)
	if err != nil {
		t.Fatalf("no backup was written: %v", err)
	}
	if string(backup) != adopterSettings {
		t.Errorf("backup is not the file as it was found:\n%s", backup)
	}
}

func TestInstallTwiceChangesNothingTheSecondTime(t *testing.T) {
	f := newFixture(t, adopterSettings, map[string]string{"Path": `C:\Windows`})
	f.install()

	first, err := os.ReadFile(f.user)
	if err != nil {
		t.Fatal(err)
	}
	beforeSets := len(f.env.setCalls)
	beforeBroadcasts := f.env.broadcasts

	output := f.install()

	second, err := os.ReadFile(f.user)
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != string(first) {
		t.Errorf("a second install rewrote the settings file:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
	if len(f.env.setCalls) != beforeSets {
		t.Errorf("a second install wrote the environment again: %v", f.env.setCalls[beforeSets:])
	}
	if f.env.broadcasts != beforeBroadcasts {
		t.Errorf("a second install broadcast a change it did not make")
	}
	if !strings.Contains(output, "already installed - nothing changed") {
		t.Errorf("a second install did not report itself as a no-op:\n%s", output)
	}
	for _, want := range []string{"unchanged already " + f.root, "unchanged already contains " + f.root, "unchanged already in " + f.user} {
		if !strings.Contains(output, want) {
			t.Errorf("output is missing %q:\n%s", want, output)
		}
	}
}

func TestUninstallRestoresThePriorState(t *testing.T) {
	f := newFixture(t, adopterSettings, map[string]string{"Path": `C:\Windows;C:\Windows\System32`})
	f.install()
	f.uninstall()

	commands := hookCommands(t, f.user)
	for _, foreign := range foreignCommands {
		if count(commands, foreign) != 1 {
			t.Errorf("adopter hook %q was not restored intact (%d occurrences)", foreign, count(commands, foreign))
		}
	}
	for _, command := range commands {
		if strings.HasPrefix(command, rootPrefix) {
			t.Errorf("CFO hook %q survived the uninstall", command)
		}
	}

	var before, after map[string]any
	if err := json.Unmarshal([]byte(adopterSettings), &before); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(f.user)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &after); err != nil {
		t.Fatal(err)
	}
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Errorf("uninstall did not restore the settings document\n before: %s\n  after: %s", beforeJSON, afterJSON)
	}

	if value, ok := f.env.values["CFO_HOME"]; ok {
		t.Errorf("CFO_HOME survived the uninstall as %q", value)
	}
	if got, want := f.env.values["Path"], `C:\Windows;C:\Windows\System32`; got != want {
		t.Errorf("PATH = %q, want %q", got, want)
	}
}

func TestUninstallTwiceIsANoOp(t *testing.T) {
	f := newFixture(t, adopterSettings, nil)
	f.install()
	f.uninstall()
	output := f.uninstall()
	if !strings.Contains(output, "nothing to remove") {
		t.Errorf("a second uninstall did not report itself as a no-op:\n%s", output)
	}
}

func TestUninstallWithNoUserSettingsFileCreatesNothing(t *testing.T) {
	f := newFixture(t, "", nil)
	output := f.uninstall()

	if _, err := os.Stat(f.user); !os.IsNotExist(err) {
		t.Errorf("uninstall created a settings file on a machine that had none: %v", err)
	}
	if !strings.Contains(output, "nothing to remove") {
		t.Errorf("output does not report the no-op:\n%s", output)
	}
}

func TestInstallWithNoUserSettingsFileCreatesOne(t *testing.T) {
	f := newFixture(t, "", nil)
	f.install()

	commands := hookCommands(t, f.user)
	if len(commands) != 6 {
		t.Fatalf("got %d hooks, want the 6 CFO hooks:\n%s", len(commands), strings.Join(commands, "\n"))
	}
	if _, err := os.Stat(f.user + backupSuffix); !os.IsNotExist(err) {
		t.Errorf("a file that did not exist was backed up")
	}
}

func TestEveryInstalledHookResolvesThroughCFOHome(t *testing.T) {
	f := newFixture(t, adopterSettings, nil)
	f.install()

	installed := 0
	for _, command := range hookCommands(t, f.user) {
		if !strings.HasPrefix(command, rootPrefix) {
			continue
		}
		installed++
		// This is what lets a session opened in a different repository be
		// supervised: CFO_HOME wins, and $CLAUDE_PROJECT_DIR is only the
		// fallback for an adopter who has not run install yet.
		if !strings.Contains(command, `${CFO_HOME:-$CLAUDE_PROJECT_DIR}`) {
			t.Errorf("hook %q does not resolve through CFO_HOME", command)
		}
	}
	if installed != 6 {
		t.Errorf("installed %d CFO hooks, want 6", installed)
	}
}

func TestInstallRemovesTheRepoHooksBlockAndKeepsTheRest(t *testing.T) {
	f := newFixture(t, adopterSettings, nil)
	f.writeRepoSettings(`{
  "permissions": {"allow": ["Bash(./cfo.exe *)"]},
  "hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "$CLAUDE_PROJECT_DIR/cfo.exe hook session-start"}]}]}
}`)
	output := f.install()

	if commands := hookCommands(t, f.repo); len(commands) != 0 {
		t.Errorf("the repo still carries hooks, so every hook fires twice inside code-goblins: %v", commands)
	}
	raw, err := os.ReadFile(f.repo)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if _, ok := document["permissions"]; !ok {
		t.Errorf("the repo permissions block was destroyed: %s", raw)
	}
	if _, ok := document["hooks"]; ok {
		t.Errorf("the repo hooks block survived: %s", raw)
	}
	if !strings.Contains(output, "removed the duplicate hooks block") {
		t.Errorf("output does not report the repo cleanup:\n%s", output)
	}
	if _, err := os.Stat(f.repo + backupSuffix); err != nil {
		t.Errorf("the repo settings file was not backed up: %v", err)
	}
}

func TestInstallReportsAnAbsentRepoSettingsFile(t *testing.T) {
	f := newFixture(t, adopterSettings, nil)
	output := f.install()
	if !strings.Contains(output, "unchanged no "+f.repo) {
		t.Errorf("output does not report the missing repo settings file:\n%s", output)
	}
}

func TestInstallSetsHomeAndPath(t *testing.T) {
	f := newFixture(t, adopterSettings, map[string]string{"Path": `C:\Windows;C:\Windows\System32`})
	f.install()

	if got := f.env.values["CFO_HOME"]; got != f.root {
		t.Errorf("CFO_HOME = %q, want %q", got, f.root)
	}
	want := `C:\Windows;C:\Windows\System32;` + f.root
	if got := f.env.values["Path"]; got != want {
		t.Errorf("PATH = %q, want %q", got, want)
	}
	if f.env.broadcasts != 1 {
		t.Errorf("broadcasts = %d, want exactly 1", f.env.broadcasts)
	}
}

func TestInstallLeavesAnAlreadyCorrectEnvironmentAlone(t *testing.T) {
	f := newFixture(t, adopterSettings, nil)
	f.env.values["CFO_HOME"] = f.root + `\`
	f.env.values["Path"] = `C:\Windows;` + strings.ToUpper(f.root)
	f.install()

	if len(f.env.setCalls) != 0 {
		t.Errorf("the environment was rewritten: %v", f.env.setCalls)
	}
	if f.env.broadcasts != 0 {
		t.Errorf("a change was broadcast that was never made")
	}
}

func TestInstallDoesNotDuplicateAPathEntryWrittenWithVariables(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CFO_TEST_TOOLS", dir)
	f := newFixture(t, adopterSettings, map[string]string{"Path": `C:\Windows;%CFO_TEST_TOOLS%\code-goblins`})
	f.service.Root = filepath.Join(dir, "code-goblins")
	f.install()

	if got, want := f.env.values["Path"], `C:\Windows;%CFO_TEST_TOOLS%\code-goblins`; got != want {
		t.Errorf("PATH = %q, want it untouched at %q", got, want)
	}
}

func TestInstallRefusesWithNothingChangedWhenTheEnvironmentIsUnreadable(t *testing.T) {
	f := newFixture(t, adopterSettings, nil)
	f.env.getErr = errors.New("install: user-scope environment variables are Windows-only")
	var out strings.Builder
	if err := f.service.Install(&out); err == nil {
		t.Fatal("install proceeded on a machine whose environment store cannot be read")
	}
	raw, err := os.ReadFile(f.user)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != adopterSettings {
		t.Errorf("the settings file was written before the environment refusal:\n%s", raw)
	}
	if _, err := os.Stat(f.user + backupSuffix); !os.IsNotExist(err) {
		t.Errorf("a backup was written for an install that refused: %v", err)
	}
}

func TestUninstallRefusesWithNothingChangedWhenTheEnvironmentIsUnreadable(t *testing.T) {
	f := newFixture(t, adopterSettings, map[string]string{"Path": `C:\Windows`})
	f.install()
	installed, err := os.ReadFile(f.user)
	if err != nil {
		t.Fatal(err)
	}

	f.env.getErr = errors.New("registry unreadable")
	var out strings.Builder
	if err := f.service.Uninstall(&out); err == nil {
		t.Fatal("uninstall proceeded on a machine whose environment store cannot be read")
	}
	after, err := os.ReadFile(f.user)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(installed) {
		t.Errorf("the settings file was rewritten before the environment refusal:\n%s", after)
	}
}

func TestInstallWarnsWhenTheBinaryIsMissing(t *testing.T) {
	f := newFixture(t, adopterSettings, nil)
	output := f.install()

	for _, want := range []string{"WARNING", "UNSUPERVISED", "go build ./cmd/cfo"} {
		if !strings.Contains(output, want) {
			t.Errorf("output is missing %q on a root with no cfo.exe:\n%s", want, output)
		}
	}
}

func TestInstallDoesNotWarnWhenTheBinaryIsPresent(t *testing.T) {
	f := newFixture(t, adopterSettings, nil)
	if err := os.WriteFile(filepath.Join(f.root, "cfo.exe"), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	output := f.install()

	if strings.Contains(output, "WARNING") {
		t.Errorf("output warns about a binary that exists:\n%s", output)
	}
}

func TestInstallRefusesAMalformedHooksBlock(t *testing.T) {
	f := newFixture(t, `{"hooks": "surprise"}`, nil)
	var out strings.Builder
	err := f.service.Install(&out)
	if err == nil {
		t.Fatal("install accepted a settings file whose hooks key is not an object")
	}
	if !strings.Contains(err.Error(), "not an object") {
		t.Errorf("error = %v, want it to name the malformed hooks key", err)
	}
	raw, readErr := os.ReadFile(f.user)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(raw) != `{"hooks": "surprise"}` {
		t.Errorf("a settings file it refused to understand was rewritten anyway: %s", raw)
	}
}

func TestInstallRefusesSettingsThatAreNotJSON(t *testing.T) {
	f := newFixture(t, "{ this is not json", nil)
	var out strings.Builder
	if err := f.service.Install(&out); err == nil {
		t.Fatal("install accepted a settings file that is not JSON")
	}
	raw, err := os.ReadFile(f.user)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{ this is not json" {
		t.Errorf("an unparseable settings file was rewritten: %s", raw)
	}
}

func TestInstallAcceptsASettingsFileWithAByteOrderMark(t *testing.T) {
	f := newFixture(t, "\xef\xbb\xbf"+adopterSettings, nil)
	f.install()

	commands := hookCommands(t, f.user)
	for _, foreign := range foreignCommands {
		if count(commands, foreign) != 1 {
			t.Errorf("adopter hook %q was lost from a BOM-prefixed file", foreign)
		}
	}
}

func TestExpandWindowsVarsLeavesUnknownNamesAlone(t *testing.T) {
	t.Setenv("CFO_TEST_ONE", "one")
	cases := map[string]string{
		`%CFO_TEST_ONE%\bin`:            `one\bin`,
		`%CFO_TEST_MISSING%\bin`:        `%CFO_TEST_MISSING%\bin`,
		`C:\plain`:                      `C:\plain`,
		`%CFO_TEST_ONE%`:                `one`,
		`50%% off`:                      `50%% off`,
		`%CFO_TEST_ONE%\%CFO_TEST_ONE%`: `one\one`,
	}
	for input, want := range cases {
		if got := expandWindowsVars(input); got != want {
			t.Errorf("expandWindowsVars(%q) = %q, want %q", input, got, want)
		}
	}
}
