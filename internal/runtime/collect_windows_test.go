package runtime

import (
	"os"
	"syscall"
	"testing"
)

// makeUnreadable opens path with no sharing, so every other read of it
// fails the way a record being rewritten under the collector does. It returns
// the function that releases it.
func makeUnreadable(t *testing.T, path string) func() {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("lock %s: %v", path, err)
	}
	if _, err := os.ReadFile(path); err == nil {
		syscall.CloseHandle(handle)
		t.Fatalf("%s is still readable, so the test proves nothing", path)
	}
	return func() { syscall.CloseHandle(handle) }
}
