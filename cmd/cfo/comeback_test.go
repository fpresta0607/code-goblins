package main

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/onboarding"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// After a restart the supervisor brings the CFO back in native terminal cfo
// with an explicit resume of the conversation it last registered with, and
// never on a new conversation: one it cannot resume stays closed with the
// reason, for the board's Reopen. A CFO that already runs is left as it is.
func TestTheComebackResumesTheCFOOnItsOwnConversationAndNeverStartsAFreshOne(t *testing.T) {
	for _, tc := range []struct {
		name, conversation string
		isRunning          bool
		resumeEnds         bool
		wantArgs           []string
		wantErr            string
	}{
		{name: "on its conversation", conversation: "a1b2c3d4-session", wantArgs: []string{"--resume a1b2c3d4-session"}},
		{name: "not on a new one when the resumed terminal does not hold", conversation: "a1b2c3d4-session", resumeEnds: true, wantArgs: []string{"--resume a1b2c3d4-session"}, wantErr: "its conversation a1b2c3d4-session could not be resumed; Reopen on its bar starts it on a new one"},
		{name: "not at all when it registered no conversation", wantErr: "it registered no conversation of its own as Claude Code; Reopen on its bar starts it on a new one"},
		{name: "not again when it already runs", conversation: "a1b2c3d4-session", isRunning: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			noResumeWait(t)
			f := newSessionFixture(t)
			f.resumeEnds = tc.resumeEnds
			if tc.isRunning {
				f.nativeCFO = supervisor.NativeCFOTerminal
			}
			if err := os.WriteFile(cfoHarnessPath(f.home.State), []byte("claude\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.conversation != "" {
				recordConversation(t, f.home.State, "claude", tc.conversation, supervisor.NativeCFOTerminal)
			}

			// Act
			err := comebackCFO(t.Context(), f.home, f.runtime)

			// Assert
			if !slices.Equal(f.nativeArgs, tc.wantArgs) {
				t.Errorf("started the CFO with %q, want %q", f.nativeArgs, tc.wantArgs)
			}
			if got := errorText(err); got != tc.wantErr {
				t.Errorf("comebackCFO = %q, want %q", got, tc.wantErr)
			}
		})
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Each goblin the supervisor brings back after a restart comes back as goblins
// resume brings it, on its own conversation, and is told in one line that the
// machine restarted; the outcome tells the supervisor whether it came back,
// waits for room, runs already, stays as it was or needs a hand.
func TestTheComebackBringsEachGoblinBackToldTheMachineRestarted(t *testing.T) {
	// Arrange
	h := comebackHome(t, map[string][2]string{
		"claude-owned": {"claude", "native"},
		"still-runs":   {"codex", "native"},
		"paused":       {"codex", "native"},
		"no-room":      {"codex", "native"},
		"fails":        {"codex", "native"},
	}, map[string]string{"paused": "paused"})
	var requests []spawn.SwitchRequest
	refused := errors.New("live goblin cap reached")
	runtime := comebackRuntime([]string{"still-runs"}, nil, func(request spawn.SwitchRequest) (spawn.SwitchResult, error) {
		if request.ID == "fails" {
			return spawn.SwitchResult{}, errors.New("switch: validate harness codex: codex is not signed in")
		}
		return spawn.SwitchResult{Resumed: request.ResumeSession != ""}, nil
	}, &requests)
	outcomes := map[string]supervisor.GoblinComeback{}

	// Act
	for _, id := range []string{"claude-owned", "still-runs", "paused", "no-room", "fails"} {
		runtime.admitLaunch = func(home.Home) error { return nil }
		if id == "no-room" {
			runtime.admitLaunch = func(home.Home) error { return refused }
		}
		outcomes[id] = comebackGoblin(t.Context(), h, runtime, id)
	}

	// Assert
	want := map[string]supervisor.GoblinComeback{
		"claude-owned": {Outcome: supervisor.CameBack, Said: "back on its conversation"},
		"still-runs":   {Outcome: supervisor.AlreadyRuns},
		"paused":       {Outcome: supervisor.LeftAsItWas},
		"no-room":      {Outcome: supervisor.WaitsForRoom, Said: "live goblin cap reached"},
		"fails":        {Outcome: supervisor.DidNotComeBack, Said: "switch: validate harness codex: codex is not signed in"},
	}
	for id, outcome := range want {
		if outcomes[id] != outcome {
			t.Errorf("%s came back %+v, want %+v", id, outcomes[id], outcome)
		}
	}
	owned := slices.IndexFunc(requests, func(request spawn.SwitchRequest) bool { return request.ID == "claude-owned" })
	if owned < 0 || requests[owned].ResumeSession != "owned-conversation" || requests[owned].ResumeNote != restartNote || strings.Count(restartNote, "\n") != 0 {
		t.Errorf("claude-owned came back with %+v, want its own conversation and the one restart line", requests)
	}
}

func TestTheComebackReportsWhetherTheTerminalRunsAfterASwitchError(t *testing.T) {
	for _, isRunning := range []bool{false, true} {
		name := "stopped"
		if isRunning {
			name = "running"
		}
		t.Run(name, func(t *testing.T) {
			h := comebackHome(t, map[string][2]string{"alpha": {"codex", "native"}}, nil)
			var requests []spawn.SwitchRequest
			isTerminalRunning := false
			switchError := errors.New("instruction confirmation timed out")
			runtime := comebackRuntime(nil, nil, func(spawn.SwitchRequest) (spawn.SwitchResult, error) {
				isTerminalRunning = isRunning
				return spawn.SwitchResult{}, switchError
			}, &requests)
			runtime.nativeTerminalRuns = func(string, string) bool { return isTerminalRunning }

			result := comebackGoblin(t.Context(), h, runtime, "alpha")

			want := supervisor.DidNotComeBack
			if isRunning {
				want = supervisor.CameBack
			}
			if result.Outcome != want || result.Said != switchError.Error() || len(requests) != 1 {
				t.Fatalf("result %+v, switches %+v, want outcome %v retaining the switch error", result, requests, want)
			}
		})
	}
}

func TestCFOStartsWaitForTheLaunchLockAndRecheckTheRunningTerminal(t *testing.T) {
	for _, path := range []string{"goblins", "reopen", "comeback"} {
		for _, isOtherRunning := range []bool{false, true} {
			name := path + "/empty after wait"
			if isOtherRunning {
				name = path + "/running after wait"
			}
			t.Run(name, func(t *testing.T) {
				noResumeWait(t)
				fixture := newSessionFixture(t)
				if err := os.WriteFile(cfoHarnessPath(fixture.home.State), []byte("claude\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				recordConversation(t, fixture.home.State, "claude", "owned-session", supervisor.NativeCFOTerminal)
				if _, err := lock.AcquireExclusiveNamed(fixture.home.State, cfoLaunchLock); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { lock.ReleaseExclusiveNamed(fixture.home.State, cfoLaunchLock) })
				var isRunning atomic.Bool
				var starts atomic.Int32
				fixture.runtime.nativeTerminalRuns = func(string, string) bool { return isRunning.Load() }
				fixture.runtime.startNativeCFO = func(home.Home, string, string, []string) error {
					starts.Add(1)
					if _, err := lock.AcquireExclusiveNamed(fixture.home.State, cfoLaunchLock); !errors.Is(err, lock.ErrHeld) {
						t.Errorf("start did not hold the launch lock: %v", err)
					}
					isRunning.Store(true)
					return nil
				}
				finished := make(chan error, 1)

				go func() {
					switch path {
					case "goblins":
						_, _, err := ensureCFOSession(t.Context(), fixture.runtime, fixture.home, "claude", &onboarding.Checklist{Output: io.Discard, Plain: true})
						finished <- err
					case "reopen":
						finished <- reopenCFO(fixture.home, fixture.runtime.startNativeCFO, fixture.runtime.nativeTerminalRuns)
					case "comeback":
						finished <- comebackCFO(t.Context(), fixture.home, fixture.runtime)
					}
				}()
				select {
				case err := <-finished:
					t.Fatalf("start returned while another start held the lock: %v", err)
				case <-time.After(200 * time.Millisecond):
				}
				isRunning.Store(isOtherRunning)
				if err := lock.ReleaseExclusiveNamed(fixture.home.State, cfoLaunchLock); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-finished:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("start did not finish after the lock was released")
				}

				wantStarts := int32(1)
				if isOtherRunning {
					wantStarts = 0
				}
				if starts.Load() != wantStarts {
					t.Fatalf("started %d CFOs, want %d", starts.Load(), wantStarts)
				}
				if _, err := lock.AcquireExclusiveNamed(fixture.home.State, cfoLaunchLock); err != nil {
					t.Fatalf("start did not release the launch lock: %v", err)
				}
				lock.ReleaseExclusiveNamed(fixture.home.State, cfoLaunchLock)
			})
		}
	}
}

func TestCFOLaunchWaitRechecksTheCFOWhenItsDeadlineEnds(t *testing.T) {
	for _, isRunning := range []bool{false, true} {
		name := "no CFO yet"
		if isRunning {
			name = "CFO running"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newSessionFixture(t)
			holder, err := lock.AcquireExclusiveNamed(fixture.home.State, cfoLaunchLock)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { lock.ReleaseExclusiveNamed(fixture.home.State, cfoLaunchLock) })
			fixture.runtime.nativeTerminalRuns = func(string, string) bool { return isRunning }
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
			defer cancel()

			err = comebackCFO(ctx, fixture.home, fixture.runtime)

			if isRunning && err != nil || !isRunning && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("comeback = %v, CFO running %v, want the final running-state check", err, isRunning)
			}
			if len(fixture.nativeStarts) != 0 {
				t.Fatalf("started a CFO without the launch lock: %v", fixture.nativeStarts)
			}
			after, err := lock.ReadNamed(fixture.home.State, cfoLaunchLock)
			if err != nil || !after.Acquired.Equal(holder.Acquired) {
				t.Fatalf("changed another start's lock: %+v, %v", after, err)
			}
		})
	}
}
