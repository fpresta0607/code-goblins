package execx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestOSRunnerHelper(t *testing.T) {
	if os.Getenv("EXECX_HELPER") != "1" {
		return
	}

	switch os.Getenv("EXECX_MODE") {
	case "nonzero":
		fmt.Fprint(os.Stderr, "child stderr")
		os.Exit(7)
	case "sleep":
		time.Sleep(10 * time.Second)
	case "orphan-stdout":
		// Like an npm .cmd shim: the direct child starts a grandchild that
		// inherits its stdout, so killing the direct child leaves the pipe open.
		grandchild := exec.Command(os.Args[0], "-test.run=TestOSRunnerHelper", "--")
		grandchild.Env = append(os.Environ(), "EXECX_MODE=hold-stdout")
		grandchild.Stdout = os.Stdout
		if err := grandchild.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(9)
		}
		time.Sleep(30 * time.Second)
	case "hold-stdout", "answer-then-hold":
		if os.Getenv("EXECX_MODE") == "answer-then-hold" {
			fmt.Fprintln(os.Stdout, "answered")
			fmt.Fprintln(os.Stderr, "still working")
		}
		marker := os.Getenv("EXECX_MARKER")
		if err := os.WriteFile(marker+".held", nil, 0o600); err != nil {
			os.Exit(9)
		}
		for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(marker + ".release"); err == nil {
				break
			}
		}
		_ = os.WriteFile(marker+".released", nil, 0o600)
		os.Exit(0)
	case "survive-cancel", "survive-cancel-empty-marker":
		marker := os.Getenv("EXECX_MARKER")
		if os.Getenv("EXECX_MODE") == "survive-cancel-empty-marker" {
			markerFile, err := os.Create(marker)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(9)
			}
			if err := markerFile.Close(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(9)
			}
			time.Sleep(100 * time.Millisecond)
		}
		time.Sleep(200 * time.Millisecond)
		if err := os.WriteFile(marker+".pid", []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(9)
		}
		if err := os.WriteFile(marker, []byte("alive"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(9)
		}
		// Live on after answering, as a helper on a loaded machine does, until
		// the test has opened this process by its pid, so the pid it waits on
		// cannot be another's.
		for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(marker + ".release"); err == nil {
				break
			}
		}
		return
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(8)
	}
	fmt.Fprintln(os.Stdout, cwd)
	fmt.Fprintln(os.Stdout, os.Getenv("EXECX_SENTINEL"))
	for _, arg := range os.Args {
		fmt.Fprintf(os.Stdout, "arg:%s\n", arg)
	}
}

func helperRequest(dir string, env []string, args ...string) Request {
	return Request{
		Dir:  dir,
		Env:  env,
		Name: os.Args[0],
		Args: append([]string{"-test.run=TestOSRunnerHelper", "--"}, args...),
	}
}

func TestOSRunnerPreservesRequestExecutionDetails(t *testing.T) {
	dir := t.TempDir()
	req := helperRequest(dir, []string{"EXECX_HELPER=1", "EXECX_SENTINEL=provided"}, "one", "two words")

	result, err := (OSRunner{}).Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr = %q", result.ExitCode, result.Stderr)
	}

	lines := strings.Split(strings.TrimSpace(string(result.Stdout)), "\n")
	if len(lines) < 2 {
		t.Fatalf("stdout = %q, want working directory and environment", result.Stdout)
	}
	if !strings.EqualFold(filepath.Clean(lines[0]), filepath.Clean(dir)) {
		t.Errorf("working directory = %q, want %q", lines[0], dir)
	}
	if lines[1] != "provided" {
		t.Errorf("sentinel = %q, want provided", lines[1])
	}
	stdout := string(result.Stdout)
	for _, arg := range []string{"arg:one", "arg:two words"} {
		if !strings.Contains(stdout, arg) {
			t.Errorf("stdout = %q, missing %q", stdout, arg)
		}
	}
	if len(result.Stderr) != 0 {
		t.Errorf("stderr = %q, want empty", result.Stderr)
	}
}

func TestOSRunnerPreservesCallerEnvironmentWhenEnvIsNil(t *testing.T) {
	t.Setenv("EXECX_HELPER", "1")
	t.Setenv("EXECX_SENTINEL", "caller")
	result, err := (OSRunner{}).Run(context.Background(), helperRequest(t.TempDir(), nil))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(string(result.Stdout), "\ncaller\n") {
		t.Errorf("stdout = %q, want caller environment", result.Stdout)
	}
}

