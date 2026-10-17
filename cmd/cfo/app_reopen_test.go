package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

func TestAppLaunchReopensTheConfiguredCFOAndKeepsTheBoardReachable(t *testing.T) {
	for _, arguments := range [][]string{{"--window", "--locate"}, {"--window"}, {"--window", "--background"}, {"--board"}} {
		for _, testCase := range []struct {
			name           string
			agent          string
			conversation   string
			isRunning      bool
			isStarting     bool
			isNative       bool
			isResumeLost   bool
			isStartFailure bool
			isUnreadable   bool
			wantArguments  []string
			wantError      string
		}{
			{name: "closed Claude CFO", agent: "claude", conversation: "saved-session", wantArguments: []string{"--resume saved-session"}},
			{name: "closed Codex CFO", agent: "codex", conversation: "saved-session", wantArguments: []string{"resume saved-session"}},
			{name: "closed pi CFO", agent: "pi", conversation: "saved-session", wantArguments: []string{""}},
			{name: "configured without registration", agent: "codex", wantArguments: []string{""}},
			{name: "running CFO", agent: "codex", isRunning: true},
			{name: "native CFO", agent: "codex", isNative: true},
			{name: "starting CFO", agent: "codex", isStarting: true},
			{name: "fresh home"},
			{name: "failed start", agent: "codex", isStartFailure: true, wantError: "fixture start failed"},
			{name: "invalid saved harness", agent: "unknown", wantError: "not claude, codex or pi"},
			{name: "unreadable saved harness", agent: "codex", isUnreadable: true, wantError: "cfo-harness"},
			{name: "lost conversation", agent: "codex", conversation: "saved-session", isResumeLost: true, wantArguments: []string{"resume saved-session", ""}},
		} {
			t.Run(strings.Join(arguments, " ")+"/"+testCase.name, func(t *testing.T) {
				// Arrange
				noResumeWait(t)
				fixture := newSessionFixture(t)
				fixture.window = nil
				fixture.cfoLive = testCase.isRunning
				fixture.cfoTerminalRuns = testCase.isStarting
				fixture.resumeEnds = testCase.isResumeLost
				if testCase.isNative {
					fixture.nativeCFO = supervisor.NativeCFOTerminal
				}
				if testCase.isUnreadable {
					if err := os.Mkdir(cfoHarnessPath(fixture.home.State), 0o700); err != nil {
						t.Fatal(err)
					}
				} else if testCase.agent != "" {
					if err := os.WriteFile(cfoHarnessPath(fixture.home.State), []byte(testCase.agent+"\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if testCase.conversation != "" {
					recordConversation(t, fixture.home.State, testCase.agent, testCase.conversation, supervisor.NativeCFOTerminal)
					claudeTranscript(t, fixture.home.Root, testCase.conversation, 1<<10)
				}
				if testCase.isStartFailure {
					fixture.runtime.startNativeCFO = func(home.Home, string, string, []string, string) error {
						return errors.New("fixture start failed")
					}
				}
				board, err := readBoardRecord(fixture.home.State)
				if err != nil {
					t.Fatal(err)
				}

				// Act
				exit, stdout, stderr := fixture.launch(arguments...)

				// Assert
				if exit != 0 || !slices.Equal(fixture.nativeArgs, testCase.wantArguments) {
					t.Errorf("exit=%d starts=%q stderr=%q, want starts=%q and a reachable board", exit, fixture.nativeArgs, stderr, testCase.wantArguments)
				}
				if testCase.wantError == "" && stderr != "" || !strings.Contains(stderr, testCase.wantError) {
					t.Errorf("stderr=%q, want error %q", stderr, testCase.wantError)
				}
				for index, agent := range fixture.harnesses {
					if agent != testCase.agent || fixture.nativeStarts[index] != fixture.home.Root {
						t.Errorf("started %s in %s, want saved %s in %s", agent, fixture.nativeStarts[index], testCase.agent, fixture.home.Root)
					}
				}
				if len(fixture.setups) != 0 || len(fixture.screens) != 0 || len(fixture.attached) != 0 || len(fixture.nativeAttached) != 0 {
					t.Error("app opening entered quick start or attached the CFO to this terminal")
				}
				saved, readError := os.ReadFile(cfoHarnessPath(fixture.home.State))
				if testCase.isUnreadable {
					info, err := os.Stat(cfoHarnessPath(fixture.home.State))
					if err != nil || !info.IsDir() {
						t.Fatalf("unreadable harness was replaced: %v", err)
					}
				} else if testCase.agent == "" {
					if !errors.Is(readError, os.ErrNotExist) {
						t.Errorf("fresh home gained a harness: %q, %v", saved, readError)
					}
				} else if readError != nil || string(saved) != testCase.agent+"\n" {
					t.Errorf("saved harness=%q, error=%v, want unchanged %s", saved, readError, testCase.agent)
				}
				switch {
				case slices.Contains(arguments, "--locate"):
					if stdout != board.URL+"\n"+fixture.home.State+"\n" || len(fixture.windows) != 0 || len(fixture.opened) != 0 {
						t.Errorf("locator stdout=%q windows=%q browser=%q, want only its two-line protocol", stdout, fixture.windows, fixture.opened)
					}
				case arguments[0] == "--board":
					if !slices.Equal(fixture.opened, []string{board.URL}) || len(fixture.windows) != 0 {
						t.Errorf("browser=%q windows=%q, want the board in the browser", fixture.opened, fixture.windows)
					}
				default:
					want := fmt.Sprintf("%s background=%v", board.URL, slices.Contains(arguments, "--background"))
					if !slices.Equal(fixture.windows, []string{want}) || len(fixture.opened) != 0 {
						t.Errorf("windows=%q browser=%q, want window %s", fixture.windows, fixture.opened, want)
					}
				}
			})
		}
	}
}

func TestAppLaunchDoesNotReopenBeforeTheBoardStarts(t *testing.T) {
	for _, arguments := range [][]string{{"--window", "--locate"}, {"--window"}, {"--window", "--background"}, {"--board"}} {
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			// Arrange
			fixture := newLauncherFixture(t, func(home.Home) (<-chan struct{}, error) {
				return nil, errors.New("fixture board failed")
			})
			fixture.cfoLive = false
			if err := os.WriteFile(cfoHarnessPath(fixture.home.State), []byte("codex\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			exit, stdout, stderr := fixture.launch(arguments...)

			// Assert
			if exit != 1 || !strings.Contains(stderr, "fixture board failed") || stdout != "" || len(fixture.nativeStarts) != 0 || len(fixture.windows) != 0 || len(fixture.opened) != 0 {
				t.Fatalf("exit=%d stdout=%q stderr=%q CFOs=%q windows=%q browser=%q, want only the board failure", exit, stdout, stderr, fixture.nativeStarts, fixture.windows, fixture.opened)
			}
		})
	}
}
