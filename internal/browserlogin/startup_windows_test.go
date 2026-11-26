package browserlogin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
	"time"
)

func TestCancelledStartDoesNotTouchTheProfileOrStartAProcess(t *testing.T) {
	profile := t.TempDir()
	port := filepath.Join(profile, activePortFile)
	if err := os.WriteFile(port, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	process, err := Start(ctx, Launch{Executable: "must-not-start.exe", Profile: profile, Headless: true})

	data, readErr := os.ReadFile(port)
	if !errors.Is(err, context.Canceled) || process.PID != 0 || readErr != nil || string(data) != "untouched" {
		t.Fatalf("cancelled launch = %v, pid %d, profile read %v, want no side effects", err, process.PID, readErr)
	}
}

func TestFailedReadinessRetainsTheProcessWhenCleanupIsUnconfirmed(t *testing.T) {
	executable, err := Executable()
	if err != nil {
		t.Fatal(err)
	}
	readinessFailure := errors.New("readiness failed")
	cleanupFailure := errors.New("cleanup unconfirmed")
	var owned Process
	await := func(context.Context, *devToolsPipe) error { return readinessFailure }
	cleanup := func(process Process) error {
		owned = process
		return cleanupFailure
	}

	process, err := start(context.Background(), Launch{Executable: executable, Profile: t.TempDir(), Headless: true}, await, cleanup)
	defer func() {
		if owned.PID != 0 {
			if err := owned.close(); err != nil {
				t.Errorf("end owned startup fixture: %v", err)
			}
		}
	}()

	if !errors.Is(err, readinessFailure) || !errors.Is(err, cleanupFailure) || process.PID == 0 || process.PID != owned.PID || process.job != owned.job {
		t.Fatalf("failed readiness = %v, returned pid %d, owned pid %d, want retained ownership", err, process.PID, owned.PID)
	}
}

// A start whose wait for DevTools runs out says what its Chrome did in that
// time and what Chrome logged, since a bare deadline cannot tell a Chrome
// that worked slowly from one that sat blocked (#2135).
func TestAStartWhoseWaitRunsOutSaysWhatItsChromeDidAndWhatItLogged(t *testing.T) {
	// Arrange
	executable, err := Executable()
	if err != nil {
		t.Fatal(err)
	}
	canary := "startup-log-canary"
	page := "data:text/html,<script>console.log('" + canary + "'); document.title = 'logged'</script>"
	await := func(ctx context.Context, pipe *devToolsPipe) error {
		for ctx.Err() == nil {
			var answer struct {
				Targets []struct{ Title string } `json:"targetInfos"`
			}
			if err := pipe.call(ctx, "Target.getTargets", nil, &answer); err != nil {
				return err
			}
			if slices.ContainsFunc(answer.Targets, func(target struct{ Title string }) bool { return target.Title == "logged" }) {
				return context.DeadlineExceeded
			}
			time.Sleep(50 * time.Millisecond)
		}
		return ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Act
	process, err := start(ctx, Launch{Executable: executable, Profile: t.TempDir(), Headless: true, Page: page}, await, Process.close)

	// Assert
	if !errors.Is(err, context.DeadlineExceeded) || process.PID != 0 {
		t.Fatalf("start = pid %d, %v, want a closed Chrome and the wait's deadline", process.PID, err)
	}
	said := err.Error()
	for _, want := range []*regexp.Regexp{
		regexp.MustCompile(`DevTools wait ended [^ ]+ after it was resumed`),
		regexp.MustCompile(`[1-9][0-9.]*(ms|s) of processor time`),
		regexp.MustCompile(`[1-9][0-9]* page faults`),
		regexp.MustCompile(`[1-9][0-9]* messages had come back on its pipe`),
		regexp.MustCompile(`its log: ".*` + canary),
	} {
		if !want.MatchString(said) {
			t.Errorf("the failed start says %q, want it to match %s", said, want)
		}
	}
}

func TestCancellationDuringReadinessClosesBeforeReturning(t *testing.T) {
	executable, err := Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var owned Process
	await := func(context.Context, *devToolsPipe) error {
		cancel()
		return nil
	}
	cleanup := func(process Process) error {
		owned = process
		return process.close()
	}

	process, err := start(ctx, Launch{Executable: executable, Profile: t.TempDir(), Headless: true}, await, cleanup)

	if !errors.Is(err, context.Canceled) || process.PID != 0 || owned.PID == 0 || Running(owned) {
		t.Fatalf("cancelled readiness = %v, returned pid %d, owned pid %d, want confirmed closure", err, process.PID, owned.PID)
	}
}
