package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// allowRules returns the permissions.allow list of a settings file, in order.
func allowRules(t *testing.T, path string) []string {
	t.Helper()
	var document struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(readFile(t, path)), &document); err != nil {
		t.Fatalf("%s is not valid JSON: %v", path, err)
	}
	return document.Permissions.Allow
}

// writeAllowRules replaces the permissions.allow list of a settings file, as
// someone editing it by hand would.
func writeAllowRules(t *testing.T, path string, rules []string) {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(readFile(t, path)), &document); err != nil {
		t.Fatal(err)
	}
	permissions, _ := document["permissions"].(map[string]any)
	if permissions == nil {
		permissions = map[string]any{}
		document["permissions"] = permissions
	}
	permissions["allow"] = rules
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(data))
}

// The rules are exactly the commands that only put an item in front of the
// Overlord, for both shell tools; cfo answer, which types into a goblin's
// terminal, and cfo send are not among them.
func TestPermissionRulesAreTheBoardCommandsForBothShells(t *testing.T) {
	want := []string{
		"Bash(cfo question *)", "Bash(cfo run-request *)", "Bash(cfo review *)", "Bash(cfo present *)", "Bash(cfo deliver *)", "Bash(cfo auth request *)",
		"PowerShell(cfo question *)", "PowerShell(cfo run-request *)", "PowerShell(cfo review *)", "PowerShell(cfo present *)", "PowerShell(cfo deliver *)", "PowerShell(cfo auth request *)",
	}

	got := PermissionRules()

	if !slices.Equal(got, want) {
		t.Errorf("PermissionRules() = %q, want %q", got, want)
	}
}

func TestInstallAddsEachCommandCenterRuleOnceAndKeepsTheAdoptersOwn(t *testing.T) {
	// Arrange
	f := newFixture(t, adopterSettings, nil)

	// Act
	output := f.install()

	// Assert
	rules := allowRules(t, f.user)
	for _, rule := range append(PermissionRules(), "Bash(git:*)") {
		if count(rules, rule) != 1 {
			t.Errorf("allow rule %q appears %d times, want 1\n%s", rule, count(rules, rule), strings.Join(rules, "\n"))
		}
	}
	if rules[0] != "Bash(git:*)" {
		t.Errorf("the adopter's own rule moved: allow = %q", rules)
	}
	for _, foreign := range foreignCommands {
		if count(hookCommands(t, f.user), foreign) != 1 {
			t.Errorf("adopter hook %q was not kept", foreign)
		}
	}
	if want := "added 12 Command Center allow rules to " + f.user; !strings.Contains(output, want) {
		t.Errorf("output is missing %q:\n%s", want, output)
	}
}

// A machine installed before the rules existed has the CFO hooks and no
// rules: a re-install repairs it, backing the file up as it stood first.
func TestReinstallAddsMissingRulesAndBacksUpTheFileFirst(t *testing.T) {
	// Arrange
	f := newFixture(t, adopterSettings, nil)
	f.install()
	writeAllowRules(t, f.user, []string{"Bash(git:*)"})
	if err := os.Remove(f.user + rulesRecordSuffix); err != nil {
		t.Fatal(err)
	}
	before := readFile(t, f.user)

	// Act
	output := f.install()

	// Assert
	if backup := readFile(t, f.user+backupSuffix); backup != before {
		t.Errorf("the backup is not the file as it stood before the repair\nbefore: %s\nbackup: %s", before, backup)
	}
	rules := allowRules(t, f.user)
	for _, rule := range PermissionRules() {
		if count(rules, rule) != 1 {
			t.Errorf("allow rule %q appears %d times after the repair, want 1", rule, count(rules, rule))
		}
	}
	for _, want := range []string{"unchanged none of the CFO's in " + f.user, "added 12 Command Center allow rules"} {
		if !strings.Contains(output, want) {
			t.Errorf("output is missing %q:\n%s", want, output)
		}
	}
}

func TestReinstallWithTheRulesInPlaceWritesNothing(t *testing.T) {
	// Arrange
	f := newFixture(t, adopterSettings, nil)
	f.install()
	settings, record := readFile(t, f.user), readFile(t, f.user+rulesRecordSuffix)

	// Act
	output := f.install()

	// Assert
	if got := readFile(t, f.user); got != settings {
		t.Errorf("a re-install rewrote the settings file:\n%s", got)
	}
	if got := readFile(t, f.user+rulesRecordSuffix); got != record {
		t.Errorf("a re-install rewrote the record of the rules it added:\n%s", got)
	}
	if want := "unchanged the 12 Command Center allow rules are already in " + f.user; !strings.Contains(output, want) {
		t.Errorf("output is missing %q:\n%s", want, output)
	}
}

