package supervisor

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
)

// updateCheckEnds waits until service's update check of Claude Code no longer
// runs or reads, and returns what the board then knows of it.
func updateCheckEnds(t *testing.T, service *Service) UpdateCheck {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		service.mu.Lock()
		check := service.updateChecks["claude"]
		service.mu.Unlock()
		if !check.IsRunning && !check.isReading && !check.read.IsZero() {
			return check
		}
		if time.Now().After(deadline) {
			t.Fatalf("the update check never ended: %+v", check)
		}
	}
}

// The Overlord, 2026-11-10: "the Auto-update failed notice needs a reason and
// a Retry" (#2333). Retry runs the harness's own update check outside every
// session: nothing is queued for a conversation, the board shows the check
// running and then how it ended, the reason when it failed, and a check that
// succeeded with the install the newest takes the notice away.
func TestRetryRunsTheHarnesssUpdateCheckOutsideTheSessionAndSaysHowItEnded(t *testing.T) {
	for _, test := range []struct {
		name        string
		said        string
		failure     error
		isCurrent   bool
		want        UpdateCheck
		wantVersion bool
	}{
		{name: "a check that succeeds with the install the newest", said: "Claude Code is up to date (2.1.322)", isCurrent: true,
			want: UpdateCheck{IsCurrent: true, Result: "Claude Code is up to date (2.1.322)"}, wantVersion: true},
		{name: "a check that fails says why", failure: errors.New("Error: Failed to install update: EPERM, operation not permitted"),
			want: UpdateCheck{HasFailed: true, Result: "Error: Failed to install update: EPERM, operation not permitted"}},
		{name: "a check that says it succeeded over an install that is not the newest", said: "Successfully updated", isCurrent: false,
			want: UpdateCheck{Result: "Successfully updated"}, wantVersion: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			release := make(chan struct{})
			var runs, reads atomic.Int32
			service := &Service{Store: store, Instance: "instance-1", Options: Options{
				UpdateCheck: func(context.Context, harness.Kind) (string, error) {
					runs.Add(1)
					<-release
					return test.said, test.failure
				},
				HarnessCurrent: func(context.Context, harness.Kind) (bool, error) {
					reads.Add(1)
					return test.isCurrent, nil
				},
			}}
			board := NewHTTP(service, "board.local", nil)

			// Act
			pressed := boardRequest(board, "POST", "/api/harness/update-check", `{"harness":"claude"}`)
			again := boardRequest(board, "POST", "/api/harness/update-check", `{"harness":"claude"}`)
			running := service.updateCheckView()["claude"]
			close(release)
			ended := updateCheckEnds(t, service)

			// Assert
			if pressed.Code != 202 || again.Code != 202 || !running.IsRunning || runs.Load() != 1 {
				t.Errorf("Retry = %d %s, again = %d, running = %+v, runs = %d; want one check running", pressed.Code, pressed.Body, again.Code, running, runs.Load())
			}
			if ended.IsRunning || ended.HasFailed != test.want.HasFailed || ended.IsCurrent != test.want.IsCurrent || ended.Result != test.want.Result {
				t.Errorf("the check ended as %+v, want %+v", ended, test.want)
			}
			if (reads.Load() == 1) != test.wantVersion {
				t.Errorf("the install was read against its publisher's newest %d times, want it read only after a check that succeeded", reads.Load())
			}
			if actions := store.Snapshot().Actions; len(actions) != 0 {
				t.Errorf("actions = %+v; want nothing queued for any conversation", actions)
			}
		})
	}
}

// A harness the board has no update check for is refused, and nothing runs.
func TestRetryIsRefusedForAHarnessWithNoUpdateCheck(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	service := &Service{Store: store, Instance: "instance-1", Options: Options{
		UpdateCheck: func(context.Context, harness.Kind) (string, error) {
			t.Error("a check ran")
			return "", nil
		},
	}}

	// Act
	pressed := boardRequest(NewHTTP(service, "board.local", nil), "POST", "/api/harness/update-check", `{"harness":"notepad"}`)

	// Assert
	if pressed.Code != 409 || !strings.Contains(pressed.Body.String(), "notepad has no update check") {
		t.Errorf("Retry = %d %s; want it refused by name", pressed.Code, pressed.Body)
	}
}

// #2333: the notice "goes away once a later check succeeds or the installed
// version is current, with no restart". A session started before an update
// keeps drawing its notice of a failed one, so each screen that shows it has
// the install read against its publisher's newest, once for as long as that
// read stands, and the board knows it out of date while the install is.
func TestANoticeOfAFailedUpdateIsOutOfDateWhileTheInstallIsTheNewest(t *testing.T) {
	for _, test := range []struct {
		name      string
		isCurrent bool
		readErr   error
		want      bool
	}{
		{name: "the install is the newest", isCurrent: true, want: true},
		{name: "a newer one is offered", isCurrent: false, want: false},
		{name: "the publisher cannot be read", isCurrent: true, readErr: errors.New("no answer"), want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			var reads atomic.Int32
			service := &Service{Store: store, Options: Options{HarnessCurrent: func(context.Context, harness.Kind) (bool, error) {
				reads.Add(1)
				return test.isCurrent, test.readErr
			}}}

			// Act
			service.noticeUpdateFailure(harness.Claude)
			first := updateCheckEnds(t, service)
			service.noticeUpdateFailure(harness.Claude)
			second := updateCheckEnds(t, service)

			// Assert
			if first.IsCurrent != test.want || second.IsCurrent != test.want {
				t.Errorf("current = %t, then %t; want %t", first.IsCurrent, second.IsCurrent, test.want)
			}
			if reads.Load() != 1 {
				t.Errorf("the install was read %d times for two screens in a row, want once while that read stands", reads.Load())
			}
		})
	}
}

// What a check ends by saying is its last line that holds words, without the
// colours a program draws them in.
func TestAnUpdateChecksResultIsItsLastLineOfWordsWithoutColours(t *testing.T) {
	for _, test := range []struct{ name, output, want string }{
		{name: "the last of several lines", output: "Current version: 2.1.321\r\nChecking for updates...\r\nClaude Code is up to date (2.1.322)\r\n\r\n", want: "Claude Code is up to date (2.1.322)"},
		{name: "coloured words", output: "\x1b[31mError: Failed to install update\x1b[0m\n", want: "Error: Failed to install update"},
		{name: "no words", output: "\r\n \n", want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			said := lastWords(test.output)

			// Assert
			if said != test.want {
				t.Errorf("lastWords = %q, want %q", said, test.want)
			}
		})
	}
}
