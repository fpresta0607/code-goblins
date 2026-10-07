package release

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The reader the update uses on this machine: Windows' own reading of a
// signed system program, and of a file nobody signed.
func TestWindowsSignatureReadsWhatWindowsReports(t *testing.T) {
	signed := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	unsigned := filepath.Join(t.TempDir(), "cfo.exe")
	if err := os.WriteFile(unsigned, []byte("MZ not a signed program"), 0o600); err != nil {
		t.Fatal(err)
	}

	status, signer, err := WindowsSignature(context.Background(), signed)
	if err != nil || status != "Valid" || signer == "" {
		t.Fatalf("WindowsSignature(%s) = %q, %q, %v; want Valid with its signer", signed, status, signer, err)
	}
	status, signer, err = WindowsSignature(context.Background(), unsigned)
	if err != nil || status == "Valid" || signer != "" {
		t.Fatalf("WindowsSignature(an unsigned file) = %q, %q, %v; want no valid signature and no signer", status, signer, err)
	}
}

// Started from PowerShell 7, as the go job's pwsh shell starts the tests and
// a pwsh terminal starts goblins update, Windows PowerShell inherits a
// PSModulePath that lists PowerShell 7's own modules first. Their
// Microsoft.PowerShell.Security cannot load in Windows PowerShell, so
// Get-AuthenticodeSignature never ran and the reader read an empty status as
// no signature at all (CI run 37565362308). The reader gives Windows
// PowerShell its own module path, as install.cmd does. powershell.exe is
// catalog-signed on this PC, which Get-AuthenticodeSignature reads as Valid.
func TestWindowsSignatureReadsThroughAPowerShell7ModulePath(t *testing.T) {
	// Arrange: PowerShell 7's Security module, which only PowerShell 7 can
	// load, first on the inherited module path.
	modules := t.TempDir()
	security := filepath.Join(modules, "Microsoft.PowerShell.Security")
	if err := os.MkdirAll(security, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "@{\r\n GUID = 'a94c8c7e-9810-47c0-b8af-65089c13a35a'\r\n ModuleVersion = '7.0.0.0'\r\n CompatiblePSEditions = @('Core')\r\n PowerShellVersion = '7.0'\r\n CmdletsToExport = @('Get-AuthenticodeSignature')\r\n}\r\n"
	if err := os.WriteFile(filepath.Join(security, "Microsoft.PowerShell.Security.psd1"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	system := os.Getenv("SystemRoot")
	t.Setenv("PSModulePath", modules+";"+filepath.Join(system, "System32", "WindowsPowerShell", "v1.0", "Modules"))
	signed := filepath.Join(system, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")

	// Act
	status, signer, err := WindowsSignature(context.Background(), signed)

	// Assert
	if err != nil || status != "Valid" || signer == "" {
		t.Fatalf("WindowsSignature(%s) under PowerShell 7's module path = %q, %q, %v; want Valid with its signer", signed, status, signer, err)
	}
}
