package pathguard

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A path a call gives by a short 8.3 name, as a hosted runner's temporary
// folder is, is refused naming it as the call gave it and where it leads.
func TestRefusalNamesAShortNamedPathAsTheCallGaveIt(t *testing.T) {
	// Arrange
	f := newFixture(t)
	long := filepath.Join(f.outside, "a folder with a long name", "notes.txt")
	if err := os.MkdirAll(filepath.Dir(long), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(long, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	short := shortName(t, long)
	resolved, err := filepath.EvalSymlinks(short)
	if err != nil {
		t.Fatal(err)
	}
	if strings.EqualFold(short, resolved) {
		t.Skipf("the volume of %s makes no short names, so no path here differs from where it leads", long)
	}

	// Act
	refusal := f.boundary.Refusal(short, true)

	// Assert
	if !strings.Contains(refusal, short) || !strings.Contains(refusal, resolved) {
		t.Fatalf("Refusal(%s) = %q, want it to name %s and where it leads, %s", short, refusal, short, resolved)
	}
}

// shortName is path by its short 8.3 name, as Windows gives it.
func shortName(t *testing.T, path string) string {
	t.Helper()
	long, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]uint16, syscall.MAX_PATH)
	length, err := syscall.GetShortPathName(long, &buffer[0], uint32(len(buffer)))
	if err != nil {
		t.Fatal(err)
	}
	return syscall.UTF16ToString(buffer[:length])
}
