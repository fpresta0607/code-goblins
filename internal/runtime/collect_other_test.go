//go:build !windows

package runtime

import (
	"os"
	"testing"
)

// makeUnreadable takes every permission off path, so every read of it fails
// the way a record being rewritten under the collector does. It returns the
// function that gives them back.
func makeUnreadable(t *testing.T, path string) func() {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("lock %s: %v", path, err)
	}
	restore := func() { os.Chmod(path, info.Mode().Perm()) }
	if _, err := os.ReadFile(path); err == nil {
		restore()
		t.Fatalf("%s is still readable, so the test proves nothing", path)
	}
	return restore
}
