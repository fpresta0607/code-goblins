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
