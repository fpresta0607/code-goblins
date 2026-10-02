// Package onboarding is the quick start's own logic: which agents this
// machine can run the CFO on, the steps that make the chosen one ready, and
// the menu each step is shown with. It installs nothing and signs nobody in
// itself: its callers hand it what does.
package onboarding

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Agents are the agents a CFO can run on, in the order the quick start shows
// them. Claude Code is first and the one it recommends.
var Agents = []string{"claude", "codex", "pi"}

// agentNames are the names the quick start shows each agent by.
var agentNames = map[string]string{"claude": "Claude Code", "codex": "Codex", "pi": "pi"}

// Name is the name the quick start shows the agent id by, in every line that
// names it.
func Name(id string) string {
	return agentNames[id]
}

// State is how ready an agent is to run the CFO.
type State int

const (
	// Missing is an agent not on PATH, or on it only in a form a native
	// terminal cannot start.
	Missing State = iota
	// Shadowed is Claude Code whose native build is installed but found
	// after npm's claude.cmd on PATH, which a native terminal cannot start.
	// Installing again changes nothing; removing npm's package does.
	Shadowed
	// SignedOut is an installed agent whose own status command says nobody is
	// signed in.
	SignedOut
	// Unverified is an installed agent whose status command did not say
	// whether anybody is signed in. It is never shown as ready.
	Unverified
	// Ready is an installed agent whose status command says it is signed in.
	Ready
)

// Agent is one agent as this machine has it.
type Agent struct {
	ID    string
	Name  string
	State State
	// Reason says why the agent is not ready, in the quick start's words, and
	// is empty when it is.
	Reason string
	// Fix is a command the person runs to make the agent ready, or empty.
	// It is shown at the end of a line of its own, so it copies whole.
	Fix string
}

// probeTimeout bounds one agent's status command.
const probeTimeout = 10 * time.Second

// Detector reads how ready each agent is, from PATH and from the agent's own
// read-only status command. It never prints what a status command wrote,
// which can hold an account's details.
type Detector struct {
	// LookPath finds a program on PATH, as exec.LookPath does.
	LookPath func(name string) (string, error)
	// Probe runs an agent's status command and returns what it wrote.
	Probe func(ctx context.Context, name string, args ...string) (execx.Result, error)
	// PiDirectory is pi's agent folder, whose settings.json names the
	// provider pi signs in to.
	PiDirectory string
	// ClaudeDirectory is the folder Claude Code's native installer puts
	// claude.exe in, ~\.local\bin.
	ClaudeDirectory string
}

// Detect reads how ready the agent named id is.
func (d Detector) Detect(ctx context.Context, id string) Agent {
	agent := Agent{ID: id, Name: agentNames[id], State: Missing, Reason: "Not installed"}
	if !slices.Contains(Agents, id) {
		agent.Name, agent.Reason = id, "Not an agent the CFO runs on"
		return agent
	}
	path, err := d.LookPath(id)
	if err != nil {
		return agent
	}
	// A native terminal starts a program itself, with no shell to run a
	// script shim, and Claude Code's native build is claude.exe.
	if id == "claude" && !strings.EqualFold(filepath.Ext(path), ".exe") {
		agent.Reason = "npm's claude.cmd comes first on PATH and a native terminal cannot start it"
		agent.Fix = "npm.cmd uninstall -g @anthropic-ai/claude-code"
		if _, err := os.Stat(filepath.Join(d.ClaudeDirectory, "claude.exe")); d.ClaudeDirectory != "" && err == nil {
			agent.State = Shadowed
		}
		return agent
	}
	agent.State, agent.Reason = Unverified, "Sign-in could not be verified"
	args := map[string][]string{"claude": {"auth", "status", "--json"}, "codex": {"login", "status"}}[id]
	if id == "pi" {
		provider := piProvider(d.PiDirectory)
		if provider == "" {
			agent.State, agent.Reason = SignedOut, "Sign-in needed: no provider chosen"
			return agent
		}
		args = []string{"auth", "check", "--provider", provider, "--json", "--no-refresh"}
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	result, err := d.Probe(ctx, id, args...)
	if err != nil {
		return agent
	}
	switch signedIn(id, result) {
	case Ready:
		agent.State, agent.Reason = Ready, ""
	case SignedOut:
		agent.State, agent.Reason = SignedOut, "Sign-in needed"
	}
	return agent
}

// piProvider is the provider pi's settings name as its default, or empty
// when they name none.
func piProvider(directory string) string {
	data, err := fsx.ReadFile(filepath.Join(directory, "settings.json"))
	if err != nil {
		return ""
	}
	var settings struct {
		DefaultProvider string `json:"defaultProvider"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return ""
	}
	return strings.TrimSpace(settings.DefaultProvider)
}

// signedIn reads an agent's status command: Ready or SignedOut only when the
// reply says so in the agent's own words, and Unverified for anything else, a
// later version's wording included, so nothing is called signed in or signed
// out by guess. The signed-in replies were read from Claude Code 2.1.287,
// Codex 0.154.0 and pi 0.85.1.
func signedIn(id string, result execx.Result) State {
	switch id {
	case "claude":
		var status struct {
			LoggedIn *bool `json:"loggedIn"`
		}
		if json.Unmarshal(result.Stdout, &status) != nil || status.LoggedIn == nil {
			return Unverified
		}
		if !*status.LoggedIn {
			return SignedOut
		}
		if result.ExitCode == 0 {
			return Ready
		}
	case "codex":
		output := strings.TrimSpace(string(result.Stdout) + "\n" + string(result.Stderr))
		if strings.HasPrefix(output, "Logged in using ") && result.ExitCode == 0 {
			return Ready
		}
		if output == "Not logged in" && result.ExitCode != 0 {
			return SignedOut
		}
	case "pi":
		var status struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(result.Stdout, &status) != nil {
			return Unverified
		}
		if status.Status == "ready" && result.ExitCode == 0 {
			return Ready
		}
		if status.Status == "not_ready" {
			return SignedOut
		}
	}
	return Unverified
}
