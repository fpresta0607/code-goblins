package installscript

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/installtest"
)

// runnerPin is what a release pins in ci-runner-install.ps1: the server of
// its files, its publisher and its program's SHA-256.
type runnerPin struct{ base, publisher, programHash string }

// runCIRunnerInstall runs ci-runner-install.ps1 as pin pins it, in a session
// whose CODE_GOBLINS_RELEASE_BASE names environmentBase, with args.
func runCIRunnerInstall(t *testing.T, pin runnerPin, environmentBase, args string) (output, temp string, err error) {
	t.Helper()
	pinner, err := filepath.Abs(filepath.Join("..", "..", "tools", "pin-installer.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	pinning := "-Source ci-runner-install.ps1 -Repository 'fpresta0607/code-goblins' -Tag 'v9.9.9' -ReleaseBase '" + pin.base + "'"
	if pin.publisher != "" {
		pinning += " -Publisher '" + pin.publisher + "'"
	}
	if pin.programHash != "" {
		pinning += " -ProgramHash '" + pin.programHash + "'"
	}
	command := "$script = & '" + pinner + "' " + pinning + "; & ([scriptblock]::Create($script)) " + args
	cmd, _, temp := installtest.StrippedCommand(t, environmentBase, nil, installtest.WindowsPowerShell(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command)
	out, err := cmd.CombinedOutput()
	return string(out), temp, err
}

// countingServer serves data at every path and counts what it is asked.
func countingServer(t *testing.T, data []byte) (url string, asked *atomic.Int32) {
	t.Helper()
	asked = &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if data == nil {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(server.Close)
	return server.URL, asked
}

const runnerDryRun = "-DryRun -AppId 1 -InstallationId 2 -Repo o/r -Key k -User u"

// skipWhenElevated skips a dry run's test in an elevated session, as on
// GitHub's runner, where the script refuses a dry run before it downloads
// anything, as TestCIRunnerInstallScriptRefusesAnElevatedDryRun shows.
func skipWhenElevated(t *testing.T) {
	t.Helper()
	if windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("an elevated dry run is refused before it downloads anything")
	}
}

// An elevated dry run is refused before anything is downloaded: elevated,
// the program it checks in the user's own temp folder, which any program of
// the user's can change, would run as an administrator. Only an elevated
// session can ask for one, as GitHub's runner does.
func TestCIRunnerInstallScriptRefusesAnElevatedDryRun(t *testing.T) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("only an elevated session can ask for an elevated dry run")
	}
	// Arrange
	program := []byte("MZ a program no one signed")
	base, asked := countingServer(t, program)

	// Act
	output, _, err := runCIRunnerInstall(t, runnerPin{base, "SIQstack LLC", fmt.Sprintf("%x", sha256.Sum256(program))}, base, runnerDryRun)

	// Assert
	if err == nil || !strings.Contains(output, "needs no elevation") || asked.Load() != 0 {
		t.Errorf("the script = %v, having asked its release %d time(s):\n%s\nwant the dry run refused before anything is downloaded", err, asked.Load(), output)
	}
}

// A cg-ci-runner.exe whose SHA-256 is not the one the script pins, or that
// is not signed by the release's publisher, is refused before it is run or
// copied, and nothing downloaded is left.
func TestCIRunnerInstallScriptRefusesAProgramItCannotVerify(t *testing.T) {
	skipWhenElevated(t)
	program := []byte("MZ a program no one signed")
	for name, row := range map[string]struct {
		programHash string
		want        string
	}{
		"a program that is not the release's": {programHash: strings.Repeat("0", 64), want: "is not the release's program, so nothing was installed"},
		"a program unsigned":                  {programHash: fmt.Sprintf("%x", sha256.Sum256(program)), want: "is not signed by SIQstack LLC, so nothing was installed"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			base := serveFiles(t, map[string][]byte{"cg-ci-runner.exe": program})

			// Act
			output, temp, err := runCIRunnerInstall(t, runnerPin{base, "SIQstack LLC", row.programHash}, base, runnerDryRun)

			// Assert
			if err == nil || !strings.Contains(output, row.want) {
				t.Errorf("the script = %v with output:\n%s\nwant it refused as %q", err, output, row.want)
			}
			if strings.Contains(output, "step 1 of") {
				t.Errorf("the script ran the program it refused:\n%s", output)
			}
			if left, _ := filepath.Glob(filepath.Join(temp, "code-goblins-ci-runner-*")); len(left) != 0 {
				t.Errorf("the download folder %v was left behind", left)
			}
		})
	}
}

