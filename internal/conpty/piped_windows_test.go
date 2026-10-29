package conpty

import (
	"io"
	"os"
	"regexp"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func startPipedChild(t *testing.T, spec PipedSpec) (*Piped, *screen) {
	t.Helper()
	spec.Args = []string{os.Args[0]}
	if spec.Env == nil {
		spec.Env = append(os.Environ(), childMode+"=echo")
	}
	piped, err := StartPiped(spec)
	if err != nil {
		t.Fatalf("StartPiped: %v", err)
	}
	s := watch(piped)
	t.Cleanup(func() {
		if !s.closed {
			_ = piped.Close()
		}
		<-s.ended
	})
	s.waitFor(t, "ready")
	return piped, s
}

func writeLine(t *testing.T, piped *Piped, line string) {
	t.Helper()
	if _, err := piped.Write([]byte(line + "\n")); err != nil {
		t.Fatalf("Write %q: %v", line, err)
	}
}

// A piped process reads the lines written to it, where and with what it was
// told to start, and what it writes comes back out.
func TestAPipedProcessCarriesLinesInAndOutputOut(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	piped, s := startPipedChild(t, PipedSpec{Dir: dir, Env: append(os.Environ(), childMode+"=echo", "CONPTY_TEST_VALUE=fleet")})

	// Act
	writeLine(t, piped, "hello goblin")
	writeLine(t, piped, "env CONPTY_TEST_VALUE")
	writeLine(t, piped, "cwd")

	// Assert
	s.waitFor(t, "got hello goblin")
	s.waitFor(t, "env fleet")
	s.waitFor(t, regexp.QuoteMeta("cwd "+dir))
}

// The exit code is kept, and the output ends once the process has, either
// because it chose to or because its input closed.
func TestAPipedProcessReportsHowItEndedAndEndsItsOutput(t *testing.T) {
	for name, c := range map[string]struct {
		end  func(t *testing.T, piped *Piped)
		code uint32
	}{
		"an exit of its own": {func(t *testing.T, piped *Piped) { writeLine(t, piped, "exit 7") }, 7},
		"its input closed":   {func(t *testing.T, piped *Piped) { _ = piped.CloseInput() }, 0},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			piped, s := startPipedChild(t, PipedSpec{})

			// Act
			c.end(t, piped)

			// Assert
			select {
			case <-piped.Done():
			case <-time.After(time.Minute):
				t.Fatal("the process did not exit")
			}
			if code := piped.ExitCode(); code != c.code {
				t.Errorf("exit code = %d, want %d", code, c.code)
			}
			select {
			case <-s.ended:
			case <-time.After(time.Minute):
				t.Fatal("the output did not end after the process exited")
			}
		})
	}
}

// Close ends the process and what it started, as a terminal's Close does, but
// not a process that broke away from its job, as a goblin host the CFO
// launches must to outlive the CFO's session.
func TestClosingAPipedProcessEndsItsTreeButNotWhatBrokeAway(t *testing.T) {
	// Arrange
	piped, s := startPipedChild(t, PipedSpec{})
	writeLine(t, piped, "spawn")
	grandchild, err := strconv.Atoi(s.waitFor(t, `grandchild (\d+)`)[1])
	if err != nil {
		t.Fatal(err)
	}
	writeLine(t, piped, "spawn-breakaway")
	brokeAway, err := strconv.Atoi(s.waitFor(t, `broke away (\d+)`)[1])
	if err != nil {
		t.Fatal(err)
	}
	away, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(brokeAway))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = windows.TerminateProcess(away, 1)
		_ = windows.CloseHandle(away)
	})

	// Act
	s.closed = true
	closeErr := piped.Close()

	// Assert
	if closeErr != nil {
		t.Fatalf("Close: %v", closeErr)
	}
	for _, pid := range []int{piped.PID(), grandchild} {
		if !exited(pid) {
			t.Errorf("pid %d is still running after Close", pid)
		}
	}
	if !running(away) {
		t.Error("the process that broke away ended with the piped process")
	}
}

// A piped process that cannot start says so, and leaves nothing behind.
func TestStartPipedRefusesWhatItCannotRun(t *testing.T) {
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"no command":        {nil, "a command is required"},
		"a missing program": {[]string{`C:\no\such\program.exe`}, "start process"},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			_, err := StartPiped(PipedSpec{Args: c.args, Stderr: io.Discard})

			// Assert
			if err == nil || !regexp.MustCompile(regexp.QuoteMeta(c.want)).MatchString(err.Error()) {
				t.Errorf("StartPiped error = %v, want one that says %q", err, c.want)
			}
		})
	}
}