// A rule the adopter wrote before the install, in either form Claude Code
// reads, is theirs: the install does not add it again and the uninstall
// leaves it.
func TestUninstallRemovesOnlyTheRulesTheInstallAdded(t *testing.T) {
	// Arrange
	own := []string{"Bash(git:*)", "Bash(cfo question *)", "PowerShell(cfo deliver:*)"}
	f := newFixture(t, adopterSettings, nil)
	writeAllowRules(t, f.user, own)
	before := readFile(t, f.user)
	f.install()
	rules := allowRules(t, f.user)
	for _, duplicate := range []string{"PowerShell(cfo deliver *)"} {
		if slices.Contains(rules, duplicate) {
			t.Errorf("install added %q beside the adopter's own %q", duplicate, "PowerShell(cfo deliver:*)")
		}
	}
	if got := len(rules); got != len(own)+10 {
		t.Errorf("install left %d allow rules, want the adopter's %d and the 10 it lacked:\n%s", got, len(own), strings.Join(rules, "\n"))
	}

	// Act
	output := f.uninstall()

	// Assert
	if got := allowRules(t, f.user); !slices.Equal(got, own) {
		t.Errorf("allow after uninstall = %q, want the adopter's own %q", got, own)
	}
	if got, want := canonicalJSON(t, readFile(t, f.user)), canonicalJSON(t, before); got != want {
		t.Errorf("uninstall did not restore the settings document\n before: %s\n  after: %s", want, got)
	}
	if _, err := os.Stat(f.user + rulesRecordSuffix); !os.IsNotExist(err) {
		t.Errorf("the record of the rules the install added survived the uninstall: %v", err)
	}
	if want := "removed the 10 Command Center allow rules cfo install added"; !strings.Contains(output, want) {
		t.Errorf("output is missing %q:\n%s", want, output)
	}
}

// A permissions block the install created leaves with the uninstall, so the
// file reads as it did before.
func TestUninstallDropsThePermissionsBlockTheInstallCreated(t *testing.T) {
	// Arrange
	original := `{"model": "claude-opus-5"}`
	f := newFixture(t, original, nil)
	f.install()

	// Act
	f.uninstall()

	// Assert
	if got, want := canonicalJSON(t, readFile(t, f.user)), canonicalJSON(t, original); got != want {
		t.Errorf("uninstall left %s, want %s", got, want)
	}
}

func TestUninstallWithNoRecordKeepsTheRules(t *testing.T) {
	// Arrange
	f := newFixture(t, adopterSettings, nil)
	f.install()
	if err := os.Remove(f.user + rulesRecordSuffix); err != nil {
		t.Fatal(err)
	}

	// Act
	f.uninstall()

	// Assert
	rules := allowRules(t, f.user)
	for _, rule := range PermissionRules() {
		if count(rules, rule) != 1 {
			t.Errorf("uninstall without a record removed %q, which nothing says the install added", rule)
		}
	}
}

func TestInstallRefusesAMalformedPermissionsBlock(t *testing.T) {
	for name, settings := range map[string]string{
		"permissions not an object": `{"permissions": "surprise"}`,
		"allow not an array":        `{"permissions": {"allow": "Bash(git:*)"}}`,
		"a rule not a string":       `{"permissions": {"allow": [42]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			f := newFixture(t, settings, nil)

			// Act
			var out strings.Builder
			err := f.service.Install(&out)

			// Assert
			if err == nil || !strings.Contains(err.Error(), `"permissions`) {
				t.Fatalf("Install() error = %v, want it to name the malformed permissions block", err)
			}
			if got := readFile(t, f.user); got != settings {
				t.Errorf("a settings file it refused to understand was rewritten: %s", got)
			}
			if _, err := os.Stat(f.user + rulesRecordSuffix); !os.IsNotExist(err) {
				t.Errorf("a refused install wrote a record of rules it never added: %v", err)
			}
		})
	}
}

func TestReadPermissionsNamesTheMissingRulesAndTheClassifySwitch(t *testing.T) {
	cases := []struct {
		name             string
		settings         string
		missing          []string
		classifyAllShell bool
	}{
		{name: "no file", missing: PermissionRules()},
		{name: "empty", settings: `{}`, missing: PermissionRules()},
		{name: "all in place", settings: settingsWithRules(PermissionRules()...)},
		{name: "two gone", settings: settingsWithRules(PermissionRules()[2:]...), missing: PermissionRules()[:2]},
		{name: "colon-star form", settings: settingsWithRules(append([]string{"Bash(cfo question:*)"}, PermissionRules()[1:]...)...)},
		{name: "classifier judges every shell command", settings: `{"autoMode": {"classifyAllShell": true}, "permissions": {"allow": ` + jsonList(PermissionRules()) + `}}`, classifyAllShell: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			path := filepath.Join(t.TempDir(), "settings.json")
			if c.settings != "" {
				writeFile(t, path, c.settings)
			}

			// Act
			got, err := ReadPermissions(path)

			// Assert
			if err != nil {
				t.Fatalf("ReadPermissions: %v", err)
			}
			if !slices.Equal(got.Missing, c.missing) {
				t.Errorf("Missing = %q, want %q", got.Missing, c.missing)
			}
			if got.ClassifyAllShell != c.classifyAllShell {
				t.Errorf("ClassifyAllShell = %v, want %v", got.ClassifyAllShell, c.classifyAllShell)
			}
		})
	}
}

