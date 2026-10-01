package onboarding

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// ErrCancelled ends the quick start with nothing chosen: the person pressed
// Escape at the first screen, or the input ended.
var ErrCancelled = errors.New("setup was cancelled; run goblins to continue")

// ErrBack is Escape at a step: the quick start returns to the choice of
// agent, and at that choice it is a cancel, said in ErrCancelled's words.
var ErrBack = fmt.Errorf("%w", ErrCancelled)

// Flow is the quick start's steps for the agent the CFO runs on: choose one,
// install it when it is missing, sign in when nobody is, and remember it.
// Every step that waits on the person is one Choose with its default first,
// so Enter alone carries a person from nothing to a ready agent.
type Flow struct {
	// Detect reads how ready an agent is.
	Detect func(ctx context.Context, id string) Agent
	// Choose shows title above choices with one selected, and returns the
	// one the person accepts. It returns ErrBack for Escape and ErrCancelled
	// when no answer can come.
	Choose func(title string, choices []string, selected int) (int, error)
	// Install installs an agent, and Login opens the agent's own sign-in and
	// returns when it ends. Neither is ever run without the person's Enter.
	Install func(id string) error
	Login   func(id string) error
	// Save remembers the agent the CFO starts as.
	Save func(id string) error
}

// Run returns the agent the CFO starts as. saved is the agent remembered from
// an earlier run, or empty. A remembered agent that is ready is returned
// without a screen, so later runs skip what is already set up; rerun shows the
// choice all the same. Nothing is remembered until the chosen agent is ready,
// or the person chose to continue with one whose sign-in cannot be verified.
func (f Flow) Run(ctx context.Context, saved string, rerun bool) (string, error) {
	choosing := rerun || !slices.Contains(Agents, saved)
	// The choice starts on the remembered agent, or else on Claude Code, the
	// one it recommends, so Enter always takes the marked agent.
	selected := max(slices.Index(Agents, saved), 0)
	// Each agent is read once, when a screen first needs it, so a run with a
	// remembered agent asks only that agent's status command.
	agents := make([]*Agent, len(Agents))
	detected := func(index int) Agent {
		if agents[index] == nil {
			agent := f.Detect(ctx, Agents[index])
			agents[index] = &agent
		}
		return *agents[index]
	}
	problem := ""
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if choosing {
			all := make([]Agent, len(Agents))
			for index := range Agents {
				all[index] = detected(index)
			}
			choice, err := f.Choose("Choose the agent your CFO runs on", agentLabels(all, saved), selected)
			if err != nil {
				return "", err
			}
			selected, choosing, problem = choice, false, ""
		}
		agent := detected(selected)
		if agent.State == Ready {
			return agent.ID, f.save(agent.ID)
		}
		title, choices, run := f.step(agent)
		if problem != "" {
			title += "\n" + problem
		}
		choice, err := f.Choose(title, choices, 0)
		if errors.Is(err, ErrBack) {
			choosing = true
			continue
		}
		if err != nil {
			return "", err
		}
		switch choices[choice] {
		case chooseAnother:
			choosing = true
			continue
		case continueUnverified:
			return agent.ID, f.save(agent.ID)
		}
		if err := run(agent.ID); err != nil {
			problem = "That did not finish: " + err.Error()
			continue
		}
		agents[selected], problem = nil, ""
		if again := detected(selected); again.State == agent.State {
			problem = "That changed nothing: " + again.Reason + "."
		}
	}
}

func (f Flow) save(id string) error {
	if err := f.Save(id); err != nil {
		return fmt.Errorf("remember %s as the CFO's agent: %w", id, err)
	}
	return nil
}

// The choices a step offers besides its default.
const (
	chooseAnother      = "Choose another agent"
	continueUnverified = "Continue without verifying"
	checkAgain         = "Check again"
)

// step is the screen for an agent that is not ready: its title, its choices
// with the default first, and what the default runs.
func (f Flow) step(agent Agent) (title string, choices []string, run func(string) error) {
	switch agent.State {
	case Missing:
		installer, _ := InstallerFor(agent.ID)
		return "Install " + agent.Name + "\n" + agent.Reason + ". Enter runs " + installer.Describe() + ".", []string{"Install " + agent.Name, chooseAnother}, f.Install
	case Shadowed:
		return "Remove npm's " + agent.Name + "\n" + agent.Reason + ". Run it in another window, then press Enter to check again.", []string{checkAgain, chooseAnother}, func(string) error { return nil }
	case SignedOut:
		return "Sign in to " + agent.Name + "\nEnter opens " + agent.Name + "'s own sign-in. Code Goblins never sees your password.", []string{"Open sign-in", chooseAnother}, f.Login
	default:
		// The agent may be signed in: its status command did not say. Signing
		// in again is the default, and continuing is the person's own call.
		return agent.Name + "'s sign-in could not be verified\nEnter opens " + agent.Name + "'s own sign-in. Code Goblins never sees your password.", []string{"Open sign-in", continueUnverified, chooseAnother}, f.Login
	}
}

// agentLabels are the choice's rows: each agent's name and how ready it is.
// Claude Code is marked as the recommended one and the remembered agent as
// the current one. A Codex or pi row says what such a CFO goes without.
func agentLabels(agents []Agent, saved string) []string {
	labels := make([]string, len(agents))
	for index, agent := range agents {
		status := agent.Reason
		if agent.State == Ready {
			status = "Ready"
		}
		labels[index] = fmt.Sprintf("%-12s %s", agent.Name, status)
		if agent.ID == "claude" {
			labels[index] += "  (Recommended: the best experience)"
		} else {
			labels[index] += "  (goblin reports do not wake it yet)"
		}
		if agent.ID == saved {
			labels[index] += "  (Current)"
		}
	}
	return labels
}
