package janitor

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// denyListing keeps this process, and every other, from listing dir until
// the test ends, as a folder another account owns can.
func denyListing(t *testing.T, dir string) {
	t.Helper()
	if out, err := exec.Command("icacls", dir, "/deny", "*S-1-1-0:(RD)").CombinedOutput(); err != nil {
		t.Fatalf("icacls /deny: %v: %s", err, out)
	}
	t.Cleanup(func() {
		if out, err := exec.Command("icacls", dir, "/remove:d", "*S-1-1-0").CombinedOutput(); err != nil {
			t.Errorf("icacls /remove:d: %v: %s", err, out)
		}
	})
}

// A bucket with an entry the sweep cannot read is named as incomplete, so its
// bytes read as what could be counted, not as all it holds, and a bucket
// read whole, or one not there at all, is not named.
func TestMeasureNamesTheBucketsItCouldNotReadWhole(t *testing.T) {
	// Arrange
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	for _, dir := range []string{filepath.Join(h.State, "locked"), h.Data, filepath.Join(h.Caches(), "uv")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{filepath.Join(h.State, "state.json"), filepath.Join(h.State, "locked", "hidden.json"), filepath.Join(h.Data, "notes.md"), filepath.Join(h.Caches(), "uv", "blob")} {
		if err := os.WriteFile(file, []byte("0123456789"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	denyListing(t, filepath.Join(h.State, "locked"))

	// Act
	buckets := Measure(h, nil)

	// Assert
	if !slices.Equal(buckets.Incomplete, []string{"state"}) {
		t.Errorf("incomplete = %v, want the state alone, whose locked folder cannot be listed", buckets.Incomplete)
	}
	if buckets.State != 10 || buckets.Data != 10 || buckets.Caches["uv"] != 10 {
		t.Errorf("state %d, data %d, uv %d bytes; want the 10 each could count", buckets.State, buckets.Data, buckets.Caches["uv"])
	}
}
