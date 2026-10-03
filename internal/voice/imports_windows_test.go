package voice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Windows' own curl.exe links Winsock; this test binary, a Go program, links
// kernel32.dll alone.
func TestAProgramThatLinksANetworkingLibraryIsRefused(t *testing.T) {
	curl := filepath.Join(os.Getenv("SystemRoot"), "System32", "curl.exe")
	program, err := os.ReadFile(curl)
	if err != nil {
		t.Fatalf("Windows' curl.exe, which this test reads as a program that links Winsock: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "engine.exe"), program, 0o700); err != nil {
		t.Fatal(err)
	}
	err = offline(dir)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "ws2_32.dll") || !strings.Contains(err.Error(), "engine.exe") {
		t.Fatalf("a program that links Winsock answered %v", err)
	}
}

func TestAProgramThatLinksNoNetworkingLibraryIsAccepted(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	program, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, name := range []string{"engine.exe", "library.dll"} {
		if err := os.WriteFile(filepath.Join(dir, name), program, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// A file that is not a program is not read as one.
	if err := os.WriteFile(filepath.Join(dir, "tokens.txt"), []byte("a b c\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	libraries, err := imports(filepath.Join(dir, "engine.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if len(libraries) == 0 {
		t.Fatal("no import of this test binary was read, so the check would accept anything")
	}
	if err := offline(dir); err != nil {
		t.Fatalf("a program that links %v was refused: %v", libraries, err)
	}
}
