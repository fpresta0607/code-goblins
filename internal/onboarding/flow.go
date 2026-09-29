package onboarding

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

var ErrCancelled = errors.New("setup cancelled; run goblins to continue")
var ErrBack = fmt.Errorf("back: %w", ErrCancelled)

var Agents = []string{"claude", "codex", "pi"}

type Flow struct {
	Detect  func(context.Context, string) Agent
	Choose  func(title string, choices []string, selected int) (int, error)
	Install func(string) error
	Login   func(string) error
	Save    func(string) error
}

func (f Flow) Run(ctx context.Context, saved string, isRerun bool) (string, error) {
	agents := make([]Agent, len(Agents))
	for index, name := range Agents {
		agents[index] = f.Detect(ctx, name)
	}
	selected := Recommended(agents, saved)
	shouldChoose := isRerun || !slices.Contains(Agents, saved)
	var problem string
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if shouldChoose {
			labels := make([]string, len(agents))
			for index, agent := range agents {
				status := agent.Reason
				if agent.Installed && agent.SignedIn {
					status = "Ready"
				}
				labels[index] = fmt.Sprintf("%s   %s", agent.Name, status)
				if index == Recommended(agents, saved) {
					labels[index] += " (Recommended)"
				}
			}
			choice, err := f.Choose("Choose your CFO", labels, selected)
			if err != nil {
				return "", err
			}
			if choice < 0 || choice >= len(agents) {
				return "", errors.New("invalid agent choice")
			}
			selected, shouldChoose, problem = choice, false, ""
		}
		agent := agents[selected]
		if agent.Installed && agent.SignedIn {
			if err := f.Save(agent.ID); err != nil {
				return "", fmt.Errorf("save default agent: %w", err)
			}
			return agent.ID, nil
		}
		title, action := "Sign in to "+agent.Name, "Open sign-in (Default)"
		operation := f.Login
		if !agent.Installed {
			title, action, operation = "Install "+agent.Name, "Install (Default)", f.Install
			packageName, err := Package(agent.ID)
			if err != nil {
				return "", err
			}
			title += "\nInstaller: npm install -g " + packageName
		}
		if problem != "" {
			title += "\n" + problem
		}
		choice, err := f.Choose(title, []string{action, "Choose another agent"}, 0)
		if errors.Is(err, ErrBack) {
			shouldChoose = true
			continue
		}
		if err != nil {
			return "", err
		}
		if choice == 1 {
			shouldChoose = true
			continue
		}
		if choice != 0 {
			return "", errors.New("invalid setup action")
		}
		if err := operation(agent.ID); err != nil {
			problem = "That step did not finish. Retry or choose another agent."
			continue
		}
		agents[selected] = f.Detect(ctx, agent.ID)
		problem = agents[selected].Reason
	}
}
