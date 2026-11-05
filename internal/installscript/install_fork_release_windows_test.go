package installscript

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/doctor"
	"github.com/fpresta0607/code-goblins/internal/installtest"
	"github.com/fpresta0607/code-goblins/internal/standin"
)

// forkReleases are the release files of the forks the install installs a
// tool from, by tool, as the doctor's hints name them.
var forkReleases = map[string]string{
	"chrome-devtools-axi": doctor.ChromeDevtoolsRelease,
	"lavish-axi":          doctor.LavishRelease,
}

// reviewedDigests are the SHA256 digests of those releases' files, as their
// release notes give them.
var reviewedDigests = map[string]string{
	"chrome-devtools-axi": "382d38195d120a02b5f86631e6e31f2a1b28d09814ab6c628c5c8f7fbd1f8247",
	"lavish-axi":          "afeada08e260f0ef38792d28b65b8a5d79b200c9c89f9bcd916837345679e714",
}

// releasePin finds the SHA256 install.ps1 pins for tool's release file.
func releasePin(tool string) *regexp.Regexp {
	return regexp.MustCompile(`Name = "` + regexp.QuoteMeta(tool) + `";[^\r\n]*Sha256 = "([0-9A-Fa-f]{64})"`)
}

// forkInstall is what a one-line install with a stand-in internet left: what
// it said, each download and npm call in the order made, and the session's
// temp and local application data folders.
type forkInstall struct {
	output   string
	recorded string
	temp     string
	local    string
}

