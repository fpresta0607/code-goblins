package auth

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// assertOwnerOnly checks the property that actually protects a secret on the
// running platform. Go's permission bits are not it on Windows: os.WriteFile
// maps only the read-only bit there, so every writable file reports 0666 and
// a mode assertion would pass on an ACL that grants Everyone full control.
func assertOwnerOnly(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s mode = %v, want no group or world access", path, info.Mode().Perm())
		}
		return
	}

	out, err := exec.Command("icacls", path).CombinedOutput()
	if err != nil {
		t.Fatalf("icacls %s: %v: %s", path, err, out)
	}
	// icacls prints one "principal:(rights)" grant per line after the path.
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		principal, _, found := strings.Cut(line, ":")
		if !found || principal == "" || strings.HasPrefix(line, "Successfully") {
			continue
		}
		principal = strings.TrimSpace(strings.TrimPrefix(principal, path))
		if principal == "" {
			continue
		}
		for _, broad := range []string{"Everyone", "Users", "Authenticated Users", "INTERACTIVE"} {
			if strings.Contains(principal, broad) {
				t.Errorf("%s grants %q access:\n%s", path, broad, out)
			}
		}
	}
}
