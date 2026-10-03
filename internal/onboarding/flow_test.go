package onboarding

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// screen is one screen a test's flow showed: its title, its choices and its
// default, and for a row of tabs each choice's mark and note.
type screen struct {
	heading  string
	choices  []string
	selected int
	tabs     bool
	marks    []string
	notes    []string
}

// machine is a test's agents by id, and a flow over them that records every
// screen, install, sign-in and save, and every finished line as "name:
// answer", with "undo" where the lines were taken back. answer picks each
// screen's choice; with none every screen's default is accepted, as Enter
// does.
type machine struct {
	t       *testing.T
	states  map[string]State
	answer  func(shown screen) (int, error)
	screens []screen
	actions []string
	probes  []string
	lines   []string
}

// titled is a step's title and the lines under it as one text.
func titled(step Step) string {
	if step.Detail == "" {
		return step.Title
	}
	return step.Title + "\n" + step.Detail
}

// asked is an Ask that answers by a step's whole title, its choices' labels
// and its default.
func asked(answer func(title string, choices []string, selected int) (int, error)) func(Step) (int, error) {
	return func(step Step) (int, error) {
		labels := make([]string, len(step.Choices))
		for index, choice := range step.Choices {
			labels[index] = choice.Label
		}
		return answer(titled(step), labels, step.Selected)
	}
}

