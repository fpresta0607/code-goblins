package installscript

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/doctor"
)

// doctorFloor is the floor goblins doctor checks tool against.
func doctorFloor(t *testing.T, tool string) string {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GOBLINS_HOME", t.TempDir())
	for _, check := range doctor.Run() {
		if check.Name == tool && check.Floor != "" {
			return check.Floor
		}
	}
	t.Fatalf("goblins doctor sets no floor for %s", tool)
	return ""
}

// gh-axi before 0.1.52 passes a --body-file's contents to gh on the command
// line, which fails on Windows with spawn ENAMETOOLONG for a body over about
// 32 KB (#2115). An install that finds no gh-axi installs the release at the
// doctor's floor, and one that finds a gh-axi goblins doctor fails replaces it
// with that release, so rerunning the install fixes a machine installed
// before the floor. A gh-axi at the floor or later is kept. A replacement
// stays out of the install's record, as every tool the install found does.
func TestOneLineInstallPutsInGhAxiAtTheDoctorsFloor(t *testing.T) {
	const tool = "gh-axi"
	floor := doctorFloor(t, tool)
	for _, tc := range []struct {
		name       string
		version    string
		isPresent  bool
		isReplaced bool
	}{
		{name: "missing", isReplaced: true},
		{name: "the release install.ps1 pinned before the floor", version: "0.1.35", isPresent: true, isReplaced: true},
		{name: "the last release before the fix", version: "0.1.51", isPresent: true, isReplaced: true},
		{name: "a version that is not a release", version: "gh-axi dev", isPresent: true, isReplaced: true},
		{name: "a command that reports no version", isPresent: true, isReplaced: true},
		{name: "the release at the floor", version: "0.1.52", isPresent: true},
		{name: "a later release", version: "0.2.0", isPresent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			calls := filepath.Join(t.TempDir(), "npm.txt")
			standIns := map[string]string{
				"npm": "@echo npm %*>>\"" + calls + "\"\r\n" +
					"@if \"%3\"==\"" + tool + "@" + floor + "\" >\"%~dp0" + tool + ".cmd\" echo @echo " + floor + "\r\n" +
					"@exit /b 0\r\n",
			}
			present := "@exit /b 0\r\n"
			if tc.version != "" {
				present = "@echo " + tc.version + "\r\n" + present
			}
			if tc.isPresent {
				standIns[tool] = present
			}

			// Act
			run := runInstallWithForkDownload(t, []byte("a stand-in for a release file"), nil, standIns, "")

			// Assert
			recorded, _ := os.ReadFile(calls)
			npmCalls := npmCallsFor(string(recorded), tool)
			if tc.isPresent {
				if isPassed := doctorPasses(t, tool, present); isPassed == tc.isReplaced {
					t.Errorf("goblins doctor passing %s %q is %v, and the case replacing it is %v, want one of them", tool, tc.version, isPassed, tc.isReplaced)
				}
			}
			if !tc.isReplaced {
				if len(npmCalls) != 0 || !regexp.MustCompile(`ok\s+`+tool+`\s+present`).MatchString(run.output) {
					t.Errorf("the install did not keep %s %s: npm calls %q\n%s", tool, tc.version, npmCalls, run.output)
				}
				return
			}
			if want := "npm install -g " + tool + "@" + floor; len(npmCalls) != 1 || strings.TrimSpace(npmCalls[0]) != want {
				t.Fatalf("npm was called for %s as %q, want %q once:\n%s", tool, npmCalls, want, run.output)
			}
			if tc.isPresent && !regexp.MustCompile(`update\s+`+tool+`\s+`+tool+` `).MatchString(run.output) {
				t.Errorf("the install does not say it updates %s %q:\n%s", tool, tc.version, run.output)
			}
			if !regexp.MustCompile(`ok\s+` + tool + `\s+installed`).MatchString(run.output) {
				t.Errorf("the install does not say it installed %s:\n%s", tool, run.output)
			}
			if tc.isPresent {
				assertNotRecorded(t, run, tool)
			}
		})
	}
}

// npm replaces only the gh-axi in its own folder, so an older one in a folder
// ahead of that one goes on answering. The install then says so and names the
// copy that comes first, rather than report a release nothing runs.
func TestOneLineInstallPutsInGhAxiAtTheDoctorsFloorUnlessACopyComesFirstOnPath(t *testing.T) {
	// Arrange
	const tool = "gh-axi"
	floor := doctorFloor(t, tool)
	stale := map[string]string{tool: "@echo 0.1.35\r\n@exit /b 0\r\n"}

	// Act
	run := runInstallWithForkDownload(t, []byte("a stand-in for a release file"), nil, stale, "")

	// Assert
	if want, calls := "npm install -g "+tool+"@"+floor, npmCallsFor(run.recorded, tool); len(calls) != 1 || strings.TrimSpace(calls[0]) != want {
		t.Fatalf("npm was called for %s as %q, want %q once:\n%s", tool, calls, want, run.output)
	}
	if regexp.MustCompile(`ok\s+` + tool + `\s+installed`).MatchString(run.output) {
		t.Errorf("the install says it installed the release, which the command does not run:\n%s", run.output)
	}
	if !regexp.MustCompile(`WARN\s+` + tool + `\s+.*is 0\.1\.35, older than ` + regexp.QuoteMeta(floor) + `.*the copy in \S+ comes first on PATH`).MatchString(run.output) {
		t.Errorf("the install does not name the stale copy that comes first on PATH:\n%s", run.output)
	}
	if !regexp.MustCompile(`Note: These could not be installed[^\r\n]*\b` + tool + `[,.]`).MatchString(run.output) {
		t.Errorf("the closing note does not name %s:\n%s", tool, run.output)
	}
	assertNotRecorded(t, run, tool)
}
