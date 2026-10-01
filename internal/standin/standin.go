// Package standin is test support for a test that runs programs it wrote to
// a temp folder: the copies of its own test binary this repository's tests
// stand in for cfo, git, a harness and the rest.
package standin

import (
	"errors"
	"io/fs"
	"os"
	"runtime"
	"testing"
	"time"
)

// releaseWait is how long a cleanup waits for Windows to let go of a program
// that ran. Windows keeps a program's image for a while after its process is
// gone, with no process running it and none holding the file open, and
// refuses to remove it meanwhile; on a loaded machine that was seen to last
// 2.6 seconds after the process was ended. Go's own t.TempDir cleanup retries
// such a refusal for 2 seconds at most, and then fails the test.
const releaseWait = 30 * time.Second

// RemoveAtCleanup removes dir when the test ends, waiting up to releaseWait
// for Windows to let go of a program the test ran from it, and fails the test
// when it still cannot: a program the test left running is never let go.
//
// Cleanups run last registered first. Register this before the cleanup that
// ends the test's programs, so they are ended first, and after the t.TempDir
// that holds dir, so that cleanup finds dir already gone.
func RemoveAtCleanup(t testing.TB, dir string) {
	t.Helper()
	t.Cleanup(func() {
		started := time.Now()
		if err := removeAll(dir, releaseWait); err != nil {
			t.Errorf("standin: %s could not be removed in the %s after the test: %v", dir, releaseWait, err)
		} else if waited := time.Since(started); waited > time.Second {
			t.Logf("standin: %s took %s to remove", dir, waited.Round(time.Millisecond))
		}
	})
}

// removeAll removes dir, trying again for up to wait while Windows refuses
// access to something in it.
func removeAll(dir string, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		err := os.RemoveAll(dir)
		if err == nil || runtime.GOOS != "windows" || !errors.Is(err, fs.ErrPermission) || !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
}
