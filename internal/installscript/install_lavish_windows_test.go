package installscript

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/installtest"
)

// lavishFile is the release file of the lavish-axi fork the install installs.
const lavishFile = "lavish-axi-0.1.79-codegoblins.3.tgz"

// lavishPin finds the SHA256 the install script pins for that file.
var lavishPin = regexp.MustCompile(`Sha256 = "([0-9A-Fa-f]{64})"`)

// runInstallWithLavishDownload runs script as the one-line install with
// stand-ins for the internet and npm: the lavish-axi release file is answered
// with content, every other download from the internet with a line of text,
// and npm records how it was called. It returns the install's output, the
// record, and the session's temp folder.
func runInstallWithLavishDownload(t *testing.T, script string, content []byte) (output, recorded, temp string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	base := installtest.ServeRelease(t, binary, fmt.Sprintf("%x  cfo.exe\n", sha256.Sum256(binary)))
	folder := t.TempDir()
	record := filepath.Join(folder, "record.txt")
	served := filepath.Join(folder, "served.tgz")
	if err := os.WriteFile(served, content, 0o600); err != nil {
		t.Fatal(err)
	}
	stubs := map[string]string{
		"git":        "@exit /b 0\r\n",
		"gh":         "@exit /b 0\r\n",
		"npm":        "@echo npm %*>>\"" + record + "\"\r\n@exit /b 0\r\n",
		"powershell": "@exit /b 1\r\n",
	}
	internet := "function Invoke-WebRequest {\n" +
		"  [CmdletBinding()] param([string]$Uri, [string]$OutFile, [switch]$UseBasicParsing)\n" +
		"  if ($Uri.StartsWith('" + base + "/')) { Microsoft.PowerShell.Utility\\Invoke-WebRequest -Uri $Uri -OutFile $OutFile -UseBasicParsing; return }\n" +
		"  Add-Content -LiteralPath '" + record + "' -Value \"download $Uri\"\n" +
		"  if ($Uri -like '*/lavish-axi-*.tgz') { Copy-Item -LiteralPath '" + served + "' -Destination $OutFile; return }\n" +
		"  Set-Content -LiteralPath $OutFile -Value \"# installer from $Uri\"\n" +
		"}\n"
	cmd, _, temp := installtest.StrippedCommand(t, base, stubs, installtest.WindowsPowerShell(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command",
		internet+"Get-Content -Raw -LiteralPath '"+script+"' | Invoke-Expression")
	cmd.Env = append(cmd.Env, standInVariable+"=1")
	out, _ := cmd.CombinedOutput()
	written, _ := os.ReadFile(record)
	return string(out), string(written), temp
}

// npmCallsFor returns the recorded npm calls that name lavish-axi.
func npmCallsFor(recorded string) []string {
	var calls []string
	for _, line := range strings.Split(recorded, "\r\n") {
		if strings.HasPrefix(line, "npm ") && strings.Contains(line, "lavish-axi") {
			calls = append(calls, line)
		}
	}
	return calls
}

// lavish-axi comes from a release file of the fork, which npm installs
// without checking it. The install downloads the file itself and hands it to
// npm only when it matches the SHA256 pinned in the script. The comparison is
// the script's own, the same whichever PowerShell runs it, so Windows
// PowerShell alone checks it.
func TestOneLineInstallRefusesALavishDownloadThatDoesNotMatchItsPin(t *testing.T) {
	output, recorded, temp := runInstallWithLavishDownload(t, installScript(t), []byte("not the release the script pins"))

	if !strings.Contains(recorded, "/v0.1.79-codegoblins.3/"+lavishFile) {
		t.Fatalf("the lavish-axi release was not downloaded:\n%s\n%s", recorded, output)
	}
	if !strings.Contains(output, "does not match the SHA256 this install pins") {
		t.Errorf("the install does not say the download failed its pin:\n%s", output)
	}
	if calls := npmCallsFor(recorded); len(calls) != 0 {
		t.Errorf("npm was handed lavish-axi as %q, want nothing installed", calls)
	}
	if left, _ := filepath.Glob(filepath.Join(temp, "code-goblins-*")); len(left) != 0 {
		t.Errorf("the install left %v behind", left)
	}
}

// The check lets the pinned file through and hands npm the checked file
// itself, never the URL, so what npm installs is what was checked. The script
// here is the repository's own with its pin changed to the stand-in file's.
func TestOneLineInstallHandsNpmTheLavishDownloadThatMatchesItsPin(t *testing.T) {
	source, err := os.ReadFile(installScript(t))
	if err != nil {
		t.Fatal(err)
	}
	if pins := len(lavishPin.FindAll(source, -1)); pins != 1 {
		t.Fatalf("install.ps1 pins %d SHA256 values for a release file, want the one for lavish-axi", pins)
	}
	content := []byte("a stand-in for the pinned release file")
	script := filepath.Join(t.TempDir(), "install.ps1")
	pinned := lavishPin.ReplaceAll(source, []byte(fmt.Sprintf(`Sha256 = "%X"`, sha256.Sum256(content))))
	if err := os.WriteFile(script, pinned, 0o600); err != nil {
		t.Fatal(err)
	}

	output, recorded, temp := runInstallWithLavishDownload(t, script, content)

	if !strings.Contains(output, "Verified "+lavishFile+" against the SHA256 this install pins") || strings.Contains(output, "does not match the SHA256 this install pins") {
		t.Fatalf("the install did not verify the download:\n%s\n%s", output, recorded)
	}
	calls := npmCallsFor(recorded)
	if len(calls) != 1 {
		t.Fatalf("npm was called for lavish-axi %d times, want once:\n%s", len(calls), recorded)
	}
	handed := strings.Trim(strings.TrimSpace(calls[0]), `"`)
	if !strings.HasPrefix(handed, "npm install -g ") || strings.Contains(handed, "https://") || !strings.HasSuffix(handed, lavishFile) {
		t.Errorf("npm was called as %q, want it to install the checked file", calls[0])
	}
	if left, _ := filepath.Glob(filepath.Join(temp, "code-goblins-*")); len(left) != 0 {
		t.Errorf("the install left %v behind", left)
	}
}