func TestReadPermissionsRefusesAMalformedPermissionsBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, path, `{"permissions": []}`)

	if _, err := ReadPermissions(path); err == nil {
		t.Fatal("ReadPermissions accepted a permissions key that is not an object")
	}
}

func settingsWithRules(rules ...string) string {
	return `{"permissions": {"allow": ` + jsonList(rules) + `}}`
}

func jsonList(values []string) string {
	data, _ := json.Marshal(values)
	return string(data)
}

// canonicalJSON renders a JSON document with sorted keys and no spacing, so
// two documents compare by content.
func canonicalJSON(t *testing.T, document string) string {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(document), &value); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A record that cannot be written stops the install before the settings
// change, so no rule is ever added that the uninstall would not find.
func TestInstallThatCannotRecordTheRulesAddsNone(t *testing.T) {
	// Arrange: a record the install reads but cannot replace.
	f := newFixture(t, adopterSettings, nil)
	record := f.user + rulesRecordSuffix
	writeFile(t, record, "[]")
	if err := os.Chmod(record, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(record, 0o644) })

	// Act
	var out strings.Builder
	err := f.service.Install(&out)

	// Assert
	if err == nil {
		t.Fatalf("install succeeded with nowhere to record the rules it adds:\n%s", out.String())
	}
	if got := readFile(t, f.user); got != adopterSettings {
		t.Errorf("the settings changed although the rules could not be recorded:\n%s", got)
	}
}

// A settings write that fails takes the record back to what it was, so the
// record never claims a rule the file does not hold, which a rule the
// adopter later writes by hand would otherwise answer to.
func TestInstallThatCannotWriteTheSettingsLeavesTheRecordAsItWas(t *testing.T) {
	for name, prior := range map[string]string{
		"no record":    "",
		"an older one": `["Bash(cfo question *)"]`,
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			f := newFixture(t, adopterSettings, nil)
			if prior != "" {
				writeFile(t, f.user+rulesRecordSuffix, prior)
			}
			// The backup is taken first, so a folder in its place fails the
			// settings write before the file changes.
			if err := os.MkdirAll(f.user+backupSuffix, 0o755); err != nil {
				t.Fatal(err)
			}

			// Act
			var out strings.Builder
			err := f.service.Install(&out)

			// Assert
			if err == nil {
				t.Fatalf("install succeeded although the settings could not be written:\n%s", out.String())
			}
			if got := readFile(t, f.user); got != adopterSettings {
				t.Errorf("the settings changed:\n%s", got)
			}
			record, readErr := os.ReadFile(f.user + rulesRecordSuffix)
			switch {
			case prior == "" && !os.IsNotExist(readErr):
				t.Errorf("a failed install left a record of rules it never added: %s (%v)", record, readErr)
			case prior != "" && (readErr != nil || canonicalJSON(t, string(record)) != canonicalJSON(t, prior)):
				t.Errorf("a failed install left the record as %s (%v), want it as it was, %s", record, readErr, prior)
			}
		})
	}
}

// An uninstall with no record of added rules has no rule to remove, so it
// never reads the permissions block, and one it cannot read does not stop it
// taking out the hooks and the environment.
func TestUninstallWithNoRecordIgnoresAMalformedPermissionsBlock(t *testing.T) {
	// Arrange
	f := newFixture(t, adopterSettings, map[string]string{"Path": `C:\Windows`})
	f.install()
	if err := os.Remove(f.user + rulesRecordSuffix); err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(readFile(t, f.user)), &document); err != nil {
		t.Fatal(err)
	}
	document["permissions"] = "surprise"
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, f.user, string(data))

	// Act
	f.uninstall()

	// Assert
	for _, command := range hookCommands(t, f.user) {
		if isCFOCommand(command) {
			t.Errorf("CFO hook %q survived the uninstall", command)
		}
	}
	if _, ok := f.env.values["CFO_HOME"]; ok {
		t.Error("CFO_HOME survived the uninstall")
	}
}

func TestReadRulesRecordAcceptsAByteOrderMark(t *testing.T) {
	settings := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, settings+rulesRecordSuffix, "\xef\xbb\xbf"+`["Bash(cfo question *)"]`)

	got, err := readRulesRecord(settings)

	if err != nil || !slices.Equal(got, []string{"Bash(cfo question *)"}) {
		t.Errorf("readRulesRecord = %q, %v; want the one rule", got, err)
	}
}
