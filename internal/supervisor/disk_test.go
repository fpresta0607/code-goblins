package supervisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// A home whose heavy folders moved to a Dev Drive starts goblins on that
// drive, so a Dev Drive that cannot be read, such as one whose disk is not
// attached, is no room to start: the reading fails rather than falling back to
// the home's drive alone.
func TestMachineDiskReadsTheDevDriveAndFailsWhenItIsMissing(t *testing.T) {
	// Arrange
	root := t.TempDir()
	attached := filepath.Join(t.TempDir(), "CodeGoblins")
	if err := os.MkdirAll(attached, 0o755); err != nil {
		t.Fatal(err)
	}

	// Act
	reading, err := MachineDisk(home.Home{Root: root, DevDrive: attached})
	_, missingErr := MachineDisk(home.Home{Root: root, DevDrive: filepath.Join(root, "detached", "CodeGoblins")})

	// Assert
	if err != nil || reading.Total == 0 || reading.Floor == 0 {
		t.Errorf("MachineDisk with a Dev Drive = %+v, %v; want a reading under the floor setting", reading, err)
	}
	if missingErr == nil {
		t.Error("a Dev Drive folder that cannot be read gave a reading, so a start would land on a drive that is not there")
	}
}
