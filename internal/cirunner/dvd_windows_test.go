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

// Windows' own tar reads the first boot's seed too, and finds both of its
// files in it.
func TestBuildSeedIsReadByWindowsTar(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "seed.iso")
	if err := BuildSeed(path); err != nil {
		t.Fatal(err)
	}
	userData, err := UserData()
	if err != nil {
		t.Fatal(err)
	}
	tar := filepath.Join(os.Getenv("SystemRoot"), "System32", "tar.exe")

	// Act
	readUserData, userErr := execx.CommandContext(context.Background(), tar, "-xOf", path, "USER-DATA").Output()
	readMetaData, metaErr := execx.CommandContext(context.Background(), tar, "-xOf", path, "META-DATA").Output()

	// Assert
	if userErr != nil || string(readUserData) != userData || metaErr != nil || string(readMetaData) != MetaData {
		t.Errorf("tar read user-data of %d bytes (%v) and meta-data %q (%v); want the VM's own", len(readUserData), userErr, readMetaData, metaErr)
	}
}
