package janitor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/reap"
)

// denyListing keeps this process, and every other, from listing dir until
// the test ends, as a folder another account owns can. A session that reads
// past the deny, as an elevated one can, skips the test.
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
	if _, err := os.ReadDir(dir); err == nil {
		t.Skip("the deny did not take, as in an elevated session")
	}
}

// writeTen writes a file of ten bytes at path, with its folders.
func writeTen(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A bucket with an entry the sweep cannot read is named as cfo runtime's
// table names it, so its bytes read as what could be counted, not as all it
// holds, and so is a caches folder that cannot be listed. A bucket read
// whole, or one not there at all, as this home's bin, is not named. The
// verification store outside the home is measured as a bucket too.
func TestMeasureNamesTheBucketsItCouldNotReadWhole(t *testing.T) {
	for name, row := range map[string]struct {
		denied string
		want   []string
	}{
		"a folder in the state":              {denied: "state", want: []string{"state"}},
		"a folder in a cache":                {denied: `caches\uv`, want: []string{`caches\uv`}},
		"the caches folder itself":           {denied: "caches", want: []string{"caches"}},
		"a folder in the verification store": {denied: "verify", want: []string{"verification"}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
			store := filepath.Join(root, "verify")
			for _, file := range []string{filepath.Join(h.State, "state.json"), filepath.Join(h.State, "locked", "hidden.json"), filepath.Join(h.Data, "notes.md"), filepath.Join(h.Caches(), "uv", "blob"), filepath.Join(h.Caches(), "uv", "locked", "hidden"), filepath.Join(store, "executions", "record.json"), filepath.Join(store, "locked", "hidden.json")} {
				writeTen(t, file)
			}
			denied := filepath.Join(root, row.denied)
			if row.denied != "caches" {
				denied = filepath.Join(denied, "locked")
			}
			denyListing(t, denied)

			// Act
			buckets := Measure(h, nil, store)

			// Assert
			if !slices.Equal(buckets.Incomplete, row.want) {
				t.Errorf("incomplete = %v, want %v", buckets.Incomplete, row.want)
			}
			if buckets.State < 10 || buckets.Data != 10 || buckets.Verification < 10 {
				t.Errorf("state %d, data %d, verification %d bytes; want what each could count", buckets.State, buckets.Data, buckets.Verification)
			}
		})
	}
}

// A sweep says when the caches could not all be read, since their size
// against the cap is then only what it could count.
func TestSweepSaysWhenTheCachesCouldNotAllBeRead(t *testing.T) {
	// Arrange
	f := newSweepFixture(t)
	writeTen(t, filepath.Join(f.home.Caches(), "uv", "blob"))
	writeTen(t, filepath.Join(f.home.Caches(), "uv", "locked", "hidden"))
	denyListing(t, filepath.Join(f.home.Caches(), "uv", "locked"))

	// Act
	record := Sweep(context.Background(), f.config(reap.Inventory{}))

	// Assert
	isSaid := false
	for _, note := range record.Notes {
		isSaid = isSaid || strings.Contains(note, "the caches could not all be read")
	}
	if !isSaid {
		t.Errorf("notes = %q; want one that says the caches could not all be read", record.Notes)
	}
}

// A sweep measures the verification store it is given beside the home.
func TestSweepMeasuresTheVerificationStoreItIsGiven(t *testing.T) {
	// Arrange
	f := newSweepFixture(t)
	store := t.TempDir()
	writeTen(t, filepath.Join(store, "executions", "record.json"))
	cfg := f.config(reap.Inventory{})
	cfg.VerificationStore = store

	// Act
	record := Sweep(context.Background(), cfg)

	// Assert
	if record.Buckets.Verification != 10 || record.Buckets.VerificationStore != store {
		t.Errorf("the store %d bytes at %q; want 10 at %q", record.Buckets.Verification, record.Buckets.VerificationStore, store)
	}
}

// The verification store is measured where it is, outside the home, so the
// home in all leaves it out, and a store whose folder could not be named is
// named as not read rather than taken for an empty one.
func TestMeasureCountsTheVerificationStoreApartFromTheHome(t *testing.T) {
	for name, row := range map[string]struct {
		isFound          bool
		wantVerification int64
		wantIncomplete   []string
	}{
		"a store found": {isFound: true, wantVerification: 20},
		"a store whose folder could not be named": {wantIncomplete: []string{"verification"}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
			writeTen(t, filepath.Join(h.Data, "notes.md"))
			store := filepath.Join(t.TempDir(), "verify")
			writeTen(t, filepath.Join(store, "executions", "record.json"))
			writeTen(t, filepath.Join(store, "passes", "pass.json"))
			named := ""
			if row.isFound {
				named = store
			}

			// Act
			buckets := Measure(h, nil, named)

			// Assert
			if buckets.Verification != row.wantVerification || buckets.VerificationStore != named || buckets.Total() != 10 || !slices.Equal(buckets.Incomplete, row.wantIncomplete) {
				t.Errorf("verification %d bytes at %q, the home %d, incomplete %v; want %d at %q, the home's own 10, incomplete %v", buckets.Verification, buckets.VerificationStore, buckets.Total(), buckets.Incomplete, row.wantVerification, named, row.wantIncomplete)
			}
		})
	}
}
