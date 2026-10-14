package cirunner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Windows' own tar, which is libarchive, reads the DVD as an ISO 9660 volume
// and finds the configuration in it: a reader written by others than the one
// that built it.
func TestBuildDVDIsReadByWindowsTar(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "job.iso")
	config := strings.Repeat("eyJ", 700)
	if err := BuildDVD(path, config); err != nil {
		t.Fatal(err)
	}
	tar := filepath.Join(os.Getenv("SystemRoot"), "System32", "tar.exe")

	// Act
	listing, listErr := execx.CommandContext(context.Background(), tar, "-tvvf", path).CombinedOutput()
	content, readErr := execx.CommandContext(context.Background(), tar, "-xOf", path, "JITCONFIG").Output()

	// Assert
	if listErr != nil || !strings.Contains(string(listing), "ISO9660") {
		t.Errorf("tar listed %q, %v; want an ISO9660 archive", listing, listErr)
	}
	if readErr != nil || string(content) != config {
		t.Errorf("tar read %d bytes, %v; want the %d-byte configuration", len(content), readErr, len(config))
	}
}
