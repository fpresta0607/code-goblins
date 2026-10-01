package onboarding

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// screen is one screen a test's flow showed: the first line of its title,
// its choices and its default.
type screen struct {
	heading  string
	choices  []string
	selected int
}

// machine is a test's agents by id, and a flow over them that records every
// screen, install, sign-in and save. answer picks each screen's choice; with
// none every screen's default is accepted, as Enter does.
type machine struct {
	t       *testing.T
	states  map[string]State
	answer  func(shown screen) (int, error)
	screens []screen
	actions []string
	probes  []string
}

func (m *machine) flow() Flow {
	return Flow{
		Detect: func(_ context.Context, id string) Agent {
			m.probes = append(m.probes, id)
			reason := map[State]string{Missing: "Not installed", SignedOut: "Sign-in needed", Unverified: "Sign-in could not be verified"}[m.states[id]]
			return Agent{ID: id, Name: agentNames[id], State: m.states[id], Reason: reason}
		},
		Choose: func(title string, choices []string, selected int) (int, error) {
			heading, _, _ := strings.Cut(title, "\n")
			shown := screen{heading, choices, selected}
			m.screens = append(m.screens, shown)
			if m.answer != nil {
				return m.answer(shown)
			}
			return selected, nil
		},
		Install: func(id string) error {
			m.actions = append(m.actions, "install "+id)
			m.states[id] = SignedOut
			return nil
		},
		Login: func(id string) error {
			m.actions = append(m.actions, "sign in "+id)
			m.states[id] = Ready
			return nil
		},
		Save: func(id string) error {
			m.actions = append(m.actions, "save "+id)
			return nil
		},
	}
}

func (m *machine) headings() []string {
	headings := make([]string, len(m.screens))
	for index, shown := range m.screens {
		headings[index] = shown.heading
	}
	return headings
}

// From nothing, Enter alone at every screen chooses Claude Code, installs it,
// signs in and remembers it: each screen's default is the way on.
func TestEnterAloneCarriesAFirstRunToAReadyAgent(t *testing.T) {
	// Arrange
	m := &machine{t: t, states: map[string]State{}}

	// Act
	agent, err := m.flow().Run(context.Background(), "", false)

	// Assert
	if err != nil || agent != "claude" {
		t.Fatalf("Run = %q, %v; want claude", agent, err)
	}
	if want := []string{"Choose the agent your CFO runs on", "Install Claude Code", "Sign in to Claude Code"}; !slices.Equal(m.headings(), want) {
		t.Errorf("screens = %q, want %q", m.headings(), want)
	}
	if want := []string{"install claude", "sign in claude", "save claude"}; !slices.Equal(m.actions, want) {
		t.Errorf("actions = %q, want %q", m.actions, want)
	}
	for _, shown := range m.screens {
		if shown.selected != 0 {
			t.Errorf("%q starts on choice %d, want its first", shown.heading, shown.selected)
		}
	}
}

// The choice starts on the remembered agent, or else on the first one that is
// ready, Claude Code among equals and when none is.
func TestTheChoiceStartsOnTheRememberedAgentOrAReadyOne(t *testing.T) {
	for _, c := range []struct {
		name   string
		states map[string]State
		saved  string
		want   int
	}{
		{"nothing ready", map[string]State{}, "", 0},
		{"only codex ready", map[string]State{"codex": Ready}, "", 1},
		{"codex and pi ready", map[string]State{"codex": Ready, "pi": Ready}, "", 1},
		{"all ready", map[string]State{"claude": Ready, "codex": Ready, "pi": Ready}, "", 0},
		{"pi remembered though claude is ready", map[string]State{"claude": Ready, "pi": Ready}, "pi", 2},
		{"a remembered name that is no agent", map[string]State{"pi": Ready}, "kimi", 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			m := &machine{t: t, states: c.states, answer: func(shown screen) (int, error) { return 0, ErrCancelled }}

			// Act
			_, err := m.flow().Run(context.Background(), c.saved, true)

			// Assert
			if !errors.Is(err, ErrCancelled) || len(m.screens) != 1 || m.screens[0].selected != c.want {
				t.Errorf("Run = %v with screens %+v; want one choice starting on %d", err, m.screens, c.want)
			}
		})
	}
}