func (m *machine) flow() Flow {
	return Flow{
		Detect: func(_ context.Context, id string) Agent {
			m.probes = append(m.probes, id)
			reason := map[State]string{Missing: "Not installed", Shadowed: "npm's claude.cmd comes first on PATH", SignedOut: "Sign-in needed", Unverified: "Sign-in could not be verified"}[m.states[id]]
			return Agent{ID: id, Name: agentNames[id], State: m.states[id], Reason: reason}
		},
		Ask: func(step Step) (int, error) {
			shown := screen{heading: step.Title, selected: step.Selected, tabs: step.Tabs}
			for _, choice := range step.Choices {
				shown.choices = append(shown.choices, choice.Label)
				shown.marks = append(shown.marks, choice.Mark)
				shown.notes = append(shown.notes, choice.Note)
			}
			m.screens = append(m.screens, shown)
			if m.answer != nil {
				return m.answer(shown)
			}
			return step.Selected, nil
		},
		Done:  func(name, answer string) { m.lines = append(m.lines, name+": "+answer) },
		Undo:  func() { m.lines = append(m.lines, "undo") },
		Marks: MarksFor(true).Agents,
		// What the table of what is proved says of each agent, as a caller
		// hands it in.
		Recommended: "claude",
		Notes:       map[string]string{"claude": "the best experience", "codex": "woken by a typed line", "pi": "woken by a typed line"},
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

// The choice starts on the remembered agent, or else on Claude Code, the one
// it recommends, however ready the others are: the default Enter takes is
// always the marked one. A scratch profile showed Codex ready, from a sign-in
// kept outside the profile, while Claude Code was not, and the choice started
// on Codex beside Claude Code's recommended mark.
func TestTheChoiceStartsOnTheRememberedAgentOrTheRecommendedOne(t *testing.T) {
	for _, c := range []struct {
		name   string
		states map[string]State
		saved  string
		want   int
	}{
		{"nothing ready", map[string]State{}, "", 0},
		{"only codex ready", map[string]State{"codex": Ready}, "", 0},
		{"codex and pi ready", map[string]State{"codex": Ready, "pi": Ready}, "", 0},
		{"all ready", map[string]State{"claude": Ready, "codex": Ready, "pi": Ready}, "", 0},
		{"pi remembered though claude is ready", map[string]State{"claude": Ready, "pi": Ready}, "pi", 2},
		{"a remembered name that is no agent", map[string]State{"pi": Ready}, "kimi", 0},
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

// The choice of agent is one control, a row of tabs: each agent's own mark
// and name, Claude Code's tab marked as the recommended one, and under the
// row how ready the marked agent is, whether it is the remembered one, and
// what a Codex or pi CFO goes without.
func TestTheChoiceIsOneRowOfTabsThatSaysHowReadyEachAgentIs(t *testing.T) {
	// Arrange
	m := &machine{t: t, states: map[string]State{"claude": Ready, "codex": SignedOut}, answer: func(screen) (int, error) { return 0, ErrCancelled }}

	// Act
	_, _ = m.flow().Run(context.Background(), "codex", true)

	// Assert
	if len(m.screens) != 1 || !m.screens[0].tabs {
		t.Fatalf("the choice shows %+v, want one row of tabs", m.screens)
	}
	shown := m.screens[0]
	if want := []string{"Claude Code (recommended)", "Codex", "pi"}; !slices.Equal(shown.choices, want) {
		t.Errorf("the tabs are %q, want %q", shown.choices, want)
	}
	if want := []string{"✻", ">_", "π"}; !slices.Equal(shown.marks, want) {
		t.Errorf("the tabs' marks are %q, want each agent's own %q", shown.marks, want)
	}
	want := []string{
		"Ready · the best experience",
		"Sign-in needed · woken by a typed line · current",
		"Not installed · woken by a typed line",
	}
	if !slices.Equal(shown.notes, want) {
		t.Errorf("the tabs' notes are %q, want %q", shown.notes, want)
	}
}

// Every step finished is one line, its name and its answer, so the screen
// holds what was answered and the one step that waits: from nothing, the
// agent, its install and its sign-in.
func TestEveryFinishedStepIsOneLine(t *testing.T) {
	for _, c := range []struct {
		name   string
		states map[string]State
		saved  string
		want   []string
	}{
		{"from nothing", map[string]State{}, "", []string{"undo", "Agent: Claude Code", "Install: Claude Code is installed", "Sign-in: Claude Code's own sign-in finished"}},
		{"a remembered agent that is ready", map[string]State{"pi": Ready}, "pi", []string{"Agent: pi"}},
		{"a remembered agent that is signed out", map[string]State{"codex": SignedOut}, "codex", []string{"Agent: Codex", "Sign-in: Codex's own sign-in finished"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			m := &machine{t: t, states: c.states}

			// Act
			_, err := m.flow().Run(context.Background(), c.saved, false)

			// Assert
			if err != nil || !slices.Equal(m.lines, c.want) {
				t.Errorf("Run = %v with lines %q, want %q", err, m.lines, c.want)
			}
		})
	}
}

// A step back to the choice takes the finished lines with it, so the agent
// left behind is not still on the screen beside the one chosen next. A
// sign-in continued without verifying says so.
func TestAStepBackTakesTheFinishedLinesWithIt(t *testing.T) {
	// Arrange
	m := &machine{t: t, states: map[string]State{"codex": Unverified}}
	m.answer = func(shown screen) (int, error) {
		switch len(m.screens) {
		case 1:
			return 0, nil // Claude Code, which is not installed
		case 2:
			return 0, ErrBack
		case 3:
			return 1, nil // Codex
		default:
			return slices.Index(shown.choices, "Continue without verifying"), nil
		}
	}

	// Act
	agent, err := m.flow().Run(context.Background(), "", false)

	// Assert
	if err != nil || agent != "codex" {
		t.Fatalf("Run = %q, %v; want codex", agent, err)
	}
	if want := []string{"undo", "Agent: Claude Code", "undo", "Agent: Codex", "Sign-in: not verified; continuing as you chose"}; !slices.Equal(m.lines, want) {
		t.Errorf("lines = %q, want %q", m.lines, want)
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
			ask := flow.Ask
			flow.Ask = func(step Step) (int, error) {
				titles = append(titles, titled(step))
				if len(titles) == 2 {
					return 0, ErrCancelled
				}
				return ask(step)
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
			flow.Ask = func(step Step) (int, error) {
				title = titled(step)
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

// Claude Code shadowed by npm's script is never reinstalled, which would
// change nothing: its screen names the uninstall, Enter checks again, and
// once npm's script is gone the flow carries on to the sign-in.
func TestAShadowedClaudeCodeIsCheckedAgainNeverReinstalled(t *testing.T) {
	// Arrange
	m := &machine{t: t, states: map[string]State{"claude": Shadowed}}
	m.answer = func(shown screen) (int, error) {
		if shown.heading == "Remove npm's Claude Code" && len(m.screens) == 3 {
			m.states["claude"] = SignedOut // the person ran the uninstall
		}
		return shown.selected, nil
	}

	// Act
	agent, err := m.flow().Run(context.Background(), "", false)

	// Assert
	if err != nil || agent != "claude" {
		t.Fatalf("Run = %q, %v; want claude", agent, err)
	}
	if want := []string{"Choose the agent your CFO runs on", "Remove npm's Claude Code", "Remove npm's Claude Code", "Sign in to Claude Code"}; !slices.Equal(m.headings(), want) {
		t.Errorf("screens = %q, want %q", m.headings(), want)
	}
	if want := []string{"Check again", "Choose another agent"}; !slices.Equal(m.screens[1].choices, want) {
		t.Errorf("the shadowed screen offers %q, want %q", m.screens[1].choices, want)
	}
	if want := []string{"sign in claude", "save claude"}; !slices.Equal(m.actions, want) {
		t.Errorf("actions = %q, want %q with no install", m.actions, want)
	}
}

// Escape at the first choice cancels in the quick start's own words, with
// nothing internal before them.
func TestEscapeAtTheChoiceCancelsInTheQuickStartsWords(t *testing.T) {
	// Arrange
	m := &machine{t: t, states: map[string]State{}, answer: func(screen) (int, error) { return 0, ErrBack }}

	// Act
	_, err := m.flow().Run(context.Background(), "", false)

	// Assert
	if !errors.Is(err, ErrCancelled) || !errors.Is(err, ErrBack) || err.Error() != "setup was cancelled; run goblins to continue" {
		t.Errorf("Run error = %q, want the cancel in ErrCancelled's words", err)
	}
}

// The uninstall that frees a shadowed Claude Code is shown at the end of a
// line of its own on every screen that names it, so a person who copies it
// never copies a period or a word with it: on the install screen, on the
// shadowed screen, and again after Check again changes nothing.
func TestTheUninstallCommandIsNeverFollowedByAnything(t *testing.T) {
	for _, c := range []struct {
		name     string
		isNative bool
	}{
		{"no native build", false},
		{"the native build shadowed", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			directory := t.TempDir()
			if c.isNative {
				if err := os.WriteFile(filepath.Join(directory, "claude.exe"), nil, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			detector := Detector{ClaudeDirectory: directory, LookPath: func(name string) (string, error) { return name + ".cmd", nil }}
			var shown []string
			flow := Flow{
				Detect: detector.Detect,
				Ask: asked(func(title string, choices []string, selected int) (int, error) {
					shown = append(shown, title)
					shown = append(shown, choices...)
					if strings.HasPrefix(title, "Choose the agent") {
						return selected, nil
					}
					if strings.Contains(title, "That changed nothing") || strings.Contains(title, "That did not finish") {
						return 0, ErrCancelled
					}
					return 0, nil
				}),
				Done:    func(string, string) {},
				Undo:    func() {},
				Install: func(string) error { return nil },
			}

			// Act
			_, err := flow.Run(context.Background(), "claude", false)

			// Assert
			if !errors.Is(err, ErrCancelled) {
				t.Fatalf("Run error = %v, want the test's own cancel", err)
			}
			named := 0
			for _, text := range shown {
				for rest := text; strings.Contains(rest, "@anthropic-ai/claude-code"); {
					_, rest, _ = strings.Cut(rest, "@anthropic-ai/claude-code")
					named++
					if rest != "" && !strings.HasPrefix(rest, "\n") {
						t.Errorf("the uninstall is followed by %q in %q", rest, text)
					}
				}
			}
			if named < 2 {
				t.Errorf("the uninstall was named %d times in %q, want it on the step and after it changed nothing", named, shown)
			}
		})
	}
}
