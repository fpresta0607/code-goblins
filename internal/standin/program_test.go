package standin

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sumOf(t *testing.T, path string) [32]byte {
	t.Helper()
	return sha256.Sum256(mustRead(t, path))
}

// The stand-in is built once into its folder and every later ask is given
// that file as it is, so the program antivirus meets is one it has met. One
// that is no longer the bytes its build recorded, as one a test wrote over,
// is built again, to the same bytes.
func TestTheStandInIsBuiltOnceAndReused(t *testing.T) {
	// Arrange
	root := t.TempDir()

	// Act
	first, err := ensure(root)
	if err != nil {
		t.Fatal(err)
	}
	built, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	sum := sumOf(t, first)
	second, err := ensure(root)

	// Assert
	if err != nil || second != first {
		t.Fatalf("the second ask gave %q (%v), want the first build %q", second, err, first)
	}
	if again, err := os.Stat(second); err != nil || !again.ModTime().Equal(built.ModTime()) || !os.SameFile(built, again) {
		t.Errorf("the second ask built the stand-in again (%v), want the first build left as it was", err)
	}
	if want := filepath.Join(root, programKey(), "standin"+executableSuffix()); first != want {
		t.Errorf("the stand-in is at %q, want %q: one folder for each toolchain and source", first, want)
	}
	if left, _ := filepath.Glob(filepath.Join(root, programKey(), "build-*")); len(left) != 0 {
		t.Errorf("the build left %v behind", left)
	}

	t.Run("a damaged one is built again to the same bytes", func(t *testing.T) {
		if err := os.WriteFile(first, []byte("not the stand-in"), 0o755); err != nil {
			t.Fatal(err)
		}

		repaired, err := ensure(root)

		if err != nil || repaired != first {
			t.Fatalf("the damaged stand-in gave %q (%v), want it built again at %q", repaired, err, first)
		}
		if got := sumOf(t, repaired); got != sum {
			t.Errorf("the stand-in built again is %x, want the first build's %x", got, sum)
		}
	})
}

// Two builds of the stand-in, in two folders, are the same bytes: a machine
// that lost its cache, or a GitHub runner, builds the program antivirus has
// already met.
func TestABuildElsewhereIsTheSameBytes(t *testing.T) {
	// Arrange
	here := sumOf(t, Program(t))

	// Act
	elsewhere, err := ensure(t.TempDir())

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if got := sumOf(t, elsewhere); got != here {
		t.Errorf("a second build is %x, want the same bytes as the first, %x", got, here)
	}
}

// run starts program with args under env and returns what it printed, what
// it wrote to stderr and its exit code.
func run(t *testing.T, env []string, program string, args ...string) (stdout, stderr string, exit int) {
	t.Helper()
	var out, errs bytes.Buffer
	command := exec.Command(program, args...)
	command.Env = append(os.Environ(), env...)
	command.Stdout, command.Stderr = &out, &errs
	err := command.Run()
	var exited *exec.ExitError
	if err != nil && !errors.As(err, &exited) {
		t.Fatalf("start %s: %v", program, err)
	}
	return out.String(), errs.String(), command.ProcessState.ExitCode()
}

