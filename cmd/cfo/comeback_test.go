package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/onboarding"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// After a restart, and after the CFO's terminal ended while nobody asked it
// to stop, the supervisor brings the CFO back in native terminal cfo by the
// rule Restart the CFO keeps: on the conversation it last registered with,
// and on a new one when the resumed terminal does not hold or the
// conversation is past the size a CFO resumes, which then starts with no
// --resume. Its first instruction tells it why it is back, so nobody has to
// type into it. A CFO that already runs is left as it is.
func TestTheComebackBringsTheCFOBackOnItsConversationOrANewOneAndTellsItWhy(t *testing.T) {
	const why = "its terminal ended with exit code 1"
	for _, tc := range []struct {
		name         string
		isRunning    bool
		resumeEnds   bool
		endsSettling bool
		isTooLarge   bool
		isUnwritten  bool
		wantArgs     []string
		wantPrompts  []string
	}{
		{name: "on its conversation", wantArgs: []string{"--resume a1b2c3d4-session"}, wantPrompts: []string{cfoBackPrompt("claude", why, "")}},
		{name: "on a new one when the resumed terminal does not hold", resumeEnds: true, wantArgs: []string{"--resume a1b2c3d4-session", ""}, wantPrompts: []string{cfoBackPrompt("claude", why, ""), cfoBackPrompt("claude", why, "You start on a new conversation. Your conversation a1b2c3d4-session could not be resumed.")}},
		{name: "on a new one when the resumed terminal ends while its dialogs are answered", endsSettling: true, wantArgs: []string{"--resume a1b2c3d4-session", ""}, wantPrompts: []string{cfoBackPrompt("claude", why, ""), cfoBackPrompt("claude", why, "You start on a new conversation. Your conversation a1b2c3d4-session could not be resumed.")}},
		{name: "on a new one when Claude Code wrote no transcript for it", isUnwritten: true, wantArgs: []string{""}, wantPrompts: []string{cfoBackPrompt("claude", why, "You start on a new conversation. Claude Code has written no transcript for its conversation a1b2c3d4-session, which it writes when its first turn begins.")}},
		{name: "on a new one past the context a CFO resumes", isTooLarge: true, wantArgs: []string{""}, wantPrompts: []string{cfoBackPrompt("claude", why, "You start on a new conversation. Its last conversation holds 802K tokens of context, past the 400K a CFO resumes.")}},
		{name: "not again when it already runs", isRunning: true},
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
			recordConversation(t, f.home.State, "claude", "a1b2c3d4-session", supervisor.NativeCFOTerminal)
			userHome := t.TempDir()
			t.Setenv("USERPROFILE", userHome)
			if tc.isTooLarge {
				writeClaudeTranscript(t, f.home.Root, "a1b2c3d4-session", requestLine(6, 2_300, 800_000, 4))
			} else if !tc.isUnwritten {
				claudeTranscript(t, f.home.Root, "a1b2c3d4-session", 1<<10)
			}
			if tc.endsSettling {
				settles := 0
				f.runtime.settleCFO = func(context.Context, string, string) []string {
					settles++
					if settles == 1 {
						f.cfoTerminalRuns = false
					}
					return nil
				}
			}

			// Act
			err := comebackCFO(t.Context(), f.home, f.runtime, why)

			// Assert
			if err != nil {
				t.Fatalf("comebackCFO = %v, want the CFO back", err)
			}
			if !slices.Equal(f.nativeArgs, tc.wantArgs) {
				t.Errorf("started the CFO with %q, want %q", f.nativeArgs, tc.wantArgs)
			}
			if !slices.Equal(f.nativePrompts, tc.wantPrompts) {
				t.Errorf("told the CFO %q, want %q", f.nativePrompts, tc.wantPrompts)
			}
		})
	}
}

