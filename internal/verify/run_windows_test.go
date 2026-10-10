package verify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/standin"
)

// TestTierFixture is not a test: it is the processes
// TestATierCommandCutOffEndsWhatItStarted runs, by the role VERIFY_TIER_ROLE
// names. The command starts a process of its own, reports that process's pid
// to the file VERIFY_TIER_STARTED names and waits, as a test runner waits
// for the tests it started. What it started only waits.
func TestTierFixture(t *testing.T) {
	switch os.Getenv("VERIFY_TIER_ROLE") {
	case "command":
		started := execx.Command(os.Args[0], "-test.run=^TestTierFixture$")
		started.Env = append(os.Environ(), "VERIFY_TIER_ROLE=started")
		if err := started.Start(); err != nil {
			t.Fatal(err)
		}
		// Written whole and then named, so the test never reads half a pid.
		report := os.Getenv("VERIFY_TIER_STARTED")
		if err := os.WriteFile(report+".tmp", []byte(strconv.Itoa(started.Process.Pid)), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(report+".tmp", report); err != nil {
			t.Fatal(err)
		}
	case "started":
	default:
		return
	}
	time.Sleep(time.Minute)
}

// A tier's command is a test runner, and a test runner starts the tests. A
// run cut off, at its limit or by its caller, ended the command alone:
// `cfo verify` at a 90-second limit would have ended `cfo gate test` and left
// the go vet and go test it had started running, with nothing left to end
// them. The limit and the caller reach the command through the same context,
// so the caller's cut stands in for the limit here: it can wait until the
// command has started its process, however slowly a loaded machine starts
// one, where a fixed limit could run out first.
func TestATierCommandCutOffEndsWhatItStarted(t *testing.T) {
	// Arrange
	report := filepath.Join(t.TempDir(), "started")
	t.Setenv("VERIFY_TIER_ROLE", "command")
	t.Setenv("VERIFY_TIER_STARTED", report)
	ctx, cut := context.WithCancel(context.Background())
	defer cut()
	ended := make(chan error, 1)
	go func() {
		_, err := Runner{Commands: execx.OSRunner{}}.Run(ctx, []project.Command{{os.Args[0], "-test.run=^TestTierFixture$"}}, "fast", "task", "commit", "", time.Minute)
		ended <- err
	}()
	var started windows.Handle
	for deadline := time.Now().Add(time.Minute); started == 0; time.Sleep(20 * time.Millisecond) {
		if data, err := os.ReadFile(report); err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatalf("the command reported %q as the pid of what it started", data)
			}
			// It waits a minute and nothing has ended it, so the pid still
			// names it, and holding it keeps the pid its own from here on.
			started = standin.Hold(t, pid)
			break
		}
		select {
		case err := <-ended:
			t.Fatalf("the command ended before it reported what it started: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the command never reported what it started")
		}
	}

	// Act
	cut()
	err := <-ended

	// Assert
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Run returned %v, want the cut it was given", err)
	}
	if event, _ := windows.WaitForSingleObject(started, 10000); event == uint32(windows.WAIT_TIMEOUT) {
		t.Error("the process the command started still runs 10 seconds after the command was cut off")
	}
}
