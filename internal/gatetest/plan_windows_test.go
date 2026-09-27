package gatetest

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// A module reached under its 8.3 short name, as GitHub runners spell their
// temp directory, still has its changed package chosen: go list spells each
// package from the short directory while git reports the long root. A volume
// without 8.3 names skips it locally, but under CI (GitHub Actions sets
// CI=true) it fails instead, so a green CI run means the test ran and passed.
func TestReadChoosesTheChangedPackageUnderAShortName(t *testing.T) {
	// Arrange
	dir := newModule(t)
	path, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, syscall.MAX_LONG_PATH)
	n, err := syscall.GetShortPathName(path, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) > len(buf) {
		t.Fatalf("short name of %s: %v", dir, err)
	}
	short := syscall.UTF16ToString(buf[:n])
	if filepath.Clean(short) == filepath.Clean(dir) {
		if os.Getenv("CI") == "true" {
			t.Fatalf("CI's volume has no 8.3 short name for %s, so the short-name path went untested", dir)
		}
		t.Skipf("outside CI the volume has no 8.3 short name for %s", dir)
	}
	write(t, filepath.Join(dir, "c", "c.go"), "package c\n\nconst C = 1\n")

	// Act
	plan, err := Read(context.Background(), execx.OSRunner{}, short)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, choice := range plan.Choices {
		got = append(got, choice.String())
	}
	if want := []string{"example.com/m/c (changed)"}; !slices.Equal(got, want) {
		t.Errorf("Read under %s = %q; want %q", short, got, want)
	}
}
