package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// writeCards writes the board's record of cards into stateDir, as the
// supervisor saves it.
func writeCards(t *testing.T, stateDir string, cards ...supervisor.CredentialRequest) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"credentials": cards})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, ".supervisor.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A wait prints each plain value he entered for the goblin to use, and each
// secret by its name alone: the value stays in the vault.
func TestWaitingOnACardPrintsPlainValuesAndOnlyTheNamesOfSecrets(t *testing.T) {
	// Arrange
	h := home.Home{State: t.TempDir()}
	writeCards(t, h.State, supervisor.CredentialRequest{ID: "cred-0123456789abcdef", Project: "throwaway", State: "saved", Names: []string{"STRIPE_SECRET_KEY"}, Plain: []string{"ACCOUNT_EMAIL"}, Values: map[string]string{"ACCOUNT_EMAIL": "shop-owner@example.com"}, Saved: []string{"STRIPE_SECRET_KEY"}, Received: []string{"ACCOUNT_EMAIL"}})
	var stdout, stderr strings.Builder

	// Act
	code := waitForCard(context.Background(), h, "cred-0123456789abcdef", "task-1", commandRuntime{}, &stdout, &stderr)

	// Assert
	if code != 0 {
		t.Fatalf("exit = %d, stderr %s", code, stderr.String())
	}
	for _, want := range []string{"card cred-0123456789abcdef is done", "ACCOUNT_EMAIL=shop-owner@example.com", "STRIPE_SECRET_KEY is in the throwaway vault"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	}
}

// A card he dismissed, or one that closed without the step, ends the wait
// saying so, with its own exit code for a dismissal.
func TestWaitingOnACardThatClosedWithoutTheStep(t *testing.T) {
	for _, test := range []struct {
		state, want string
		code        int
	}{
		{"dismissed", "go on without it", cardDismissed},
		{"answered", "he answered instead: use the fleet bot", cardAnswered},
		{"withdrawn", "was withdrawn", 1},
		{"expired", "was expired", 1},
	} {
		t.Run(test.state, func(t *testing.T) {
			// Arrange
			h := home.Home{State: t.TempDir()}
			reason := ""
			if test.state == "answered" {
				reason = "use the fleet bot's token"
			}
			writeCards(t, h.State, supervisor.CredentialRequest{ID: "cred-0123456789abcdef", Project: "throwaway", State: test.state, Reason: reason, Link: "https://example.com/signup"})
			var stdout, stderr strings.Builder

			// Act
			code := waitForCard(context.Background(), h, "cred-0123456789abcdef", "task-1", commandRuntime{}, &stdout, &stderr)

			// Assert
			if code != test.code || !strings.Contains(stdout.String(), test.want) {
				t.Fatalf("exit %d, stdout %q; want %d saying %q", code, stdout.String(), test.code, test.want)
			}
		})
	}
}

func TestWaitingOnAGrantCardReportsTheServiceAndResourceNotice(t *testing.T) {
	// Arrange
	h := home.Home{State: t.TempDir()}
	writeCards(t, h.State, supervisor.CredentialRequest{ID: "cred-0123456789abcdef", Project: "throwaway", Task: "task-1", GrantService: "sample", State: "granted"})
	var stdout, stderr strings.Builder

	// Act
	code := waitForCard(context.Background(), h, "cred-0123456789abcdef", "task-1", commandRuntime{}, &stdout, &stderr)

	// Assert
	if code != 0 || !strings.Contains(stdout.String(), "granted sample to task-1; re-source your auth.ps1") {
		t.Fatalf("wait = %d %s %s", code, stdout.String(), stderr.String())
	}
}

// His press of the window button shows the goblin's own browser session at
// the card's page, on a profile of its task's, and tells the board. Once the
// card closes, the session runs headless again on that same profile, so it
// keeps the sign-in he made there.
func TestWaitingOnACardShowsTheWindowAtHisPressAndHidesItAfter(t *testing.T) {
	// Arrange
	h := home.Home{State: t.TempDir()}
	scratch := t.TempDir()
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "task-1", TaskTmp: scratch}); err != nil {
		t.Fatal(err)
	}
	card := supervisor.CredentialRequest{ID: "cred-0123456789abcdef", Task: "task-1", Project: "throwaway", State: "open", Window: true, WindowState: "requested", Link: "https://example.com/login"}
	writeCards(t, h.State, card)
	type shown struct {
		profile, page string
		isHeaded      bool
	}
	var shows []shown
	var steps []supervisor.CredentialStep
	runtime := commandRuntime{
		showBrowser: func(_ context.Context, profile, page string, isHeaded bool) error {
			shows = append(shows, shown{profile, page, isHeaded})
			if isHeaded {
				// He signs in there and presses Done.
				card.State = "done"
				time.Sleep(20 * time.Millisecond)
				writeCards(t, h.State, card)
			}
			return nil
		},
		stepCard: func(_ home.Home, step supervisor.CredentialStep) error {
			steps = append(steps, step)
			return nil
		},
	}
	var stdout, stderr strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Act
	code := waitForCard(ctx, h, card.ID, "task-1", runtime, &stdout, &stderr)

	// Assert
	if code != 0 {
		t.Fatalf("exit = %d, stderr %s", code, stderr.String())
	}
	profile := filepath.Join(scratch, "browser-profile")
	if len(shows) != 2 || shows[0] != (shown{profile, card.Link, true}) || shows[1] != (shown{profile, "", false}) {
		t.Fatalf("shows = %+v, want the window at the card's page, then headless on the same profile", shows)
	}
	if len(steps) != 1 || steps[0] != (supervisor.CredentialStep{ID: card.ID, Task: "task-1", Action: "window-open"}) {
		t.Fatalf("steps = %+v, want the board told the window is open", steps)
	}
	if !strings.Contains(stdout.String(), "keeps what he did there") {
		t.Fatalf("stdout = %q, want it to say the session keeps his sign-in", stdout.String())
	}
}

