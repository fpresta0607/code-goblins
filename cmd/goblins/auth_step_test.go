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
	code := waitForCard(context.Background(), h, "cred-0123456789abcdef", &stdout, &stderr)

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
			code := waitForCard(context.Background(), h, "cred-0123456789abcdef", &stdout, &stderr)

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
	code := waitForCard(context.Background(), h, "cred-0123456789abcdef", &stdout, &stderr)

	// Assert
	if code != 0 || !strings.Contains(stdout.String(), "granted sample to task-1; re-source your auth.ps1") {
		t.Fatalf("wait = %d %s %s", code, stdout.String(), stderr.String())
	}
}

func TestWaitingOnAGrantCardReportsRefreshFailureWithoutSourcingTheUnchangedScript(t *testing.T) {
	// Arrange
	h := home.Home{State: t.TempDir()}
	reason := "spawn: the record of task-1 changed as asked, but its auth.ps1 was not rewritten, so run goblins auth refresh task-1: task cleanup is in progress"
	writeCards(t, h.State, supervisor.CredentialRequest{ID: "cred-0123456789abcdef", Project: "throwaway", Task: "task-1", GrantService: "sample", State: "granted", Reason: reason})
	var stdout, stderr strings.Builder

	// Act
	code := waitForCard(context.Background(), h, "cred-0123456789abcdef", &stdout, &stderr)

	// Assert
	if code != 0 || !strings.Contains(stdout.String(), "granted sample to task-1") || !strings.Contains(stdout.String(), reason) || strings.Contains(stdout.String(), "re-source your auth.ps1") {
		t.Fatalf("wait = %d %s %s", code, stdout.String(), stderr.String())
	}
}

// A card whose step he took on the board's screen of the goblin's browser
// closes once he hands the browser back, and the goblin's wait prints his
// words with its Done, so the goblin goes on knowing what he did there.
func TestWaitingOnACardHeHandedBackPrintsHisWords(t *testing.T) {
	// Arrange
	h := home.Home{State: t.TempDir()}
	writeCards(t, h.State, supervisor.CredentialRequest{ID: "cred-0123456789abcdef", Task: "task-1", Project: "throwaway", State: "done", Window: true, WindowState: "open", Link: "https://example.com/login", Reason: "Handed back from the board: signed in, the second factor is on"})
	var stdout, stderr strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Act
	code := waitForCard(ctx, h, "cred-0123456789abcdef", &stdout, &stderr)

	// Assert
	if code != 0 {
		t.Fatalf("exit = %d, stderr %s", code, stderr.String())
	}
	for _, want := range []string{"card cred-0123456789abcdef is done", "Handed back from the board: signed in, the second factor is on"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
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
	t.Setenv(spawn.BrowserURLVariable, "")
	for name, args := range map[string][]string{
		"a window without a wait":         {"--project", "throwaway", "--task", "task-1", "--why", "Sign in", "--link", "https://example.com/login", "--window"},
		"a window of another session":     {"--project", "throwaway", "--task", "task-2", "--why", "Sign in", "--link", "https://example.com/login", "--window", "--wait"},
		"a window the board cannot show":  {"--project", "throwaway", "--task", "task-1", "--why", "Sign in", "--link", "https://example.com/login", "--window", "--wait"},
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