// The choice names how ready each agent is, marks Claude Code as recommended
// and the remembered agent as current, and says what a Codex or pi CFO goes
// without.
func TestTheChoiceSaysHowReadyEachAgentIsAndWhichIsRecommended(t *testing.T) {
	// Arrange
	m := &machine{t: t, states: map[string]State{"claude": Ready, "codex": SignedOut}, answer: func(screen) (int, error) { return 0, ErrCancelled }}

	// Act
	_, _ = m.flow().Run(context.Background(), "codex", true)

	// Assert
	want := []string{
		"Claude Code  Ready  (Recommended: the best experience)",
		"Codex        Sign-in needed  (goblin reports do not wake it yet)  (Current)",
		"pi           Not installed  (goblin reports do not wake it yet)",
	}
	if len(m.screens) != 1 || !slices.Equal(m.screens[0].choices, want) {
		t.Errorf("the choice shows %+v, want %q", m.screens, want)
	}
}

// A remembered agent that is ready is returned with no screen and only its
// own status command run, so later runs skip what is already set up; goblins
// setup shows the choice all the same, starting on it.
func TestARememberedReadyAgentSkipsEveryScreenUnlessSetupIsRerun(t *testing.T) {
	for _, rerun := range []bool{false, true} {
		// Arrange
		m := &machine{t: t, states: map[string]State{"claude": Ready, "pi": Ready}}

		// Act
		agent, err := m.flow().Run(context.Background(), "pi", rerun)

		// Assert
		if err != nil || agent != "pi" {
			t.Fatalf("rerun %v: Run = %q, %v; want pi", rerun, agent, err)
		}
		if rerun {
			if len(m.screens) != 1 || m.screens[0].selected != 2 {
				t.Errorf("a rerun showed %+v, want the choice starting on pi", m.screens)
			}
			continue
		}
		if len(m.screens) != 0 || !slices.Equal(m.probes, []string{"pi"}) {
			t.Errorf("a later run showed %+v and probed %q, want no screen and pi alone probed", m.screens, m.probes)
		}
	}
}

// A remembered agent that is no longer signed in goes straight to its
// sign-in, with the other agents one choice away.
func TestARememberedAgentThatIsSignedOutGoesStraightToItsSignIn(t *testing.T) {
	// Arrange
	m := &machine{t: t, states: map[string]State{"codex": SignedOut}}

	// Act
	agent, err := m.flow().Run(context.Background(), "codex", false)

	// Assert
	if err != nil || agent != "codex" {
		t.Fatalf("Run = %q, %v; want codex", agent, err)
	}
	if len(m.screens) != 1 || m.screens[0].heading != "Sign in to Codex" || !slices.Equal(m.screens[0].choices, []string{"Open sign-in", "Choose another agent"}) {
		t.Errorf("screens = %+v, want Codex's sign-in alone, with another agent as its other choice", m.screens)
	}
	if want := []string{"sign in codex", "save codex"}; !slices.Equal(m.actions, want) {
		t.Errorf("actions = %q, want %q", m.actions, want)
	}
}

// Input that ends, or Escape at the choice, installs nothing, signs nobody in
// and remembers nothing, at whichever screen it happens.
func TestACancelledScreenNeverInstallsSignsInOrRemembers(t *testing.T) {
	for _, c := range []struct {
		at     string
		states map[string]State
	}{
		{"Choose the agent your CFO runs on", map[string]State{}},
		{"Install Claude Code", map[string]State{}},
		{"Sign in to Claude Code", map[string]State{"claude": SignedOut}},
		{"Claude Code's sign-in could not be verified", map[string]State{"claude": Unverified}},
	} {
		t.Run(c.at, func(t *testing.T) {
			// Arrange
			m := &machine{t: t, states: c.states}
			m.answer = func(shown screen) (int, error) {
				if shown.heading == c.at {
					return 0, ErrCancelled
				}
				return shown.selected, nil
			}

			// Act
			_, err := m.flow().Run(context.Background(), "", false)

			// Assert
			if !errors.Is(err, ErrCancelled) || len(m.actions) != 0 {
				t.Errorf("Run = %v after %q, want it cancelled with nothing done", err, m.actions)
			}
			if last := m.screens[len(m.screens)-1].heading; last != c.at {
				t.Errorf("the last screen was %q, want the flow to reach %q", last, c.at)
			}
		})
	}
}

