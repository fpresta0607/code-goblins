package execx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	case "short-sleep":
		time.Sleep(200 * time.Millisecond)
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
		if err := os.WriteFile(marker, []byte("alive"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(9)
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

func TestOSRunnerReturnsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result, err := (OSRunner{}).Run(ctx, helperRequest(t.TempDir(), []string{"EXECX_HELPER=1", "EXECX_MODE=sleep"}))
	if err == nil {
		t.Fatal("Run returned nil error after context cancellation")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want context deadline exceeded", err)
	}
	if len(result.Stdout) != 0 || len(result.Stderr) != 0 || result.ExitCode != 0 {
		t.Errorf("result = %+v, want zero result on cancellation", result)
	}
}

func TestOSRunnerStartsWithoutWaitingForChildExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := time.Now()
	err := (OSRunner{}).Start(ctx, helperRequest(t.TempDir(), []string{"EXECX_HELPER=1", "EXECX_MODE=short-sleep"}))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Start blocked for %s, want non-blocking child launch", elapsed)
	}
	time.Sleep(300 * time.Millisecond)
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

	deadline := time.Now().Add(2 * time.Second)
	for {
		if data, err := os.ReadFile(marker); err == nil {
			switch string(data) {
			case "alive":
				// Let the helper return and its asynchronous reaper finish before
				// go test removes the Windows test executable.
				time.Sleep(100 * time.Millisecond)
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