// A stand-in put under a program's name records each command under that
// name and does what the first rule matching the command says.
func TestAStandInDoesWhatItsRulesSay(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	RemoveAtCleanup(t, dir)
	standIn := filepath.Join(dir, "No-Mistakes"+executableSuffix())
	Put(t, standIn)
	record := filepath.Join(dir, "record.txt")
	env := Env(record,
		Rule{Args: "--version", Stdout: "no-mistakes version v1.0.0\n", Stderr: "a new version is available\n"},
		Rule{Args: "daemon stop", Exit: 1},
		Rule{Args: "daemon stop", Exit: 7},
	)
	for _, test := range []struct {
		name               string
		args               []string
		wantOut, wantError string
		wantExit           int
	}{
		{"a command with a rule prints what the rule says", []string{"--version"}, "no-mistakes version v1.0.0\n", "a new version is available\n", 0},
		{"the first rule that matches decides", []string{"daemon", "stop"}, "", "", 1},
		{"a command with no rule succeeds and says nothing", []string{"daemon", "start"}, "", "", 0},
		{"no command at all succeeds", nil, "", "", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			stdout, stderr, exit := run(t, env, standIn, test.args...)

			// Assert
			if stdout != test.wantOut || stderr != test.wantError || exit != test.wantExit {
				t.Errorf("stand-in %q printed %q, said %q and exited %d, want %q, %q and %d", test.args, stdout, stderr, exit, test.wantOut, test.wantError, test.wantExit)
			}
		})
	}
	recorded, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if want := "no-mistakes --version\r\nno-mistakes daemon stop\r\nno-mistakes daemon start\r\nno-mistakes\r\n"; string(recorded) != want {
		t.Errorf("the stand-in recorded %q, want %q", recorded, want)
	}

	t.Run("a rule for any command decides every command", func(t *testing.T) {
		_, _, exit := run(t, Env("", Rule{Any: true, Exit: 3}), standIn, "install", "--quiet")

		if exit != 3 {
			t.Errorf("the stand-in exited %d, want the rule's 3", exit)
		}
	})

	t.Run("a rule can match a command that goes on, and put the stand-in in a home", func(t *testing.T) {
		home := filepath.Join(t.TempDir(), "CodeGoblins", "bin")
		RemoveAtCleanup(t, filepath.Dir(home))
		installed := filepath.Join(home, "cfo"+executableSuffix())
		env := Env("", Rule{Args: "install", Prefix: true, CopyTo: []string{installed}})

		_, _, exit := run(t, env, standIn, "install", "--window-built")
		_, _, other := run(t, env, standIn, "installer")

		if exit != 0 || sumOf(t, installed) != sumOf(t, Program(t)) {
			t.Errorf("the stand-in exited %d and did not put itself at %s", exit, installed)
		}
		if other != 0 {
			t.Errorf("the stand-in exited %d for another command, want 0", other)
		}
		if _, _, exit := run(t, Env("", Rule{Args: "install", Prefix: true, Exit: 4}), standIn, "installer"); exit != 0 {
			t.Errorf("a rule for install decided installer too: exit %d", exit)
		}
	})

	t.Run("rules it cannot read stop it", func(t *testing.T) {
		_, stderr, exit := run(t, []string{"CFO_STANDIN_RULES=not rules"}, standIn, "install")

		if exit != 125 || !strings.Contains(stderr, "CFO_STANDIN_RULES") {
			t.Errorf("the stand-in exited %d saying %q, want 125 and a line naming CFO_STANDIN_RULES", exit, stderr)
		}
	})
}

// A stand-in told to hold keeps running, as a program still at work does,
// until the test ends it.
func TestAStandInToldToHoldKeepsRunning(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	RemoveAtCleanup(t, dir)
	standIn := filepath.Join(dir, "no-mistakes"+executableSuffix())
	Put(t, standIn)
	held := exec.Command(standIn, "hold")
	held.Env = append(os.Environ(), Env("", Rule{Args: "hold", Hold: true})...)
	exited := make(chan struct{})

	// Act
	if err := held.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = held.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = held.Process.Kill()
		<-exited
	})

	// Assert
	select {
	case <-exited:
		t.Fatal("the stand-in exited, want it held")
	case <-time.After(500 * time.Millisecond):
	}
}

// Put writes the one stand-in's bytes, so a program a test put somewhere is
// one antivirus has met.
func TestPutWritesTheOneStandIn(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "cfo"+executableSuffix())

	// Act
	Put(t, path)

	// Assert
	if got, want := sumOf(t, path), sumOf(t, Program(t)); got != want {
		t.Errorf("Put wrote %x, want the stand-in's %x", got, want)
	}
	if !bytes.Equal(Bytes(t), mustRead(t, path)) {
		t.Errorf("Bytes is not what Put wrote")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

// No stand-in is put under the name of a program Windows ships, whatever its
// case or extension: that is how malware hides as part of Windows, and
// Defender detected the one stand-in that did it.
func TestNoStandInIsPutUnderAWindowsProgramsName(t *testing.T) {
	for name, isRefused := range map[string]bool{
		"rundll32.exe":    true,
		"RunDll32.EXE":    true,
		"cmd.exe":         true,
		"powershell.exe":  true,
		"explorer.exe":    true,
		"svchost.exe":     true,
		"where.cmd":       true,
		"more.com":        true,
		"cfo.exe":         false,
		"goblins.exe":     false,
		"claude.exe":      false,
		"codex.exe":       false,
		"git.exe":         false,
		"no-mistakes.exe": false,
		"herdr.exe":       false,
		"rundll32.txt":    false,
		"built":           false,
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			path := filepath.Join(t.TempDir(), name)

			// Act
			err := place(Bytes(t), path)

			// Assert
			if refused := err != nil; refused != isRefused {
				t.Fatalf("putting a stand-in at %s returned %v, want refused = %v", name, err, isRefused)
			}
			if _, statErr := os.Stat(path); isRefused && !os.IsNotExist(statErr) {
				t.Errorf("a refused stand-in was written all the same (%v)", statErr)
			}
			if isRefused && !strings.Contains(err.Error(), name) {
				t.Errorf("the refusal %q does not name %s", err, name)
			}
		})
	}
}