// The goblin's own done and withdrawal reach the supervisor as steps on its
// card.
func TestAuthRequestSendsTheAskersStepOnItsCard(t *testing.T) {
	for _, test := range []struct {
		args []string
		want supervisor.CredentialStep
		says string
	}{
		{[]string{"--done", "cred-0123456789abcdef", "--task", "task-1"}, supervisor.CredentialStep{ID: "cred-0123456789abcdef", Task: "task-1", Action: "done"}, "is done"},
		{[]string{"--withdraw", "cred-0123456789abcdef", "--task", "task-1", "--reason", "The account already existed"}, supervisor.CredentialStep{ID: "cred-0123456789abcdef", Task: "task-1", Action: "withdraw", Reason: "The account already existed"}, "is withdrawn"},
		{[]string{"--reopen", "cred-0123456789abcdef"}, supervisor.CredentialStep{ID: "cred-0123456789abcdef", Action: "reopen"}, "is back in the Command Center"},
	} {
		t.Run(test.args[0], func(t *testing.T) {
			// Arrange
			useScratchHome(t)
			var steps []supervisor.CredentialStep
			runtime := commandRuntime{stepCard: func(_ home.Home, step supervisor.CredentialStep) error {
				steps = append(steps, step)
				return nil
			}}

			// Act
			code, stdout, stderr := runCLIWithRuntime(t, runtime, append([]string{"request"}, test.args...)...)

			// Assert
			if code != 0 || len(steps) != 1 || steps[0] != test.want || !strings.Contains(stdout, test.says) {
				t.Fatalf("exit %d, steps %+v, stdout %q, stderr %q", code, steps, stdout, stderr)
			}
		})
	}
}

// A card's flags are refused out of their shape before anything is filed
// or sent.
func TestAuthRequestRefusesCardFlagsOutOfShape(t *testing.T) {
	t.Setenv(spawn.BrowserSessionVariable, "task-1")
	for name, args := range map[string][]string{
		"a window without a wait":         {"--project", "throwaway", "--task", "task-1", "--why", "Sign in", "--link", "https://example.com/login", "--window"},
		"a window of another session":     {"--project", "throwaway", "--task", "task-2", "--why", "Sign in", "--link", "https://example.com/login", "--window", "--wait"},
		"a withdrawal without a reason":   {"--withdraw", "cred-0123456789abcdef", "--task", "task-1"},
		"a reason without a withdrawal":   {"--done", "cred-0123456789abcdef", "--reason", "late"},
		"done and withdraw at once":       {"--done", "cred-0123456789abcdef", "--withdraw", "cred-0123456789abcdef", "--reason", "late"},
		"a wait on a card with a new one": {"--wait-on", "cred-0123456789abcdef", "--project", "throwaway"},
		"a label that names nothing":      {"--project", "throwaway", "--why", "Sign up", "--link", "https://example.com/signup", "--label", "The email"},
		"a label of no input":             {"--project", "throwaway", "--why", "Sign up", "--link", "https://example.com/signup", "--label", "ACCOUNT_EMAIL=The email"},
		"a plain name shaped like a key":  {"--project", "throwaway", "--why", "Sign up", "--plain", "sk" + "_live_q7Lm2Xv9Rt4Kp8Zw3Nb6Hd1Yc"},
		"nothing to ask or open":          {"--project", "throwaway", "--why", "Sign up"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			root := useScratchHome(t)
			runtime := commandRuntime{stepCard: func(home.Home, supervisor.CredentialStep) error {
				t.Fatal("a refused step was sent")
				return nil
			}}

			// Act
			code, _, stderr := runCLIWithRuntime(t, runtime, append([]string{"request"}, args...)...)

			// Assert
			if code != 2 {
				t.Fatalf("exit = %d, want 2; stderr %s", code, stderr)
			}
			if _, err := os.Stat(filepath.Join(root, "state", "credential-requests-inbox")); !os.IsNotExist(err) {
				t.Fatal("a refused card reached the inbox")
			}
		})
	}
}