// After a restart a pi CFO comes back on its own session with pi --session,
// as a pi goblin does, once pi has written that session's file, and on a new
// one, told why, until it has.
func TestTheComebackResumesAPiCFOOnItsOwnSession(t *testing.T) {
	const why = "the machine restarted or its Windows sign-in ended, which ended its terminal"
	for _, tc := range []struct {
		name             string
		isSessionWritten bool
		wantArgs         []string
		wantPrompts      []string
	}{
		{name: "on its session", isSessionWritten: true, wantArgs: []string{"--session a1b2c3d4-session"}, wantPrompts: []string{cfoBackPrompt("pi", why, "")}},
		{name: "on a new one before pi wrote its session", wantArgs: []string{""}, wantPrompts: []string{cfoBackPrompt("pi", why, "You start on a new conversation. pi has written no file for its conversation a1b2c3d4-session, which it writes when its first reply ends.")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			noResumeWait(t)
			f := newSessionFixture(t)
			userHome := t.TempDir()
			t.Setenv("USERPROFILE", userHome)
			if tc.isSessionWritten {
				writePiSession(t, userHome, "a1b2c3d4-session")
			}
			if err := os.WriteFile(cfoHarnessPath(f.home.State), []byte("pi\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			recordConversation(t, f.home.State, "pi", "a1b2c3d4-session", supervisor.NativeCFOTerminal)

			// Act
			err := comebackCFO(t.Context(), f.home, f.runtime, why)

			// Assert
			if err != nil {
				t.Fatalf("comebackCFO = %v, want the CFO back", err)
			}
			if !slices.Equal(f.nativeArgs, tc.wantArgs) {
				t.Errorf("started the CFO with %q, want %q", f.nativeArgs, tc.wantArgs)
			}
			if !slices.Equal(f.nativePrompts, tc.wantPrompts) {
				t.Errorf("told the CFO %q, want %q", f.nativePrompts, tc.wantPrompts)
			}
		})
	}
}

// A CFO brought back whose terminal ends while its startup dialogs are
// answered did not come back, and the supervisor is told so.
func TestTheComebackSaysACFOThatEndedDuringStartupDidNotComeBack(t *testing.T) {
	// Arrange
	noResumeWait(t)
	f := newSessionFixture(t)
	recordConversation(t, f.home.State, "claude", "a1b2c3d4-session", supervisor.NativeCFOTerminal)
	f.runtime.settleCFO = func(context.Context, string, string) []string {
		f.cfoTerminalRuns = false
		return nil
	}

	// Act
	err := comebackCFO(t.Context(), f.home, f.runtime, "its terminal ended with exit code 1")

	// Assert
	if got := errorText(err); got != "its native terminal ended during startup" {
		t.Fatalf("comebackCFO = %q, want the ended terminal said", got)
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
	refused := errors.New("only 4.6 GB of memory is free")
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
		"no-room":      {Outcome: supervisor.WaitsForRoom, Said: "only 4.6 GB of memory is free"},
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

// A goblin whose resume the restart cut short comes back through a resume,
// which finishes what its record left, told the machine restarted, and never
// through a bare switch that would leave its record reading resuming. A
// failed pause took hold once the terminal ended, so that goblin stays
// paused, and a failed resume or stop is left as it was and named.
func TestTheComebackFinishesTheResumeTheRestartCutShort(t *testing.T) {
	// Arrange
	h := comebackHome(t, map[string][2]string{
		"resume-cut-short": {"claude", "native"},
		"resume-failed":    {"claude", "native"},
		"pause-failed":     {"codex", "native"},
		"stop-failed":      {"codex", "native"},
		"no-room":          {"codex", "native"},
	}, nil)
	for _, left := range [][3]string{{"resume-cut-short", "resume", "resuming"}, {"resume-failed", "resume", "failed"}, {"pause-failed", "pause", "failed"}, {"stop-failed", "stop", "failed"}, {"no-room", "resume", "resuming"}} {
		meta, err := state.ReadTaskMeta(h.State, left[0])
		if err != nil {
			t.Fatal(err)
		}
		operation := "operation-" + left[0]
		if left[1] == "resume" {
			meta.ResumeOperation = operation
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
		}
		if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: operation, Action: left[1], Phase: left[2], Started: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	var switches []spawn.SwitchRequest
	runtime := comebackRuntime(nil, nil, func(spawn.SwitchRequest) (spawn.SwitchResult, error) { return spawn.SwitchResult{}, nil }, &switches)
	var resumes []lifecycle.Request
	runtime.taskLifecycle = func(_ context.Context, _ home.Home, request lifecycle.Request, _ string) (state.Lifecycle, error) {
		resumes = append(resumes, request)
		if request.ID == "no-room" {
			return state.Lifecycle{}, fmt.Errorf("%w: only 4.6 GB of memory is free", state.ErrNoRoom)
		}
		return state.Lifecycle{ID: request.ID, Phase: "running"}, nil
	}
	outcomes := map[string]supervisor.GoblinComeback{}

	// Act
	for _, id := range []string{"resume-cut-short", "resume-failed", "pause-failed", "stop-failed", "no-room"} {
		outcomes[id] = comebackGoblin(t.Context(), h, runtime, id)
	}

	// Assert
	if outcomes["resume-cut-short"].Outcome != supervisor.CameBack {
		t.Errorf("resume-cut-short came back %+v, want back", outcomes["resume-cut-short"])
	}
	if outcomes["pause-failed"].Outcome != supervisor.LeftAsItWas {
		t.Errorf("pause-failed came back %+v, want it left paused", outcomes["pause-failed"])
	}
	for _, failed := range [][2]string{{"resume-failed", "its resume failed"}, {"stop-failed", "its stop failed"}} {
		if outcomes[failed[0]].Outcome != supervisor.DidNotComeBack || !strings.Contains(outcomes[failed[0]].Said, failed[1]) {
			t.Errorf("%s came back %+v, want it left as it was with %q named", failed[0], outcomes[failed[0]], failed[1])
		}
	}
	if outcomes["no-room"].Outcome != supervisor.WaitsForRoom || !strings.Contains(outcomes["no-room"].Said, "only 4.6 GB of memory is free") {
		t.Errorf("no-room came back %+v, want it waiting for room", outcomes["no-room"])
	}
	if len(switches) != 0 {
		t.Errorf("switches %+v, want none: a resume finishes what the record left", switches)
	}
	if len(resumes) != 2 {
		t.Fatalf("resumes %+v, want one for each resume the restart cut short", resumes)
	}
	for _, request := range resumes {
		if request.Action != "resume" || request.Generation != "s1" || request.Operation == "" || !request.IsWatched || request.ResumeNote != restartNote {
			t.Errorf("resume %+v, want one of the task's generation, read by the comeback, told the machine restarted", request)
		}
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
	for _, path := range []string{"goblins", "reopen", "comeback", "app"} {
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
				if _, err := lock.AcquireExclusiveNamed(fixture.home.State, supervisor.CFOLaunchLock); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { lock.ReleaseExclusiveNamed(fixture.home.State, supervisor.CFOLaunchLock) })
				var isRunning atomic.Bool
				var starts atomic.Int32
				fixture.runtime.nativeTerminalRuns = func(string, string) bool { return isRunning.Load() }
				fixture.runtime.startNativeCFO = func(home.Home, string, string, []string, string) error {
					starts.Add(1)
					if _, err := lock.AcquireExclusiveNamed(fixture.home.State, supervisor.CFOLaunchLock); !errors.Is(err, lock.ErrHeld) {
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
						finished <- reopenCFO(fixture.home, "the Overlord reopened it from the board", fixture.runtime.startNativeCFO, fixture.runtime.nativeTerminalRuns)
					case "comeback":
						finished <- comebackCFO(t.Context(), fixture.home, fixture.runtime, "its terminal ended with exit code 1")
					case "app":
						exit, _, stderr := fixture.launch("--window", "--locate")
						if exit != 0 || stderr != "" {
							finished <- errors.New(stderr)
						} else {
							finished <- nil
						}
					}
				}()
				select {
				case err := <-finished:
					t.Fatalf("start returned while another start held the lock: %v", err)
				// stall-accepted: nothing is meant to finish while the lock is held, and the start's own wait for it is 30 s, so a stall only makes this wait longer.
				case <-time.After(200 * time.Millisecond):
				}
				isRunning.Store(isOtherRunning)
				if err := lock.ReleaseExclusiveNamed(fixture.home.State, supervisor.CFOLaunchLock); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-finished:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(time.Minute):
					t.Fatal("start did not finish after the lock was released")
				}

				wantStarts := int32(1)
				if isOtherRunning {
					wantStarts = 0
				}
				if starts.Load() != wantStarts {
					t.Fatalf("started %d CFOs, want %d", starts.Load(), wantStarts)
				}
				if _, err := lock.AcquireExclusiveNamed(fixture.home.State, supervisor.CFOLaunchLock); err != nil {
					t.Fatalf("start did not release the launch lock: %v", err)
				}
				lock.ReleaseExclusiveNamed(fixture.home.State, supervisor.CFOLaunchLock)
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
			holder, err := lock.AcquireExclusiveNamed(fixture.home.State, supervisor.CFOLaunchLock)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { lock.ReleaseExclusiveNamed(fixture.home.State, supervisor.CFOLaunchLock) })
			fixture.runtime.nativeTerminalRuns = func(string, string) bool { return isRunning }
			// stall-accepted: the test means this context to run out while another start holds the lock.
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
			defer cancel()

			err = comebackCFO(ctx, fixture.home, fixture.runtime, "its terminal ended with exit code 1")

			if isRunning && err != nil || !isRunning && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("comeback = %v, CFO running %v, want the final running-state check", err, isRunning)
			}
			if len(fixture.nativeStarts) != 0 {
				t.Fatalf("started a CFO without the launch lock: %v", fixture.nativeStarts)
			}
			after, err := lock.ReadNamed(fixture.home.State, supervisor.CFOLaunchLock)
			if err != nil || !after.Acquired.Equal(holder.Acquired) {
				t.Fatalf("changed another start's lock: %+v, %v", after, err)
			}
		})
	}
}
