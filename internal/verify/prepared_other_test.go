//go:build !windows

package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreparedGenerationRejectsSymlinkFiles(t *testing.T) {
	for _, name := range []string{PreparedEvidenceFile, "runtime/v8_context_snapshot.bin"} {
		t.Run(name, func(t *testing.T) {
			root, inventory := preparedFixture(t)
			path := filepath.Join(root, filepath.FromSlash(name))
			target := path + ".original"
			if err := os.Rename(path, target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}

			err := ValidatePreparedGeneration(root, inventory)

			if err == nil || !strings.Contains(err.Error(), "symlink") {
				t.Fatalf("symlink passed: %v", err)
			}
		})
	}
}