// The program comes from the release the script pins alone. A variable in
// the session that names another server is never read, since the elevated
// install would fetch whatever a program of the user's set it to.
func TestCIRunnerInstallScriptDownloadsOnlyFromItsRelease(t *testing.T) {
	skipWhenElevated(t)
	// Arrange
	program := []byte("MZ a program no one signed")
	release, releaseAsked := countingServer(t, nil)
	other, otherAsked := countingServer(t, program)

	// Act
	output, _, err := runCIRunnerInstall(t, runnerPin{release, "SIQstack LLC", fmt.Sprintf("%x", sha256.Sum256(program))}, other, runnerDryRun)

	// Assert
	if releaseAsked.Load() == 0 {
		t.Errorf("the script asked its release nothing:\n%s", output)
	}
	if err == nil || otherAsked.Load() != 0 {
		t.Errorf("the script = %v and asked the session's server %d time(s):\n%s\nwant it refused and that server never asked", err, otherAsked.Load(), output)
	}
}

// A copy that names no publisher, as the repository's own does, installs
// nothing and downloads nothing, since the service holds a GitHub App's key.
func TestCIRunnerInstallScriptInstallsNothingUnsigned(t *testing.T) {
	skipWhenElevated(t)
	// Arrange
	base, asked := countingServer(t, nil)

	// Act
	output, temp, err := runCIRunnerInstall(t, runnerPin{base: base}, base, runnerDryRun)

	// Assert
	if err == nil || !strings.Contains(output, "names no publisher, so it installs nothing") {
		t.Errorf("the script = %v with output:\n%s\nwant it to install nothing", err, output)
	}
	if left, _ := filepath.Glob(filepath.Join(temp, "code-goblins-ci-runner-*")); len(left) != 0 || asked.Load() != 0 {
		t.Errorf("the script made %v and asked the release %d time(s); want nothing downloaded", left, asked.Load())
	}
}

