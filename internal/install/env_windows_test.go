//go:build windows

package install

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// testRegistryKey creates a scratch registry key for one test, removed when
// the test ends, so the round trip runs against real PowerShell and a real
// registry value without ever touching the live HKCU:\Environment.
func testRegistryKey(t *testing.T) string {
	t.Helper()
	key := fmt.Sprintf(`HKCU:\Software\cfo-install-test-%d`, os.Getpid())
	runRegistryScript(t, fmt.Sprintf(`New-Item -Path '%s' -Force | Out-Null`, key))
	t.Cleanup(func() {
		runRegistryScript(t, fmt.Sprintf(`Remove-Item -LiteralPath '%s' -Recurse -Force -ErrorAction SilentlyContinue`, key))
	})
	return key
}

func runRegistryScript(t *testing.T, script string) {
	t.Helper()
	result, err := execx.OSRunner{}.Run(context.Background(), execx.Request{
		Name: "powershell",
		Args: []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("script %q exited %d: %s", script, result.ExitCode, result.Stderr)
	}
}

// TestRegistryEnvStoreRoundTripsNonASCII proves a Set followed by a Get is
// byte-exact for a value full of non-ASCII characters, regardless of the
// console's OEM code page: exactly the corruption the no-setx design exists
// to prevent from ever reaching a rewritten PATH.
func TestRegistryEnvStoreRoundTripsNonASCII(t *testing.T) {
	store := registryEnvStore{Commands: execx.OSRunner{}, key: testRegistryKey(t)}

	value := `C:\Users\José\bin;C:\dev\日本語;C:\tools\Ünïcödé €`
	if err := store.Set("CfoTestPath", value); err != nil {
		t.Fatal(err)
	}
	got, set, err := store.Get("CfoTestPath")
	if err != nil {
		t.Fatal(err)
	}
	if !set {
		t.Fatal("set = false after Set")
	}
	if got != value {
		t.Errorf("Get = %q, want %q (byte-exact round trip)", got, value)
	}

	if _, set, err := store.Get("CfoTestAbsent"); err != nil || set {
		t.Errorf("Get(absent) = set %v, err %v; want unset with no error", set, err)
	}
}
