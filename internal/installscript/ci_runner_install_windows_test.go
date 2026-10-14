package installscript

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/installtest"
)

// runCIRunnerInstall runs ci-runner-install.ps1 as its release pins it, by
// publisher, against the release files base serves, with args.
func runCIRunnerInstall(t *testing.T, base, publisher string, args string) (output, temp string, err error) {
	t.Helper()
	pin, err := filepath.Abs(filepath.Join("..", "..", "tools", "pin-installer.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	pinning := "-Source ci-runner-install.ps1 -Repository 'fpresta0607/code-goblins' -Tag 'v9.9.9'"
	if publisher != "" {
		pinning += " -Publisher '" + publisher + "'"
	}
	shell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	command := "$script = & '" + pin + "' " + pinning + "; & ([scriptblock]::Create($script)) " + args
	cmd, _, temp := installtest.StrippedCommand(t, base, nil, shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command)
	out, err := cmd.CombinedOutput()
	return string(out), temp, err
}

// A release's cg-ci-runner.exe that does not match its SHA256SUMS, or is not
// signed by the release's publisher, is refused before it is run or copied,
// and nothing downloaded is left.
func TestCIRunnerInstallScriptRefusesAProgramItCannotVerify(t *testing.T) {
	program := []byte("MZ a program no one signed")
	sum := sha256.Sum256(program)
	for name, row := range map[string]struct {
		sums string
		want string
	}{
		"a checksum that differs": {sums: strings.Repeat("0", 64) + "  cg-ci-runner.exe\n", want: "does not match the release's checksum, so nothing was installed"},
		"no checksum for it":      {sums: fmt.Sprintf("%x  cfo.exe\n", sum), want: "does not match the release's checksum, so nothing was installed"},
		"a program unsigned":      {sums: fmt.Sprintf("%x  cg-ci-runner.exe\n", sum), want: "is not signed by SIQstack LLC, so nothing was installed"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			base := serveFiles(t, map[string][]byte{"SHA256SUMS": []byte(row.sums), "cg-ci-runner.exe": program})

			// Act
			output, temp, err := runCIRunnerInstall(t, base, "SIQstack LLC", "-DryRun -AppId 1 -InstallationId 2 -Repo o/r -Key k -User u")

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

// A copy that names no publisher, as the repository's own does, installs
// nothing and downloads nothing, since the service holds a GitHub App's key.
func TestCIRunnerInstallScriptInstallsNothingUnsigned(t *testing.T) {
	// Arrange
	base := serveFiles(t, nil)

	// Act
	output, temp, err := runCIRunnerInstall(t, base, "", "-DryRun -AppId 1 -InstallationId 2 -Repo o/r -Key k -User u")

	// Assert
	if err == nil || !strings.Contains(output, "names no publisher, so it installs nothing") {
		t.Errorf("the script = %v with output:\n%s\nwant it to install nothing", err, output)
	}
	if left, _ := filepath.Glob(filepath.Join(temp, "code-goblins-ci-runner-*")); len(left) != 0 {
		t.Errorf("the script made a download folder %v; want nothing downloaded", left)
	}
}

// The script is plain ASCII, as a release download with no charset is read,
// and names each of the pin's three values exactly once.
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
	for _, pin := range []string{`$repo = "`, `releases/latest/download"`, `$releasePublisher = ""`} {
		if count := strings.Count(string(data), pin); count != 1 {
			t.Errorf("ci-runner-install.ps1 names %q %d times; want once, for the release to pin", pin, count)
		}
	}
}
