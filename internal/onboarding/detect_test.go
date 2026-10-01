package onboarding

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// piSettings is a pi agent folder whose settings name anthropic as the
// provider.
func piSettings(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "settings.json"), []byte(`{"defaultProvider":"anthropic"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return directory
}

// installed finds every agent on PATH as a program a native terminal starts.
func installed(name string) (string, error) { return name + ".exe", nil }

// Each agent's own status command says whether it is signed in, and only its
// own words count: a reply that says neither leaves the sign-in unverified.
func TestDetectReadsEachAgentsSignInFromItsOwnStatusCommand(t *testing.T) {
	piArgs := []string{"auth", "check", "--provider", "anthropic", "--json", "--no-refresh"}
	for _, c := range []struct {
		name, id string
		args     []string
		stdout   string
		exit     int
		want     State
		reason   string
	}{
		{"claude signed in", "claude", []string{"auth", "status", "--json"}, `{"loggedIn":true,"authMethod":"claude.ai"}`, 0, Ready, ""},
		{"claude signed out", "claude", []string{"auth", "status", "--json"}, `{"loggedIn":false}`, 1, SignedOut, "Sign-in needed"},
		{"claude says nothing it knows", "claude", []string{"auth", "status", "--json"}, `{}`, 0, Unverified, "Sign-in could not be verified"},
		{"claude signed in by a failing command", "claude", []string{"auth", "status", "--json"}, `{"loggedIn":true}`, 1, Unverified, "Sign-in could not be verified"},
		{"claude loggedIn of the wrong type", "claude", []string{"auth", "status", "--json"}, `{"loggedIn":"true"}`, 0, Unverified, "Sign-in could not be verified"},
		{"codex signed in", "codex", []string{"login", "status"}, "Logged in using ChatGPT\n", 0, Ready, ""},
		{"codex signed out", "codex", []string{"login", "status"}, "Not logged in\n", 1, SignedOut, "Sign-in needed"},
		{"codex in other words", "codex", []string{"login", "status"}, "Session expired", 1, Unverified, "Sign-in could not be verified"},
		{"pi signed in", "pi", piArgs, `{"status":"ready"}`, 0, Ready, ""},
		{"pi signed out", "pi", piArgs, `{"status":"not_ready"}`, 1, SignedOut, "Sign-in needed"},
		{"pi in other words", "pi", piArgs, `not json`, 0, Unverified, "Sign-in could not be verified"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			detector := Detector{LookPath: installed, PiDirectory: piSettings(t), Probe: func(ctx context.Context, name string, args ...string) (execx.Result, error) {
				if name != c.id || !slices.Equal(args, c.args) {
					t.Errorf("probed %s %q, want %s %q", name, args, c.id, c.args)
				}
				if _, bounded := ctx.Deadline(); !bounded {
					t.Error("the status command ran with no deadline")
				}
				return execx.Result{Stdout: []byte(c.stdout), ExitCode: c.exit}, nil
			}}

			// Act
			agent := detector.Detect(context.Background(), c.id)

			// Assert
			if agent.State != c.want || agent.Reason != c.reason {
				t.Errorf("Detect = state %d, reason %q; want state %d, reason %q", agent.State, agent.Reason, c.want, c.reason)
			}
		})
	}
}

// An agent not on PATH is missing and its status command is never run.
func TestDetectNeverProbesAnAgentThatIsNotInstalled(t *testing.T) {
	for _, id := range Agents {
		t.Run(id, func(t *testing.T) {
			// Arrange
			detector := Detector{LookPath: func(string) (string, error) { return "", exec.ErrNotFound }, Probe: func(context.Context, string, ...string) (execx.Result, error) {
				t.Error("probed an agent that is not installed")
				return execx.Result{}, nil
			}}

			// Act
			agent := detector.Detect(context.Background(), id)

			// Assert
			if agent.State != Missing || agent.Reason != "Not installed" || agent.Name == "" {
				t.Errorf("Detect = %+v, want it missing as Not installed, with its name", agent)
			}
		})
	}
}

// A status command that cannot run leaves the sign-in unverified, and what it
// failed with, which can name an account, is never the reason shown.
func TestDetectKeepsAFailedStatusCommandsOutputOffTheScreen(t *testing.T) {
	for _, id := range Agents {
		t.Run(id, func(t *testing.T) {
			// Arrange
			detector := Detector{LookPath: installed, PiDirectory: piSettings(t), Probe: func(context.Context, string, ...string) (execx.Result, error) {
				return execx.Result{Stdout: []byte("account someone@example.com")}, errors.New("token for someone@example.com expired")
			}}

			// Act
			agent := detector.Detect(context.Background(), id)

			// Assert
			if agent.State != Unverified || agent.Reason != "Sign-in could not be verified" {
				t.Errorf("Detect = %+v, want its sign-in unverified with the quick start's own reason", agent)
			}
		})
	}
}

// A native terminal starts a program itself, with no shell to run a script
// shim: Claude Code found only as npm's claude.cmd is missing, its status
// command never run, while Codex and pi run from their shims.
func TestDetectCountsClaudeCodeOnlyAsItsNativeBuild(t *testing.T) {
	// Arrange
	probed := []string{}
	detector := Detector{LookPath: func(name string) (string, error) { return name + ".cmd", nil }, PiDirectory: piSettings(t), Probe: func(_ context.Context, name string, _ ...string) (execx.Result, error) {
		probed = append(probed, name)
		return execx.Result{Stdout: []byte("Logged in using ChatGPT")}, nil
	}}

	// Act
	claude := detector.Detect(context.Background(), "claude")
	codex := detector.Detect(context.Background(), "codex")

	// Assert
	if claude.State != Missing || claude.Reason != npmClaude {
		t.Errorf("claude as a script = %+v, want it missing for its native build", claude)
	}
	if codex.State != Ready || !slices.Equal(probed, []string{"codex"}) {
		t.Errorf("codex as a script = %+v, probed %q; want it ready and claude never probed", codex, probed)
	}
}

// npmClaude is the reason Claude Code found as npm's script is not ready.
const npmClaude = "npm's claude.cmd comes first on PATH and a native terminal cannot start it; run: npm.cmd uninstall -g @anthropic-ai/claude-code"

// Claude Code found as npm's script while its native build is installed is
// shadowed, not missing: installing again would change nothing, and the
// reason names the uninstall that does. Its status command is never run.
func TestDetectNamesTheUninstallWhenNpmsClaudeShadowsTheNativeBuild(t *testing.T) {
	for _, c := range []struct {
		name     string
		isNative bool
		want     State
	}{
		{"the native build installed", true, Shadowed},
		{"no native build", false, Missing},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			directory := t.TempDir()
			if c.isNative {
				if err := os.WriteFile(filepath.Join(directory, "claude.exe"), nil, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			detector := Detector{ClaudeDirectory: directory, LookPath: func(name string) (string, error) { return name + ".cmd", nil }, Probe: func(context.Context, string, ...string) (execx.Result, error) {
				t.Error("probed claude found as a script")
				return execx.Result{}, nil
			}}

			// Act
			agent := detector.Detect(context.Background(), "claude")

			// Assert
			if agent.State != c.want || agent.Reason != npmClaude {
				t.Errorf("Detect = %+v, want state %d with the uninstall named", agent, c.want)
			}
		})
	}
}

// pi signs in to a provider, so a pi whose settings name none is signed out
// with what to do in pi, and no provider is guessed.
func TestDetectReadsPiAsSignedOutUntilItHasAProvider(t *testing.T) {
	for name, settings := range map[string]string{"no settings file": "", "no provider": `{}`, "unreadable settings": `{`} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			directory := t.TempDir()
			if settings != "" {
				if err := os.WriteFile(filepath.Join(directory, "settings.json"), []byte(settings), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			detector := Detector{LookPath: installed, PiDirectory: directory, Probe: func(context.Context, string, ...string) (execx.Result, error) {
				t.Error("probed pi with no provider to check")
				return execx.Result{}, nil
			}}

			// Act
			agent := detector.Detect(context.Background(), "pi")

			// Assert
			if agent.State != SignedOut || agent.Reason != "Sign-in needed: no provider chosen" {
				t.Errorf("Detect = %+v, want pi signed out until it has a provider", agent)
			}
		})
	}
}

// A name that is no agent is never looked for on PATH.
func TestDetectRefusesANameThatIsNoAgent(t *testing.T) {
	// Arrange
	detector := Detector{LookPath: func(string) (string, error) {
		t.Error("looked on PATH for a name that is no agent")
		return "", nil
	}}

	// Act
	agent := detector.Detect(context.Background(), "kimi")

	// Assert
	if agent.State != Missing || agent.Reason != "Not an agent the CFO runs on" {
		t.Errorf("Detect = %+v, want it refused as no agent", agent)
	}
}
