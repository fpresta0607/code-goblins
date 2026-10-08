package supervisor

import (
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// This process runs in the sign-in SignInBegan reads, so the sign-in began after
// the machine started and before this process did.
func TestTheSignInBeganBetweenTheMachinesStartAndThisProcesss(t *testing.T) {
	// Act
	signedIn, err := SignInBegan()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &created, &exited, &kernel, &user); err != nil {
		t.Fatal(err)
	}
	started := time.Unix(0, created.Nanoseconds())
	booted := time.Now().Add(-windows.DurationSinceBoot())
	if signedIn.Before(booted.Add(-time.Minute)) || signedIn.After(started) {
		t.Fatalf("signed in %s, want between the machine's start %s and this process's %s", signedIn, booted, started)
	}
}
