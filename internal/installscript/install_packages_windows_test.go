package installscript

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fsx"
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

// An npx that installs a package holds a lock on its cache folder, which npm
// before 11.6.3 reports compromised (ECOMPROMISED) when the file system
// answers slowly, as it did on a CI runner on 2026-10-07 and again on
// 2026-10-14 with a fresh npm cache (issue #888). So the npm beside the npx
// installs the skills CLI into a folder of the install's own, npx runs it from
// that folder with --no, never installing a package itself, and the folder is
// removed once the skills are in. The stand-in npx fails with ECOMPROMISED
// whenever it would install the CLI itself.
func TestOneLineInstallAddsSkillsWithoutNpxInstallingAPackage(t *testing.T) {
	// Arrange
	folder := t.TempDir()
	calls := filepath.Join(folder, installtest.NpxCalls)
	cli := filepath.Join(folder, "npm-calls.txt")
	npm := "@echo %*>>\"" + cli + "\"\r\n" +
		"@if not \"%1 %2\"==\"install --prefix\" exit /b 0\r\n" +
		"@mkdir \"%~3\\node_modules\\.bin\"\r\n" +
		"@echo.>\"%~3\\node_modules\\.bin\\skills.cmd\"\r\n" +
		"@exit /b 0\r\n"
	npx := "@if not \"%1\"==\"--no\" (echo npm error code ECOMPROMISED& echo installs %6>>\"" + calls + "\"& exit /b 1)\r\n" +
		"@if not exist \"%CD%\\node_modules\\.bin\\skills.cmd\" (echo installs %6>>\"" + calls + "\"& exit /b 1)\r\n" +
		"@echo local %6 %CD%>>\"" + calls + "\"\r\n" +
		"@exit /b 0\r\n"

	// Act
	output, _, temp := runInstallWithLavishDownload(t, installScript(t), []byte("not the release the script pins"), map[string]string{"npm": npm, "npx": npx})

	// Assert
	fetched, err := os.ReadFile(cli)
	if err != nil {
		t.Fatalf("npm was never called: %v\n%s", err, output)
	}
	fetch := regexp.MustCompile(`(?m)^install --prefix (\S+) --no-save --no-audit --no-fund skills@` + regexp.QuoteMeta(skillsCliPin(t)) + `\s*$`).FindStringSubmatch(strings.ReplaceAll(string(fetched), "\r\n", "\n"))
	if fetch == nil {
		t.Fatalf("npm was called as %q, want it to install the pinned skills CLI into a folder of its own", fetched)
	}
	recorded, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("npx was never called: %v\n%s", err, output)
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(recorded), "\r\n", "\n")), "\n")
	want := []string{"gh-axi", "chrome-devtools-axi", "no-mistakes"}
	if len(lines) != len(want) {
		t.Fatalf("npx was called as %q, want each skill once from the skills CLI's folder", lines)
	}
	for index, skill := range want {
		fields := strings.SplitN(lines[index], " ", 3)
		// A runner's temporary folder can be spelt with 8.3 short names on
		// one side and long ones on the other.
		if len(fields) != 3 || fields[0] != "local" || fields[1] != skill || !strings.EqualFold(fsx.LongPath(fields[2]), fsx.LongPath(fetch[1])) {
			t.Errorf("call %d was %q, want %s run in %s", index+1, lines[index], skill, fetch[1])
		}
	}
	if !strings.HasPrefix(strings.ToLower(fsx.LongPath(fetch[1])), strings.ToLower(fsx.LongPath(temp))) {
		t.Errorf("the skills CLI went into %s, want a folder in the temporary folder %s", fetch[1], temp)
	}
	if _, err := os.Stat(fetch[1]); !os.IsNotExist(err) {
		t.Errorf("the skills CLI's folder %s outlived the install: %v", fetch[1], err)
	}
	if note := installNote.FindString(output); strings.Contains(note, "skill") {
		t.Errorf("the install's Note is %q, want every skill installed:\n%s", note, output)
	}
}

// The skills CLI comes from npm's registry, so a fetch of it that fails is
// tried once more, and one that fails again leaves every skill out, each
// named in the install's one Note line, with npx never run.
func TestOneLineInstallNamesEverySkillWhenTheSkillsCliCannotBeFetched(t *testing.T) {
	// Arrange
	folder := t.TempDir()
	calls := filepath.Join(folder, installtest.NpxCalls)
	cli := filepath.Join(folder, "npm-calls.txt")
	npm := "@echo %*>>\"" + cli + "\"\r\n" +
		"@if \"%1 %2\"==\"install --prefix\" exit /b 1\r\n" +
		"@exit /b 0\r\n"
	npx := "@echo %*>>\"" + calls + "\"\r\n@exit /b 0\r\n"

	// Act
	output, _, _ := runInstallWithLavishDownload(t, installScript(t), []byte("not the release the script pins"), map[string]string{"npm": npm, "npx": npx})

	// Assert
	fetched, err := os.ReadFile(cli)
	if err != nil {
		t.Fatalf("npm was never called: %v\n%s", err, output)
	}
	if got := strings.Count(string(fetched), "skills@"+skillsCliPin(t)); got != 2 {
		t.Errorf("npm fetched the skills CLI %d times, want 2:\n%s", got, fetched)
	}
	if recorded, err := os.ReadFile(calls); err == nil {
		t.Errorf("npx was called as %q, want it never run without the skills CLI", recorded)
	}
	note := installNote.FindString(output)
	for _, skill := range []string{"gh-axi", "chrome-devtools-axi", "no-mistakes"} {
		if !strings.Contains(note, skill+" skill") {
			t.Errorf("the install's Note is %q, want it to name the %s skill:\n%s", note, skill, output)
		}
	}
	if !strings.Contains(output, "skills CLI") || !strings.Contains(output, "npm install exited with code 1, so no skill is added") {
		t.Errorf("the install's log does not say the skills CLI could not be fetched:\n%s", output)
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
		call := "--no skills@" + skillsCliPin(t) + " add kunchenguid/" + skill + " --skill " + skill + " -g -y -a claude-code -a codex -a pi --copy"
		if got := strings.Count(string(recorded), call); got != want {
			t.Errorf("npx was called %d times as %q, want %d:\n%s", got, call, want, recorded)
		}
	}
	note := installNote.FindString(output)
	if !strings.Contains(note, "gh-axi skill") || strings.Contains(note, "chrome-devtools-axi skill") || strings.Contains(note, "no-mistakes skill") {
		t.Errorf("the install's Note is %q, want it to name the gh-axi skill alone of the skills:\n%s", note, output)
	}
}
