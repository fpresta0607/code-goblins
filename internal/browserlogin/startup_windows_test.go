package browserlogin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
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
	await := func(context.Context, string) (string, error) { return "", readinessFailure }
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

func TestCancellationDuringReadinessClosesBeforeReturning(t *testing.T) {
	executable, err := Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var owned Process
	await := func(context.Context, string) (string, error) {
		cancel()
		return "http://127.0.0.1:1", nil
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
