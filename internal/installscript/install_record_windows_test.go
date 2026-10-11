package installscript

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/installtest"
	"github.com/fpresta0607/code-goblins/internal/standin"
)

// fetchedRecord is the record install.ps1 keeps of what it fetched, which the
// uninstall reads to leave or remove exactly that.
type fetchedRecord struct {
	Tools []struct {
		Name    string `json:"name"`
		Kind    string `json:"kind"`
		Package string `json:"package"`
	} `json:"tools"`
	Path   []string `json:"path"`
	Places []string `json:"places"`
}

// The install records each tool it installed with the package its own
// installer removes it by, and each of the tools' places that was not there
// before, never one that was: the uninstall never guesses. A second install
// adds to the record and names no tool twice.
func TestTheInstallRecordsWhatItFetchedAndNothingThatWasThere(t *testing.T) {
	// Arrange
	binary := standin.Bytes(t)
	base := installtest.ServeRelease(t, binary, fmt.Sprintf("%x  cfo.exe\n", sha256.Sum256(binary)))
	stubs := map[string]string{
		"git": "@exit /b 0\r\n",
		"gh":  "@exit /b 0\r\n",
		// Codex's install makes its own folder, as the real one does.
		"npm": "@if \"%3\"==\"@openai/codex@0.160.1\" mkdir \"%USERPROFILE%\\.codex\" 2>nul\r\n@exit /b 0\r\n",
	}
	internet := "function Invoke-WebRequest {\n" +
		"  [CmdletBinding()] param([string]$Uri, [string]$OutFile, [switch]$UseBasicParsing)\n" +
		"  if ($Uri.StartsWith('" + base + "/')) { Microsoft.PowerShell.Utility\\Invoke-WebRequest -Uri $Uri -OutFile $OutFile -UseBasicParsing; return }\n" +
		"  Set-Content -LiteralPath $OutFile -Value @(\"# installer from $Uri\", 'exit 1')\n" +
		"}\n"
	// Act
	cmd, local, temp := installtest.StrippedCommand(t, base, stubs, installtest.WindowsPowerShell(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command",
		internet+"Get-Content -Raw -LiteralPath '"+installScript(t)+"' | Invoke-Expression; exit $LASTEXITCODE")
	profile := strings.TrimPrefix(envOf(cmd.Env, "USERPROFILE"), "USERPROFILE=")
	if err := os.MkdirAll(filepath.Join(profile, ".pi"), 0o755); err != nil {
		t.Fatal(err)
	}
	first, record := run(t, cmd, local, temp)
	again := execx.Command(cmd.Path, cmd.Args[1:]...)
	again.Env, again.Dir = cmd.Env, cmd.Dir
	_, second := run(t, again, local, temp)

	// Assert
	packages := map[string]string{}
	for _, tool := range record.Tools {
		packages[tool.Name] = tool.Package
	}
	for name, want := range map[string]string{"codex": "@openai/codex", "pi": "@earendil-works/pi-coding-agent", "tasks-axi": "tasks-axi", "gh-axi": "gh-axi"} {
		if packages[name] != want {
			t.Errorf("the record names %s's package %q, want %q:\n%+v\n%s", name, packages[name], want, record, first)
		}
	}
	if _, ok := packages["claude"]; ok {
		t.Errorf("the record names Claude Code, whose installer failed: %+v", record)
	}
	if !slices.ContainsFunc(record.Places, func(place string) bool { return strings.EqualFold(place, filepath.Join(profile, ".codex")) }) {
		t.Errorf("the record's places are %v, want ~\\.codex, which the install made", record.Places)
	}
	if slices.ContainsFunc(record.Places, func(place string) bool { return strings.EqualFold(place, filepath.Join(profile, ".pi")) }) {
		t.Errorf("the record's places are %v, want no ~\\.pi, which was there before", record.Places)
	}
	names := map[string]int{}
	for _, tool := range second.Tools {
		names[tool.Name]++
	}
	for name, times := range names {
		if times != 1 {
			t.Errorf("a second install names %s %d times in the record", name, times)
		}
	}
	if len(second.Tools) < len(record.Tools) {
		t.Errorf("a second install dropped tools from the record: %+v, then %+v", record.Tools, second.Tools)
	}
}

// run runs a stripped install with a home's state folder in place, as cfo
// install makes it, and reads the record it left.
func run(t *testing.T, cmd *exec.Cmd, local, temp string) (string, fetchedRecord) {
	t.Helper()
	state := filepath.Join(local, "CodeGoblins", "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	printed, _ := cmd.CombinedOutput()
	said := installtest.Said(printed, temp)
	data, err := os.ReadFile(filepath.Join(state, "fetched.json"))
	if err != nil {
		t.Fatalf("the install wrote no record of what it fetched: %v\n%s", err, said)
	}
	var record fetchedRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("the record is not JSON: %v\n%s", err, data)
	}
	return said, record
}

func envOf(env []string, name string) string {
	for _, entry := range env {
		if strings.HasPrefix(strings.ToUpper(entry), strings.ToUpper(name)+"=") {
			return entry
		}
	}
	return ""
}