// runInstallWithForkDownload runs the repository's install script as the
// one-line install with stand-ins for the internet and npm: every release
// file of a fork is answered with content, every other download from the
// internet with a line of text, and npm records how it was called. The pin of
// each tool in matching is changed to content's SHA256 in the text the
// install runs, so no changed copy of the script is written. A stand-in in
// standIns replaces the one of its name. Handed the release file of the tool
// npmReplaces, npm also puts that release's build in place of the tool's
// command, as a real install does when the command is npm's own. The home's
// state folder is in place, as cfo install makes it, so the install keeps its
// record.
func runInstallWithForkDownload(t *testing.T, content []byte, matching []string, standIns map[string]string, npmReplaces string) forkInstall {
	t.Helper()
	source, err := os.ReadFile(installScript(t))
	if err != nil {
		t.Fatal(err)
	}
	pins := ""
	for _, tool := range matching {
		pin := releasePin(tool).FindSubmatch(source)
		if pin == nil {
			t.Fatalf("install.ps1 pins no SHA256 for %s", tool)
		}
		pins += fmt.Sprintf(".Replace('%s', '%X')", pin[1], sha256.Sum256(content))
	}
	binary := standin.Bytes(t)
	base := installtest.ServeRelease(t, binary, fmt.Sprintf("%x  cfo.exe\n", sha256.Sum256(binary)))
	folder := t.TempDir()
	record := filepath.Join(folder, "record.txt")
	served := filepath.Join(folder, "served.tgz")
	if err := os.WriteFile(served, content, 0o600); err != nil {
		t.Fatal(err)
	}
	npm := "@echo npm %*>>\"" + record + "\"\r\n"
	if npmReplaces != "" {
		file := path.Base(forkReleases[npmReplaces])
		build := strings.TrimSuffix(strings.TrimPrefix(file, npmReplaces+"-"), ".tgz")
		npm += "@if \"%~nx3\"==\"" + file + "\" >\"%~dp0" + npmReplaces + ".cmd\" echo @echo " + build + "\r\n"
	}
	stubs := map[string]string{
		"git": "@exit /b 0\r\n",
		"gh":  "@exit /b 0\r\n",
		"npm": npm + "@exit /b 0\r\n",
	}
	maps.Copy(stubs, standIns)
	internet := "function Invoke-WebRequest {\n" +
		"  [CmdletBinding()] param([string]$Uri, [string]$OutFile, [switch]$UseBasicParsing)\n" +
		"  if ($Uri.StartsWith('" + base + "/')) { Microsoft.PowerShell.Utility\\Invoke-WebRequest -Uri $Uri -OutFile $OutFile -UseBasicParsing; return }\n" +
		"  Add-Content -LiteralPath '" + record + "' -Value \"download $Uri\"\n" +
		"  if ($Uri -like 'https://github.com/fpresta0607/*/releases/download/*.tgz') { Copy-Item -LiteralPath '" + served + "' -Destination $OutFile; return }\n" +
		"  Set-Content -LiteralPath $OutFile -Value @(\"# installer from $Uri\", 'exit 1')\n" +
		"}\n"
	cmd, local, temp := installtest.StrippedCommand(t, base, stubs, installtest.WindowsPowerShell(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command",
		internet+"(Get-Content -Raw -LiteralPath '"+installScript(t)+"')"+pins+" | Invoke-Expression; exit $LASTEXITCODE")
	if err := os.MkdirAll(filepath.Join(local, "CodeGoblins", "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, _ := cmd.CombinedOutput()
	written, _ := os.ReadFile(record)
	return forkInstall{output: installtest.Said(out, temp), recorded: string(written), temp: temp, local: local}
}

// npmCallsFor returns the recorded npm calls that name tool.
func npmCallsFor(recorded, tool string) []string {
	var calls []string
	for _, line := range strings.Split(recorded, "\r\n") {
		if strings.HasPrefix(line, "npm ") && strings.Contains(line, tool) {
			calls = append(calls, line)
		}
	}
	return calls
}

// assertNpmGotTheCheckedFile fails unless npm was called once for tool, with
// the downloaded file itself, never its URL, so what npm installs is what
// was checked.
func assertNpmGotTheCheckedFile(t *testing.T, run forkInstall, tool string) {
	t.Helper()
	calls := npmCallsFor(run.recorded, tool)
	if len(calls) != 1 {
		t.Fatalf("npm was called for %s %d times, want once:\n%s\n%s", tool, len(calls), run.recorded, run.output)
	}
	handed := strings.Trim(strings.TrimSpace(calls[0]), `"`)
	if !strings.HasPrefix(handed, "npm install -g ") || strings.Contains(handed, "https://") || !strings.HasSuffix(handed, path.Base(forkReleases[tool])) {
		t.Errorf("npm was called as %q, want it to install the checked file", calls[0])
	}
}

// A tool from a fork comes from a release file, which npm installs without
// checking it. The install downloads the files the doctor's hints name, and
// no other, itself and hands each to npm only when it matches the SHA256
// pinned in the script. The comparison is the script's own, the same
// whichever PowerShell runs it, so Windows PowerShell alone checks it.
func TestOneLineInstallRefusesAForkReleaseThatDoesNotMatchItsPin(t *testing.T) {
	// Act
	run := runInstallWithForkDownload(t, []byte("not the release the script pins"), nil, nil, "")

	// Assert
	var downloaded []string
	for _, line := range strings.Split(run.recorded, "\r\n") {
		if release, isDownload := strings.CutPrefix(line, "download "); isDownload && strings.HasSuffix(release, ".tgz") {
			downloaded = append(downloaded, release)
		}
	}
	slices.Sort(downloaded)
	if want := slices.Sorted(maps.Values(forkReleases)); !slices.Equal(downloaded, want) {
		t.Errorf("the install downloaded the release files %v, want the ones the doctor names, %v:\n%s\n%s", downloaded, want, run.recorded, run.output)
	}
	for tool, release := range forkReleases {
		if !strings.Contains(run.output, "The downloaded "+path.Base(release)+" does not match the SHA256 this install pins") {
			t.Errorf("the install does not say the download of %s failed its pin:\n%s", tool, run.output)
		}
		if calls := npmCallsFor(run.recorded, tool); len(calls) != 0 {
			t.Errorf("npm was handed %s as %q, want nothing installed", tool, calls)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(run.temp, "code-goblins-*")); len(left) != 0 {
		t.Errorf("the install left %v behind", left)
	}
	// A refused download is a warning: the install goes on and adds each skill
	// the fleet's tools publish, through the stand-in npx.
	skills, err := os.ReadFile(filepath.Join(run.temp, installtest.NpxCalls))
	if err != nil {
		t.Fatalf("npx was never called: %v\n%s", err, run.output)
	}
	for _, skill := range []string{"gh-axi", "chrome-devtools-axi", "no-mistakes"} {
		want := fmt.Sprintf("--no skills@%s add kunchenguid/%s --skill %s -g -y -a claude-code -a codex -a pi --copy", skillsCliPin(t), skill, skill)
		if strings.Count(string(skills), want) != 1 {
			t.Errorf("npx calls %q, want %q once", skills, want)
		}
	}
}

// The script pins each fork's reviewed release, and the check lets the
// pinned file through and hands npm the checked file itself. The text the
// install runs here is the repository's script with the tool's pin changed to
// the stand-in file's.
func TestOneLineInstallHandsNpmTheForkReleaseThatMatchesItsPin(t *testing.T) {
	source, err := os.ReadFile(installScript(t))
	if err != nil {
		t.Fatal(err)
	}
	for tool := range forkReleases {
		t.Run(tool, func(t *testing.T) {
			// Arrange
			pin := releasePin(tool).FindSubmatch(source)
			if pin == nil || !strings.EqualFold(string(pin[1]), reviewedDigests[tool]) {
				t.Fatalf("install.ps1 pins %s at %q, want the reviewed release's digest %s", tool, pin, reviewedDigests[tool])
			}

			// Act
			run := runInstallWithForkDownload(t, []byte("a stand-in for the pinned release file"), []string{tool}, nil, "")

			// Assert
			file := path.Base(forkReleases[tool])
			if !strings.Contains(run.output, "Verified "+file+" against the SHA256 this install pins") || strings.Contains(run.output, "The downloaded "+file+" does not match") {
				t.Fatalf("the install did not verify the download:\n%s\n%s", run.output, run.recorded)
			}
			assertNpmGotTheCheckedFile(t, run, tool)
			if left, _ := filepath.Glob(filepath.Join(run.temp, "code-goblins-*")); len(left) != 0 {
				t.Errorf("the install left %v behind", left)
			}
		})
	}
}

// doctorPasses says whether cfo doctor's check passes tool when its command is
// a script with the text present. The PATH it sets holds for the rest of the
// test, so a test calls it after its install.
func doctorPasses(t *testing.T, tool, present string) bool {
	t.Helper()
	folder := t.TempDir()
	if err := os.WriteFile(filepath.Join(folder, tool+".cmd"), []byte(present), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", folder)
	t.Setenv("CFO_HOME", t.TempDir())
	for _, check := range doctor.Run() {
		if check.Name == tool {
			return check.Err == ""
		}
	}
	t.Fatalf("cfo doctor does not check %s", tool)
	return false
}

// assertNotRecorded fails unless the install's record leaves out tool, which
// was there before the install.
func assertNotRecorded(t *testing.T, run forkInstall, tool string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(run.local, "CodeGoblins", "state", "fetched.json"))
	if err != nil {
		t.Fatalf("the install kept no record: %v\n%s", err, run.output)
	}
	var record fetchedRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	for _, fetched := range record.Tools {
		if fetched.Name == tool {
			t.Errorf("the record names %s, which was there before the install: %s", tool, data)
		}
	}
}

// An install that finds a fork's tool already there keeps it only when it is
// the release's build or a later one of the fork's, the builds cfo doctor
// passes, so each case here also asks the doctor. The upstream package, a
// build of the fork's older than the release, such as one installed by hand,
// and one that reports no version or does not run are replaced with the
// release file, so a fix made in the fork reaches every machine the next
// install runs on (#1626). A replacement stays out of the install's record,
// so the uninstall leaves the command in place, as it leaves every tool the
// install found.
func TestOneLineInstallReplacesAForkToolThatIsNotItsReleaseOrLater(t *testing.T) {
	for _, tc := range []struct {
		name       string
		tool       string
		version    string
		exitCode   int
		isReplaced bool
	}{
		{name: "the upstream release install.ps1 pinned", tool: "chrome-devtools-axi", version: "0.1.39", isReplaced: true},
		{name: "an upstream release above the fork's", tool: "chrome-devtools-axi", version: "0.1.45", isReplaced: true},
		{name: "the fork's build installed by hand", tool: "chrome-devtools-axi", version: "0.1.40-codegoblins.2", isReplaced: true},
		{name: "a build that reports no version", tool: "chrome-devtools-axi", version: "", isReplaced: true},
		{name: "a command that prints the release's build and fails", tool: "chrome-devtools-axi", version: "0.1.40-codegoblins.3", exitCode: 1, isReplaced: true},
		{name: "the release's version with no build number", tool: "chrome-devtools-axi", version: "0.1.40-codegoblins", isReplaced: true},
		{name: "the release's build", tool: "chrome-devtools-axi", version: "0.1.40-codegoblins.3"},
		{name: "a later build of the fork's", tool: "chrome-devtools-axi", version: "0.1.40-codegoblins.4"},
		{name: "a later release of the fork's with no build number", tool: "chrome-devtools-axi", version: "0.1.41-codegoblins"},
		{name: "a Scrawl build before the release", tool: "lavish-axi", version: "0.1.79-codegoblins.17", isReplaced: true},
		{name: "the Scrawl release's build", tool: "lavish-axi", version: "0.1.79-codegoblins.19"},
	} {
		t.Run(tc.tool+" "+tc.name, func(t *testing.T) {
			// Arrange
			present := fmt.Sprintf("@exit /b %d\r\n", tc.exitCode)
			if tc.version != "" {
				present = "@echo " + tc.version + "\r\n" + present
			}

			// Act
			run := runInstallWithForkDownload(t, []byte("a stand-in for the pinned release file"), []string{tc.tool}, map[string]string{tc.tool: present}, tc.tool)

			// Assert
			if isPassed := doctorPasses(t, tc.tool, present); isPassed == tc.isReplaced {
				t.Errorf("cfo doctor passing %s %q is %v, and the case replacing it is %v, want one of them", tc.tool, tc.version, isPassed, tc.isReplaced)
			}
			downloaded := strings.Contains(run.recorded, "download "+forkReleases[tc.tool])
			if !tc.isReplaced {
				if downloaded || len(npmCallsFor(run.recorded, tc.tool)) != 0 || !regexp.MustCompile(`ok\s+`+regexp.QuoteMeta(tc.tool)+`\s+present`).MatchString(run.output) {
					t.Errorf("the install did not keep %s %s:\n%s\n%s", tc.tool, tc.version, run.recorded, run.output)
				}
				return
			}
			if !downloaded || !regexp.MustCompile(`update\s+`+regexp.QuoteMeta(tc.tool)+`\s+`+regexp.QuoteMeta(tc.tool)+` `).MatchString(run.output) {
				t.Fatalf("the install kept %s %q rather than replace it with its release:\n%s\n%s", tc.tool, tc.version, run.recorded, run.output)
			}
			assertNpmGotTheCheckedFile(t, run, tc.tool)
			if !regexp.MustCompile(`ok\s+` + regexp.QuoteMeta(tc.tool) + `\s+installed`).MatchString(run.output) {
				t.Errorf("the install does not say it installed %s:\n%s", tc.tool, run.output)
			}
			assertNotRecorded(t, run, tc.tool)
		})
	}
}

// npm replaces only the copy in its own folder, so a stale command in a folder
// ahead of that one goes on answering. The install then says so and names the
// copy that comes first, rather than report a release nothing runs: the fix
// cfo doctor names, npm install -g, reaches the same folder and cannot clear it.
func TestOneLineInstallReplacesAForkToolThatIsNotItsReleaseOrLaterUnlessACopyComesFirstOnPath(t *testing.T) {
	// Arrange
	const tool = "chrome-devtools-axi"
	stale := map[string]string{tool: "@echo 0.1.39\r\n@exit /b 0\r\n"}

	// Act
	run := runInstallWithForkDownload(t, []byte("a stand-in for the pinned release file"), []string{tool}, stale, "")

	// Assert
	assertNpmGotTheCheckedFile(t, run, tool)
	if regexp.MustCompile(`ok\s+` + tool + `\s+installed`).MatchString(run.output) {
		t.Errorf("the install says it installed the release, which the command does not run:\n%s", run.output)
	}
	if !regexp.MustCompile(`WARN\s+` + tool + `\s+.*is 0\.1\.39,.*the copy in \S+ comes first on PATH`).MatchString(run.output) {
		t.Errorf("the install does not name the stale copy that comes first on PATH:\n%s", run.output)
	}
	if !regexp.MustCompile(`Note: These could not be installed[^\r\n]*` + tool + `[,.]`).MatchString(run.output) {
		t.Errorf("the closing note does not name %s:\n%s", tool, run.output)
	}
	assertNotRecorded(t, run, tool)
}