func TestOSRunnerReturnsNormalNonZeroExitWithSeparateStderr(t *testing.T) {
	result, err := (OSRunner{}).Run(context.Background(), helperRequest(t.TempDir(), []string{"EXECX_HELPER=1", "EXECX_MODE=nonzero"}))
	if err != nil {
		t.Fatalf("Run returned error for normal non-zero exit: %v", err)
	}
	if result.ExitCode != 7 {
		t.Errorf("ExitCode = %d, want 7", result.ExitCode)
	}
	if string(result.Stderr) != "child stderr" {
		t.Errorf("stderr = %q, want child stderr", result.Stderr)
	}
	if len(result.Stdout) != 0 {
		t.Errorf("stdout = %q, want empty", result.Stdout)
	}
}

func TestOSRunnerReturnsStartError(t *testing.T) {
	_, err := (OSRunner{}).Run(context.Background(), Request{Name: filepath.Join(t.TempDir(), "missing-executable")})
	if err == nil {
		t.Fatal("Run returned nil error for a missing executable")
	}
}

// cancelOnceHeld cancels ctx once the helper that marker names says it
// holds, which it says only after it wrote what it writes first, so the
// test's context ends after that however slowly a loaded machine started the
// helper. The channel it returns says it cancelled.
func cancelOnceHeld(ctx context.Context, cancel context.CancelFunc, marker string) <-chan struct{} {
	cancelled := make(chan struct{}, 1)
	go func() {
		for ctx.Err() == nil {
			if _, err := os.Stat(marker + ".held"); err == nil {
				cancelled <- struct{}{}
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	return cancelled
}

func TestOSRunnerReturnsContextCancellationWithTheOutputSoFar(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "child")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelled := cancelOnceHeld(ctx, cancel, marker)

	result, err := (OSRunner{}).Run(ctx, helperRequest(t.TempDir(), []string{"EXECX_HELPER=1", "EXECX_MODE=answer-then-hold", "EXECX_MARKER=" + marker}))
	select {
	case <-cancelled:
	default:
		t.Fatalf("Run returned before the process answered: %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context canceled", err)
	}
	if string(result.Stdout) != "answered\n" || string(result.Stderr) != "still working\n" || result.ExitCode != 0 {
		t.Errorf("result = {Stdout: %q, Stderr: %q, ExitCode: %d}, want what the process wrote before its context ended and no exit code", result.Stdout, result.Stderr, result.ExitCode)
	}
}

func TestOSRunnerReturnsAfterCancellationWhileAGrandchildHoldsStdout(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "grandchild")
	t.Cleanup(func() {
		// Release the grandchild and let it exit before go test removes its
		// working directory and the Windows test executable.
		_ = os.WriteFile(marker+".release", nil, 0o600)
		for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(marker + ".released"); err == nil {
				time.Sleep(100 * time.Millisecond)
				return
			}
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelled := make(chan struct{}, 1)
	go func() {
		for ctx.Err() == nil {
			if _, err := os.Stat(marker + ".held"); err == nil {
				cancelled <- struct{}{}
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	_, err := (OSRunner{}).Run(ctx, helperRequest(t.TempDir(), []string{"EXECX_HELPER=1", "EXECX_MODE=orphan-stdout", "EXECX_MARKER=" + marker}))
	_, releasedErr := os.Stat(marker + ".released")

	select {
	case <-cancelled:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v, want context canceled", err)
		}
		if !os.IsNotExist(releasedErr) {
			t.Errorf("Run returned only once the grandchild let go of stdout (%v), want it bounded while a grandchild holds stdout open", releasedErr)
		}
	default:
		t.Fatalf("Run returned before the grandchild held stdout: %v", err)
	}
}

// The child holds until this test releases it, so Start returning while the
// child is still held proves Start did not wait for it to exit, however slowly
// a loaded machine spawns processes; a fixed time limit could not tell the two
// apart.
func TestOSRunnerStartsWithoutWaitingForChildExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	marker := filepath.Join(t.TempDir(), "child")

	err := (OSRunner{}).Start(ctx, helperRequest(t.TempDir(), []string{"EXECX_HELPER=1", "EXECX_MODE=hold-stdout", "EXECX_MARKER=" + marker}))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := os.Stat(marker + ".released"); !os.IsNotExist(err) {
		t.Fatalf("the child had already finished when Start returned (%v), want a non-blocking launch", err)
	}
	// Release the child and let it exit before go test removes the Windows
	// test executable.
	if err := os.WriteFile(marker+".release", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(marker + ".released"); err == nil {
			time.Sleep(100 * time.Millisecond)
			return
		}
	}
	t.Fatal("the released child never finished")
}

func TestOSRunnerStartSurvivesRequestContextCancellation(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "child-survived")
	ctx, cancel := context.WithCancel(context.Background())
	err := (OSRunner{}).Start(ctx, helperRequest(t.TempDir(), []string{
		"EXECX_HELPER=1",
		"EXECX_MODE=survive-cancel-empty-marker",
		"EXECX_MARKER=" + marker,
	}))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	cancel()

	deadline := time.Now().Add(time.Minute)
	for {
		if data, err := os.ReadFile(marker); err == nil {
			switch string(data) {
			case "alive":
				// The helper's working directory, which t.TempDir removes, and
				// the test executable, which go test removes, stay in use until
				// the helper has ended, and Start reports no end. The helper is
				// opened before it is released, so the pid waited on is still
				// the helper's.
				data, err := os.ReadFile(marker + ".pid")
				if err != nil {
					t.Fatal(err)
				}
				pid, err := strconv.Atoi(string(data))
				if err != nil {
					t.Fatalf("helper pid = %q: %v", data, err)
				}
				helper, err := os.FindProcess(pid)
				if err != nil {
					t.Fatalf("FindProcess(%d): %v", pid, err)
				}
				if err := os.WriteFile(marker+".release", nil, 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := helper.Wait(); err != nil {
					t.Fatalf("waiting for helper %d to end: %v", pid, err)
				}
				return
			case "":
				// os.WriteFile creates the marker before writing its contents.
				// Keep polling while that write is in progress.
			default:
				t.Fatalf("marker = %q, want alive", data)
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("started child did not survive request context cancellation")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestOSRunnerStartRejectsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := (OSRunner{}).Start(ctx, helperRequest(t.TempDir(), []string{"EXECX_HELPER=1"})); err == nil {
		t.Fatal("Start returned nil for a cancelled context")
	}
}

// A streamed run hands over what the process writes and its exit code, and a
// normal non-zero exit is a result and no error, as it is for Run.
func TestOSRunnerStreamWritesTheOutputAndReturnsTheExitCode(t *testing.T) {
	// Arrange
	var stdout, stderr strings.Builder

	// Act
	exit, err := (OSRunner{}).Stream(context.Background(), helperRequest(t.TempDir(), []string{"EXECX_HELPER=1", "EXECX_MODE=nonzero"}), &stdout, &stderr)

	// Assert
	if err != nil || exit != 7 || stderr.String() != "child stderr" || stdout.Len() != 0 {
		t.Errorf("Stream = %d, %v with stdout %q and stderr %q; want 7 and no error with child stderr on stderr alone", exit, err, stdout.String(), stderr.String())
	}
}

// A streamed run its context ended is ended with it and says so, with what
// the process wrote before then already handed over. The process holds until
// the test releases it, which it never does, so Stream returning before the
// process finished by itself proves Stream ended it.
func TestOSRunnerStreamEndsTheProcessWhenItsContextEnds(t *testing.T) {
	// Arrange
	marker := filepath.Join(t.TempDir(), "child")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelled := cancelOnceHeld(ctx, cancel, marker)
	var stdout, stderr strings.Builder
	request := helperRequest(t.TempDir(), []string{"EXECX_HELPER=1", "EXECX_MODE=answer-then-hold", "EXECX_MARKER=" + marker})
	request.KillTree = true

	// Act
	_, err := (OSRunner{}).Stream(ctx, request, &stdout, &stderr)
	_, releasedErr := os.Stat(marker + ".released")

	// Assert
	select {
	case <-cancelled:
	default:
		t.Fatalf("Stream returned before the process answered: %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context canceled", err)
	}
	if stdout.String() != "answered\n" {
		t.Errorf("stdout = %q, want what the process wrote before its context ended", stdout.String())
	}
	if !os.IsNotExist(releasedErr) {
		t.Errorf("Stream returned only once the process finished by itself (%v), want it ended when its context ended", releasedErr)
	}
}