// Escape at a step returns to the choice with nothing installed, and another
// agent can be chosen there.
func TestEscapeAtAStepReturnsToTheChoice(t *testing.T) {
	// Arrange
	m := &machine{t: t, states: map[string]State{"codex": Ready}}
	m.answer = func(shown screen) (int, error) {
		switch len(m.screens) {
		case 1:
			return 0, nil // Claude Code, which is not installed
		case 2:
			return 0, ErrBack
		default:
			return 1, nil // Codex
		}
	}

	// Act
	agent, err := m.flow().Run(context.Background(), "", false)

	// Assert
	if err != nil || agent != "codex" {
		t.Fatalf("Run = %q, %v; want codex", agent, err)
	}
	if want := []string{"Choose the agent your CFO runs on", "Install Claude Code", "Choose the agent your CFO runs on"}; !slices.Equal(m.headings(), want) {
		t.Errorf("screens = %q, want %q", m.headings(), want)
	}
	if want := []string{"save codex"}; !slices.Equal(m.actions, want) {
		t.Errorf("actions = %q, want %q", m.actions, want)
	}
}

// An install or sign-in that fails, or that changes nothing, stays on its
// screen saying so, with the same default to try again and no agent
// remembered.
func TestAStepThatDoesNotFinishStaysOnItsScreenAndSaysWhy(t *testing.T) {
	for _, c := range []struct {
		name    string
		fail    error
		problem string
	}{
		{"the sign-in fails", errors.New("exit status 1"), "That did not finish: exit status 1"},
		{"the sign-in changes nothing", nil, "That changed nothing: Sign-in needed."},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			m := &machine{t: t, states: map[string]State{"claude": SignedOut}}
			flow := m.flow()
			flow.Login = func(id string) error {
				m.actions = append(m.actions, "sign in "+id)
				return c.fail
			}
			var titles []string
			choose := flow.Choose
			flow.Choose = func(title string, choices []string, selected int) (int, error) {
				titles = append(titles, title)
				if len(titles) == 2 {
					return 0, ErrCancelled
				}
				return choose(title, choices, selected)
			}

			// Act
			_, err := flow.Run(context.Background(), "claude", false)

			// Assert
			if !errors.Is(err, ErrCancelled) || !slices.Equal(m.actions, []string{"sign in claude"}) {
				t.Fatalf("Run = %v after %q, want one sign-in and nothing remembered", err, m.actions)
			}
			if len(titles) != 2 || !strings.HasPrefix(titles[1], "Sign in to Claude Code\n") || !strings.HasSuffix(titles[1], "\n"+c.problem) {
				t.Errorf("the screen after the sign-in is %q, want the sign-in again ending with %q", titles, c.problem)
			}
		})
	}
}

// An agent whose sign-in cannot be verified is never called ready: its screen
// opens the sign-in by default, and continuing with it is the person's own
// choice, which remembers it.
func TestAnUnverifiedSignInIsContinuedOnlyByChoice(t *testing.T) {
	// Arrange
	m := &machine{t: t, states: map[string]State{"pi": Unverified}}
	m.answer = func(shown screen) (int, error) { return slices.Index(shown.choices, "Continue without verifying"), nil }

	// Act
	agent, err := m.flow().Run(context.Background(), "pi", false)

	// Assert
	if err != nil || agent != "pi" {
		t.Fatalf("Run = %q, %v; want pi", agent, err)
	}
	if len(m.screens) != 1 || m.screens[0].selected != 0 || !slices.Equal(m.screens[0].choices, []string{"Open sign-in", "Continue without verifying", "Choose another agent"}) {
		t.Errorf("screens = %+v, want one screen whose default opens the sign-in", m.screens)
	}
	if want := []string{"save pi"}; !slices.Equal(m.actions, want) {
		t.Errorf("actions = %q, want %q", m.actions, want)
	}
}

// The install screen names what Enter runs before it runs it.
func TestTheInstallScreenNamesWhatItRuns(t *testing.T) {
	for id, want := range map[string]string{
		"claude": "Install Claude Code\nNot installed. Enter runs the installer at https://claude.ai/install.ps1.",
		"codex":  "Install Codex\nNot installed. Enter runs npm install -g @openai/codex.",
		"pi":     "Install pi\nNot installed. Enter runs npm install -g @earendil-works/pi-coding-agent.",
	} {
		t.Run(id, func(t *testing.T) {
			// Arrange
			m := &machine{t: t, states: map[string]State{}}
			flow := m.flow()
			var title string
			flow.Choose = func(shown string, _ []string, _ int) (int, error) {
				title = shown
				return 0, ErrCancelled
			}

			// Act
			_, _ = flow.Run(context.Background(), id, false)

			// Assert
			if title != want {
				t.Errorf("the install screen is %q, want %q", title, want)
			}
		})
	}
}
