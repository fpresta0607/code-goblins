package spawn

import (
	"os"
	"path/filepath"
	"testing"
)

// A throwaway test, removed before this branch is ready. It fails the first
// time it runs on a runner and passes the second, in a job that runs only
// the tests one pattern leaves it, to prove on a real run that the second
// try reaches a test by its name there too.
func TestFailsOnceOnPurposeInASplitPackage(t *testing.T) {
	marker := filepath.Join(os.Getenv("RUNNER_TEMP"), "failed-once-on-purpose-split")
	if _, err := os.Stat(marker); err != nil {
		if err := os.WriteFile(marker, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Fatal("failing once on purpose, to prove the second try and its record")
	}
}
