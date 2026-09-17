package proc

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The read must agree with the operating system on this process, which is the
// only working directory a test can know independently.
func TestWorkingDirectoryReadsThisProcess(t *testing.T) {
	want, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got, err := WorkingDirectory(Self())
	if err != nil {
		t.Fatalf("WorkingDirectory(self): %v", err)
	}
	if !sameDir(got, want) {
		t.Errorf("WorkingDirectory(self) = %q, want %q", got, want)
	}
}

// The whole reason this exists: a process started from a directory carries no
// path in its command line, so the directory is invisible to every process
// listing. A dev server left running in a retired worktree is exactly this
// shape, and it is the leak the runtime report is built to find.
func TestWorkingDirectoryFindsADirectoryNoCommandLineNames(t *testing.T) {
	dir := t.TempDir()
	command := exec.Command("cmd.exe", "/c", "pause")
	command.Dir = dir
	if err := command.Start(); err != nil {
		t.Skipf("could not start a child process: %v", err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	})

	// The command line names cmd.exe and "pause" and nothing else; only the
	// parameter block knows where it is running.
	if strings.Contains(strings.Join(command.Args, " "), dir) {
		t.Fatalf("the fixture's own command line names the directory, so it proves nothing")
	}

	var got string
	var err error
	for attempt := 0; attempt < 50; attempt++ {
		got, err = WorkingDirectory(command.Process.Pid)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("WorkingDirectory(child): %v", err)
	}
	if !sameDir(got, dir) {
		t.Errorf("WorkingDirectory(child) = %q, want %q", got, dir)
	}
}

// A process this one cannot open must report a plain failure rather than an
// empty string that would read as "started from the drive root". PID 4 is the
// System process, which no unprivileged caller can open.
func TestWorkingDirectoryRefusesAProcessItCannotOpen(t *testing.T) {
	got, err := WorkingDirectory(4)
	if err == nil {
		t.Skipf("this session can open the System process (%q); the refusal path is untestable here", got)
	}
	if !errors.Is(err, ErrDirectoryUnreadable) {
		t.Errorf("err = %v, want it to wrap ErrDirectoryUnreadable", err)
	}
	if got != "" {
		t.Errorf("directory = %q, want nothing alongside an error", got)
	}
}

func TestWorkingDirectoryRefusesAPIDThatIsNotRunning(t *testing.T) {
	// A pid far above the live range: the read must fail, never guess.
	if got, err := WorkingDirectory(0x7FFFFFF0); err == nil {
		t.Errorf("WorkingDirectory(unused pid) = %q, want an error", got)
	}
}

// sameDir compares two Windows paths allowing for case and a trailing
// separator, which the parameter block and os.Getwd spell differently.
func sameDir(left, right string) bool {
	clean := func(value string) string {
		return strings.ToLower(strings.TrimRight(filepath.Clean(value), `\`))
	}
	return clean(left) == clean(right)
}
