package installscript

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/installtest"
	"github.com/fpresta0607/code-goblins/internal/onboarding"
)

// skillsCliPinned finds the version of the skills CLI install.ps1 pins.
var skillsCliPinned = regexp.MustCompile(`\$skillsCliVersion = "([^"]+)"`)

// npmPackage finds each package install.ps1 installs with npm, by name, and
// the version it names after the name's own @, if any.
var npmPackage = regexp.MustCompile(`npm\.cmd install -g ((?:@[^/\s"]+/)?[^@\s"]+)(?:@([^\s"]*))?"`)

// exactVersion is a version npm resolves to one release only.
var exactVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// installNote finds the install's one Note line on what it could not install.
var installNote = regexp.MustCompile(`Note: These could not be installed[^\r\n]*`)

// skillsCliPin is the version of the skills CLI install.ps1 pins.
func skillsCliPin(t *testing.T) string {
	t.Helper()
	source, err := os.ReadFile(installScript(t))
	if err != nil {
		t.Fatal(err)
	}
	found := skillsCliPinned.FindSubmatch(source)
	if found == nil {
		t.Fatal("install.ps1 pins no $skillsCliVersion")
	}
	return string(found[1])
}

// Every package the install fetches from npm, the skills CLI it runs through
// npx among them, names an exact version, so a release upstream reaches users
// only when it is pinned here. The quick start installs an agent the way
// install.ps1 does, at the version install.ps1 pins.
func TestTheInstallPinsEveryPackageItFetchesFromNpm(t *testing.T) {
	// Arrange
	source, err := os.ReadFile(installScript(t))
	if err != nil {
		t.Fatal(err)
	}

	// Act
	packages := npmPackage.FindAllStringSubmatch(string(source), -1)

	// Assert
	if len(packages) == 0 {
		t.Fatal("install.ps1 installs nothing with npm.cmd install -g, which this test reads")
	}
	pinned := map[string]string{}
	for _, found := range packages {
		if !exactVersion.MatchString(found[2]) {
			t.Errorf("install.ps1 installs %s at %q, want an exact version", found[1], found[2])
		}
		pinned[found[1]] = found[2]
	}
	if version := skillsCliPin(t); !exactVersion.MatchString(version) {
		t.Errorf("install.ps1 pins the skills CLI at %q, want an exact version", version)
	}
	for _, agent := range []string{"codex", "pi"} {
		installer, ok := onboarding.InstallerFor(agent)
		if !ok || installer.Kind != "npm" {
			t.Fatalf("the quick start installs %s as %+v, want an npm package", agent, installer)
		}
		at := strings.LastIndex(installer.Source, "@")
		if at <= 0 || pinned[installer.Source[:at]] != installer.Source[at+1:] {
			t.Errorf("the quick start installs %s as %q, want the version install.ps1 pins: %v", agent, installer.Source, pinned)
		}
	}
}

// A skill comes from GitHub, so one whose install fails is tried once more,
// and one that fails again is left out and named in the install's one Note
// line while the install goes on. The stand-in npx fails gh-axi every time
// and chrome-devtools-axi the first time only.
func TestOneLineInstallTriesASkillAgainAndNamesOneThatStillFails(t *testing.T) {
	// Arrange
	folder := t.TempDir()
	calls := filepath.Join(folder, installtest.NpxCalls)
	tried := filepath.Join(folder, "tried")
	npx := "@echo %*>>\"" + calls + "\"\r\n" +
		"@if \"%6\"==\"gh-axi\" exit /b 1\r\n" +
		"@if not \"%6\"==\"chrome-devtools-axi\" exit /b 0\r\n" +
		"@if exist \"" + tried + "\" exit /b 0\r\n" +
		"@echo.>\"" + tried + "\"\r\n" +
		"@exit /b 1\r\n"

	// Act
	output, _, _ := runInstallWithLavishDownload(t, installScript(t), []byte("not the release the script pins"), map[string]string{"npx": npx})

	// Assert
	recorded, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("npx was never called: %v\n%s", err, output)
	}
	for skill, want := range map[string]int{"gh-axi": 2, "chrome-devtools-axi": 2, "no-mistakes": 1} {
		call := "-y skills@" + skillsCliPin(t) + " add kunchenguid/" + skill + " --skill " + skill + " -g -y -a claude-code -a codex -a pi --copy"
		if got := strings.Count(string(recorded), call); got != want {
			t.Errorf("npx was called %d times as %q, want %d:\n%s", got, call, want, recorded)
		}
	}
	note := installNote.FindString(output)
	if !strings.Contains(note, "gh-axi skill") || strings.Contains(note, "chrome-devtools-axi skill") || strings.Contains(note, "no-mistakes skill") {
		t.Errorf("the install's Note is %q, want it to name the gh-axi skill alone of the skills:\n%s", note, output)
	}
}
