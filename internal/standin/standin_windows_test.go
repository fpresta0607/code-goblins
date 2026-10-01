package standin

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// childMode names, for the child run of this test binary, how its test
// leaves the folder: "tempdir" to t.TempDir's own cleanup, "standin" to
// RemoveAtCleanup. Unset, this is the ordinary run and the child test does
// nothing.
const childMode = "STANDIN_TEST_CHILD"

// goRetries is longer than the 2 seconds for which Go's own t.TempDir cleanup
// retries a removal Windows refuses.
const goRetries = 4 * time.Second

// writeProgram writes a copy of this test binary to dir, as the tests that
// run stand-in programs do.
func writeProgram(t *testing.T, dir string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(dir, "program.exe")
	if err := os.WriteFile(program, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return program
}

// keepImage has Windows keep program's image the way it keeps one whose
// process has just ended: no process runs it and none has the file open, and
// it still cannot be removed. It returns what lets the image go.
func keepImage(t *testing.T, program string) (release func()) {
	t.Helper()
	name, err := windows.UTF16PtrFromString(program)
	if err != nil {
		t.Fatal(err)
	}
	file, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// SEC_IMAGE: the section is of the file as an executable image.
	const secImage = 0x1000000
	image, err := windows.CreateFileMapping(file, nil, windows.PAGE_READONLY|secImage, 0, 0, nil)
	_ = windows.CloseHandle(file)
	if err != nil {
		t.Fatal(err)
	}
	return func() { _ = windows.CloseHandle(image) }
}

// TestChildLeavesAProgramWindowsStillHas is the test the parent below runs in
// a child: it writes a program to its folder and ends while Windows still has
// the program's image, which it lets go only after goRetries.
func TestChildLeavesAProgramWindowsStillHas(t *testing.T) {
	mode := os.Getenv(childMode)
	if mode == "" {
		return
	}
	dir := t.TempDir()
	if mode == "standin" {
		RemoveAtCleanup(t, dir)
	}
	release := keepImage(t, writeProgram(t, dir))
	time.AfterFunc(goRetries, release)
}

// The failure, 16 times in 12 gate runs up to 2026-10-01: a test that wrote a
// stand-in program to its t.TempDir and ran it failed in cleanup alone, with
// "Access is denied" on the program. Caught live, the program refused removal
// half a second after its process was gone, with no process running it and
// none holding the file open, and went 2.1 seconds later: Windows keeps a run
// program's image for a while, and Go's cleanup retries for 2 seconds at
// most. The first case is that premise, a folder left to t.TempDir failing
// exactly so; the second is the same folder under RemoveAtCleanup.
func TestAFolderOfAProgramWindowsStillHasIsRemovedAtCleanup(t *testing.T) {
	for _, test := range []struct {
		name     string
		mode     string
		wantPass bool
	}{
		{"left to t.TempDir, the test fails in cleanup", "tempdir", false},
		{"under RemoveAtCleanup, the test passes", "standin", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			child := exec.Command(os.Args[0], "-test.run=^TestChildLeavesAProgramWindowsStillHas$", "-test.count=1")
			child.Env = append(os.Environ(), childMode+"="+test.mode)

			output, err := child.CombinedOutput()

			if passed := err == nil; passed != test.wantPass {
				t.Fatalf("the child test passed = %v, want %v:\n%s", passed, test.wantPass, output)
			}
			if failedInCleanup := strings.Contains(string(output), "TempDir RemoveAll cleanup") && strings.Contains(string(output), "program.exe: Access is denied."); failedInCleanup == test.wantPass {
				t.Fatalf("the child test failed in t.TempDir's cleanup = %v, want %v:\n%s", failedInCleanup, !test.wantPass, output)
			}
		})
	}
}

// A program that is never let go, as one still running is not, fails the
// removal once the wait runs out, and the folder goes once it is let go.
func TestRemovalGivesUpAtItsBoundOnAProgramNeverLetGo(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	release := keepImage(t, writeProgram(t, dir))
	defer release()
	started := time.Now()

	err := removeAll(dir, 300*time.Millisecond)

	if err == nil || !strings.Contains(err.Error(), "program.exe") {
		t.Fatalf("removing a folder whose program is never let go returned %v, want a failure naming program.exe", err)
	}
	if waited := time.Since(started); waited < 300*time.Millisecond || waited > 5*time.Second {
		t.Fatalf("the removal gave up after %s, want its bound of 300ms", waited)
	}
	release()
	if err := removeAll(dir, 300*time.Millisecond); err != nil {
		t.Fatalf("the folder could not be removed once its program was let go: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the folder is still there after its removal (%v)", err)
	}
}
