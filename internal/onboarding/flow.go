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
// Every step that waits on the person is one Ask with its default first, so
// Enter alone carries a person from nothing to a ready agent, and every step
// finished is one Done line, so the screen holds what was answered and the
// one step that waits.
type Flow struct {
	// Detect reads how ready an agent is.
	Detect func(ctx context.Context, id string) Agent
	// Ask shows a step and returns the choice the person accepts. It returns
	// ErrBack for Escape and ErrCancelled when no answer can come.
	Ask func(step Step) (int, error)
	// Done says a step is finished, by its name and its answer, and Undo
	// takes back every line Done said, for a step back to the choice of
	// agent.
	Done func(name, answer string)
	Undo func()
	// Marks are each agent's own mark on its tab.
	Marks map[string]string
	// Recommended is the agent whose tab says it is the recommended one, and
	// Notes are the few words each agent's note says a CFO in it gets. Both
	// come from the one table of what is proved, which this package does not
	// hold.
	Recommended string
	Notes       map[string]string
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
	// named is whether the agent's own line is on the screen.
	named := false
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if choosing {
			all := make([]Agent, len(Agents))
			for index := range Agents {
				all[index] = detected(index)
			}
			f.Undo()
			choice, err := f.Ask(f.agentStep(all, saved, selected))
			if err != nil {
				return "", err
			}
			selected, choosing, problem, named = choice, false, "", false
		}
		agent := detected(selected)
		if !named {
			f.Done("Agent", agent.Name)
			named = true
		}
		if agent.State == Ready {
			return agent.ID, f.save(agent.ID)
		}
		step, run := f.step(agent)
		if agent.Fix != "" {
			step.Detail += "\nRun: " + agent.Fix
		}
		if problem != "" {
			step.Detail += "\n" + problem
		}
		choice, err := f.Ask(step)
		if errors.Is(err, ErrBack) {
			choosing = true
			continue
		}
		if err != nil {
			return "", err
		}
		switch step.Choices[choice].Label {
		case chooseAnother:
			choosing = true
			continue
		case continueUnverified:
			f.Done("Sign-in", "not verified; continuing as you chose")
			return agent.ID, f.save(agent.ID)
		}
		if err := run(agent.ID); err != nil {
			problem = "That did not finish: " + err.Error()
			continue
		}
		agents[selected], problem = nil, ""
		if again := detected(selected); again.State == agent.State {
			problem = "That changed nothing: " + again.Reason + "."
		} else {
			f.Done(finished(agent))
		}
	}
}

// finished is the line for the step that made agent, as it was, more ready.
func finished(agent Agent) (name, answer string) {
	switch agent.State {
	case Missing:
		return "Install", agent.Name + " is installed"
	case Shadowed:
		return "Cleanup", "npm's " + agent.Name + " is out of the way"
	default:
		return "Sign-in", agent.Name + "'s own sign-in finished"
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

// step is the screen for an agent that is not ready, with its default
// first, and what the default runs.
func (f Flow) step(agent Agent) (Step, func(string) error) {
	switch agent.State {
	case Missing:
		installer, _ := InstallerFor(agent.ID)
		return Step{Title: "Install " + agent.Name, Detail: agent.Reason + ". Enter runs " + installer.Describe() + ".", Choices: Labels("Install "+agent.Name, chooseAnother)}, f.Install
	case Shadowed:
		return Step{Title: "Remove npm's " + agent.Name, Detail: agent.Reason + ". Run the command below in another window, then press Enter to check again.", Choices: Labels(checkAgain, chooseAnother)}, func(string) error { return nil }
	case SignedOut:
		return Step{Title: "Sign in to " + agent.Name, Detail: "Enter opens " + agent.Name + "'s own sign-in. Code Goblins never sees your password.", Choices: Labels("Open sign-in", chooseAnother)}, f.Login
	default:
		// The agent may be signed in: its status command did not say. Signing
		// in again is the default, and continuing is the person's own call.
		return Step{Title: agent.Name + "'s sign-in could not be verified", Detail: "Enter opens " + agent.Name + "'s own sign-in. Code Goblins never sees your password.", Choices: Labels("Open sign-in", continueUnverified, chooseAnother)}, f.Login
	}
}

// agentStep is the choice of agent: one row of tabs, each with the agent's
// own mark, and under the row how ready the marked agent is and what a CFO in
// it gets. The recommended agent's tab says so, and the remembered agent's
// note says it is the current one.
func (f Flow) agentStep(agents []Agent, saved string, selected int) Step {
	choices := make([]Choice, len(agents))
	for index, agent := range agents {
		label, note := agent.Name, agent.Reason
		if agent.State == Ready {
			note = "Ready"
		}
		if agent.ID == f.Recommended {
			label += " (recommended)"
		}
		if said := f.Notes[agent.ID]; said != "" {
			note += " · " + said
		}
		if agent.ID == saved {
			note += " · current"
		}
		choices[index] = Choice{Label: label, Mark: f.Marks[agent.ID], Note: note}
	}
	return Step{Title: "Choose the agent your CFO runs on", Choices: choices, Selected: selected, Tabs: true}
}
