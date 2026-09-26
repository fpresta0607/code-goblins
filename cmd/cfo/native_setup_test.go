package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// npm installs pi and codex on Windows as PowerShell script shims, and a
// machine whose execution policy is Restricted refuses to run a script. cfo
// hooks check still reads the harness's version through such a shim: its
// probe runs with the policy bypassed, as every other script cfo runs does.
func TestHooksCheckReadsAScriptShimUnderARestrictedPolicy(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "pi.ps1"), []byte("Write-Output '0.85.99'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PSExecutionPolicyPreference", "Restricted")
	// The premise: this policy refuses the shim when nothing bypasses it.
	if out, _ := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "& pi --version; exit $LASTEXITCODE").CombinedOutput(); strings.Contains(string(out), "0.85.99") || !strings.Contains(string(out), "UnauthorizedAccess") {
		t.Fatalf("the Restricted policy did not refuse the shim: %s", out)
	}
	var stdout, stderr bytes.Buffer

	code := runNativeSetup([]string{"check", "pi"}, &stdout, &stderr, commandRuntime{})

	if code != 0 || !strings.Contains(stdout.String(), "pi native contract verified (0.85.99)") {
		t.Fatalf("cfo hooks check pi = %d\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
}
