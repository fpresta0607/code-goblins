package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A throwaway test, removed before this branch is ready. It fails the first
// time it runs on a runner and passes the second, to prove on a real run
// that a test which fails once leaves its job green and is named on the test
// check.
func TestFailsOnceOnPurpose(t *testing.T) {
	marker := filepath.Join(os.Getenv("RUNNER_TEMP"), "failed-once-on-purpose-rest")
	if _, err := os.Stat(marker); err != nil {
		if err := os.WriteFile(marker, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Fatal("failing once on purpose, to prove the second try and its record")
	}
}
