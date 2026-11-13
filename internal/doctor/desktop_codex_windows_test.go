package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Codex desktop app is a packaged app: where it is installed, its
// bundled codex.exe is found by its package family, and elsewhere, as on CI,
// it is reported absent rather than as a failure. Whether it is installed
// for this user is read apart, from the folder Windows keeps its execution
// aliases in, so an answer of absent is checked too.
func TestDesktopCodexIsFoundByItsPackageOrReportedAbsent(t *testing.T) {
	_, aliasErr := os.Stat(filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "WindowsApps", codexPackageFamily))

	path, isInstalled, err := DesktopCodex()

	if err != nil {
		t.Fatal(err)
	}
	if isInstalled != (aliasErr == nil) {
		t.Fatalf("installed=%v, but the app's alias folder says %v", isInstalled, aliasErr == nil)
	}
	if !isInstalled {
		if path != "" {
			t.Fatalf("absent app named %q", path)
		}
		return
	}
	if !strings.EqualFold(filepath.Base(path), "codex.exe") {
		t.Fatalf("desktop codex = %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