// The pin writes no runner install script it cannot pin as given, and pins a
// valid one to the release's own files.
func TestThePinWritesTheRunnerScriptOnlyAsGiven(t *testing.T) {
	pinner, err := filepath.Abs(filepath.Join("..", "..", "tools", "pin-installer.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("a", 64)
	for name, row := range map[string]struct {
		args      []string
		isRefused bool
	}{
		"a program hash that is not a SHA-256":   {args: []string{"-Source", "ci-runner-install.ps1", "-ProgramHash", "abc"}, isRefused: true},
		"a release base PowerShell would expand": {args: []string{"-Source", "ci-runner-install.ps1", "-ProgramHash", hash, "-ReleaseBase", "http://127.0.0.1/$env:USERNAME"}, isRefused: true},
		"a program hash for the fleet's install": {args: []string{"-Source", "install.ps1", "-ProgramHash", hash}, isRefused: true},
		"the release's own":                      {args: []string{"-Source", "ci-runner-install.ps1", "-ProgramHash", hash}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			args := append([]string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", pinner, "-Repository", "fpresta0607/code-goblins", "-Tag", "v1.2.3", "-Publisher", "Code Goblins Test Publisher"}, row.args...)

			// Act
			out, err := exec.Command(installtest.WindowsPowerShell(), args...).CombinedOutput()

			// Assert
			isPinned := strings.Contains(string(out), `$releaseProgramHash = "`+hash+`"`) && strings.Contains(string(out), `$releaseBase = "https://github.com/fpresta0607/code-goblins/releases/download/v1.2.3"`)
			if row.isRefused && (err == nil || isPinned) {
				t.Errorf("pin-installer.ps1 = %v, accepting %q:\n%s", err, row.args, out)
			}
			if !row.isRefused && (err != nil || !isPinned) {
				t.Errorf("pin-installer.ps1 = %v, want the script pinned to the release:\n%s", err, out)
			}
		})
	}
}

// The release pins the runner's install to the program's SHA-256 once the
// program is signed, since signing changes its bytes: a hash taken before
// would refuse the program the release publishes.
func TestTheReleasePinsTheRunnerProgramItSigned(t *testing.T) {
	// Arrange
	pin, _ := releaseStep(t, "Pin the installer to this release")
	_, afterSign := releaseStep(t, "Sign")

	// Assert
	if !strings.Contains(pin, "$pin.ProgramHash = (Get-FileHash cg-ci-runner.exe -Algorithm SHA256).Hash") {
		t.Errorf("the pin does not take the runner program's SHA-256:\n%s", pin)
	}
	if !strings.Contains(afterSign, "      - name: Pin the installer to this release\n") {
		t.Error("the installer is pinned before the programs are signed")
	}
}

// No variable the script sets is one of its parameters, which PowerShell
// names whatever their case: the script's own $repo once replaced -Repo, so
// every install was for the release's repository, never the one it was given.
func TestCIRunnerInstallScriptSetsNoneOfItsParameters(t *testing.T) {
	// Arrange
	script, err := filepath.Abs(filepath.Join("..", "..", "ci-runner-install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	find := `$errors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile('` + script + `', [ref]$null, [ref]$errors)
if ($errors) { throw "the script does not parse: $errors" }
$parameters = @($ast.ParamBlock.Parameters | ForEach-Object { $_.Name.VariablePath.UserPath })
"parameters: " + ($parameters -join ",")
$ast.FindAll({ param($node) $node -is [Management.Automation.Language.AssignmentStatementAst] -or $node -is [Management.Automation.Language.ForEachStatementAst] }, $true) | ForEach-Object {
    $target = if ($_ -is [Management.Automation.Language.ForEachStatementAst]) { $_.Variable } else { $_.Left }
    if ($target -is [Management.Automation.Language.ConvertExpressionAst]) { $target = $target.Child }
    if ($target -is [Management.Automation.Language.VariableExpressionAst] -and $parameters -contains $target.VariablePath.UserPath) {
        "line $($_.Extent.StartLineNumber) sets " + $target.VariablePath.UserPath
    }
}`

	// Act
	out, err := exec.Command(installtest.WindowsPowerShell(), "-NoProfile", "-NonInteractive", "-Command", find).CombinedOutput()

	// Assert
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if err != nil || !strings.Contains(lines[0], ",Repo,") {
		t.Fatalf("reading the script's parameters = %v:\n%s", err, out)
	}
	if len(lines) > 1 {
		t.Errorf("ci-runner-install.ps1 sets its own parameters:\n%s", strings.Join(lines[1:], "\n"))
	}
}

// The script is plain ASCII, as a release download with no charset is read,
// and names each of the pin's four values exactly once.
func TestCIRunnerInstallScriptIsPlainASCII(t *testing.T) {
	// Act
	data, err := os.ReadFile(filepath.Join("..", "..", "ci-runner-install.ps1"))

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range data {
		if b > 0x7F {
			t.Fatalf("ci-runner-install.ps1 holds the byte %#x, outside ASCII", b)
		}
	}
	for _, pin := range []string{`$releaseRepository = "`, `$releaseBase = "`, `$releasePublisher = ""`, `$releaseProgramHash = ""`} {
		if count := strings.Count(string(data), pin); count != 1 {
			t.Errorf("ci-runner-install.ps1 names %q %d times; want once, for the release to pin", pin, count)
		}
	}
}
