package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

func TestGatePrepareUnderAShortTemporaryRoot(t *testing.T) {
	dir := t.TempDir()
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
	if filepath.Clean(short) == filepath.Clean(fsx.LongPath(dir)) {
		if os.Getenv("CI") == "true" {
			t.Fatalf("CI's volume has no 8.3 short name for %s, so the short-name path went untested", dir)
		}
		t.Skipf("outside CI the volume has no 8.3 short name for %s", dir)
	}
	t.Setenv("TMP", short)
	t.Setenv("TEMP", short)
	t.Setenv("GOTMPDIR", short)

	t.Run("configured preparation", TestGatePrepareConfiguredCommandReadiesFrontendBeforeTest)
	t.Run("affected changes", TestGatePrepareInstallsOnlyForFrontendChanges)
}
