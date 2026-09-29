package onboarding

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type Agent struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	SignedIn  bool   `json:"signed_in"`
	Reason    string `json:"reason,omitempty"`
}

type Detector struct {
	LookPath    func(string) (string, error)
	Probe       func(context.Context, string, ...string) (execx.Result, error)
	PiDirectory string
}

func (d Detector) Detect(ctx context.Context, name string) Agent {
	agent := Agent{ID: name, Reason: "Sign-in could not be verified"}
	var args []string
	switch name {
	case "claude":
		agent.Name = "Claude Code"
		args = []string{"auth", "status", "--json"}
	case "codex":
		agent.Name = "Codex"
		args = []string{"login", "status"}
	case "pi":
		agent.Name = "pi"
	default:
		agent.Reason = "Unsupported agent"
		return agent
	}
	if _, err := d.LookPath(name); err != nil {
		agent.Reason = "Not installed"
		return agent
	}
	agent.Installed = true
	if name == "pi" {
		data, err := os.ReadFile(filepath.Join(d.PiDirectory, "settings.json"))
		var settings struct {
			DefaultProvider string `json:"defaultProvider"`
		}
		if err != nil || json.Unmarshal(data, &settings) != nil || strings.TrimSpace(settings.DefaultProvider) == "" {
			agent.Reason = "Choose a provider in pi with /login and /model"
			return agent
		}
		args = []string{"auth", "check", "--provider", settings.DefaultProvider, "--json", "--no-refresh"}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result, err := d.Probe(ctx, name, args...)
	if err != nil {
		return agent
	}
	switch name {
	case "claude":
		var status struct {
			LoggedIn *bool `json:"loggedIn"`
		}
		if json.Unmarshal(result.Stdout, &status) != nil || status.LoggedIn == nil {
			return agent
		}
		agent.SignedIn = *status.LoggedIn && result.ExitCode == 0
		if !*status.LoggedIn {
			agent.Reason = "Sign-in needed"
		}
	case "codex":
		output := strings.TrimSpace(string(result.Stdout) + "\n" + string(result.Stderr))
		if strings.HasPrefix(output, "Logged in using ") && result.ExitCode == 0 {
			agent.SignedIn = true
		}
		if output == "Not logged in" && result.ExitCode == 1 {
			agent.Reason = "Sign-in needed"
		}
	case "pi":
		var status struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(result.Stdout, &status) != nil {
			return agent
		}
		agent.SignedIn = status.Status == "ready" && result.ExitCode == 0
		if status.Status == "not_ready" {
			agent.Reason = "Sign-in needed"
		}
	}
	if agent.SignedIn {
		agent.Reason = ""
	}
	return agent
}

func Recommended(agents []Agent, saved string) int {
	for index, agent := range agents {
		if agent.ID == saved {
			return index
		}
	}
	for index, agent := range agents {
		if agent.Installed && agent.SignedIn {
			return index
		}
	}
	return 0
}
