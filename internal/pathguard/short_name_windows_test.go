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

// A write to a .git a call gives by its short 8.3 name, GIT~1, is refused in
// a repository-free folder, where that name leads to the .git.
func TestRefusalMakesNoRepositoryByAShortName(t *testing.T) {
	// Arrange
	f := newFixture(t)
	config := filepath.Join(f.worktree, "pkg", ".git", "config")
	if err := os.MkdirAll(filepath.Dir(config), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	short := shortName(t, config)
	if strings.Contains(strings.ToLower(short), ".git") {
		t.Skipf("the volume of %s makes no short names, so no path here names .git by another", config)
	}
	f.boundary.RepositoryFree = []string{f.worktree}

	// Act
	refusal := f.boundary.Refusal(short, true)

	// Assert
	if !strings.Contains(refusal, "a .git in "+f.worktree) {
		t.Fatalf("Refusal(%s) = %q, want it refused as a .git in %s", short, refusal, f.worktree)
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

// A folder the shell moves to by a short 8.3 name, as a hosted runner's
// home is, stays as the command named it, so a refusal of a path read from
// it names that path, and where it leads. The folder is on the system
// drive, which makes short names where a Dev Drive makes none.
func TestToolRefusalNamesAPathFromAMovedToFolderAsTheCommandReachesIt(t *testing.T) {
	// Arrange
	root, err := os.MkdirTemp(filepath.Join(os.Getenv("SystemRoot"), "..", "Users", "Public"), "pathguard-")
	if err != nil {
		t.Skipf("no folder on the system drive to make: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	long := filepath.Join(root, "a profile with a long name")
	if err := os.MkdirAll(filepath.Join(long, ".ssh"), 0o755); err != nil {
		t.Fatal(err)
	}
	home := shortName(t, long)
	if strings.EqualFold(home, long) {
		t.Skipf("the volume of %s makes no short names", long)
	}
	worktree := filepath.Join(root, "worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	boundary := Boundary{Writable: []string{worktree}, Secrets: Secrets(home, "", "", ""), Cwd: worktree, UserHome: home, Getenv: func(string) string { return "" }}
	given := filepath.Join(home, ".ssh")
	resolved, err := filepath.EvalSymlinks(given)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	refusal := boundary.ToolRefusal("Bash", toolInput(t, map[string]string{"command": "cd ~ && cp -r .ssh ./keys"}))

	// Assert
	if !strings.Contains(refusal, given) || !strings.Contains(refusal, resolved) {
		t.Fatalf("refusal %q, want it to name %s and where it leads, %s", refusal, given, resolved)
	}
}
