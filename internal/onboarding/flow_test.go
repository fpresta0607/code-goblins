package onboarding

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestSetupInstallsSignsInThenRemembersOnlyTheChosenAgent(t *testing.T) {
	var actions []string
	isInstalled, isSignedIn := false, false
	flow := Flow{
		Detect: func(_ context.Context, name string) Agent {
			return Agent{ID: name, Name: name, Installed: isInstalled, SignedIn: isSignedIn}
		},
		Choose: func(title string, choices []string, selected int) (int, error) {
			actions = append(actions, title)
			if selected != 0 {
				t.Fatalf("default = %d", selected)
			}
			return selected, nil
		},
		Install: func(name string) error { actions = append(actions, "install:"+name); isInstalled = true; return nil },
		Login:   func(name string) error { actions = append(actions, "login:"+name); isSignedIn = true; return nil },
		Save:    func(name string) error { actions = append(actions, "save:"+name); return nil },
	}

	got, err := flow.Run(context.Background(), "", false)

	if err != nil || got != "claude" {
		t.Fatalf("got %q, %v", got, err)
	}
	if !slices.Equal(actions, []string{"Choose your CFO", "Install claude\nInstaller: npm install -g @anthropic-ai/claude-code@2.1.284", "install:claude", "Sign in to claude", "login:claude", "save:claude"}) {
		t.Fatalf("actions: %q", actions)
	}
}

func TestSetupSkipsReadySavedAgentAndRerunOffersTheSavedDefault(t *testing.T) {
	for _, isRerun := range []bool{false, true} {
		choices := 0
		flow := Flow{
			Detect: func(_ context.Context, name string) Agent {
				return Agent{ID: name, Name: name, Installed: true, SignedIn: true}
			},
			Choose: func(_ string, _ []string, selected int) (int, error) {
				choices++
				if selected != 2 {
					t.Fatalf("default=%d", selected)
				}
				return selected, nil
			},
			Save: func(name string) error {
				if name != "pi" {
					t.Fatalf("saved=%s", name)
				}
				return nil
			},
		}
		got, err := flow.Run(context.Background(), "pi", isRerun)
		if err != nil || got != "pi" || (choices == 1) != isRerun {
			t.Fatalf("rerun=%t got=%s choices=%d err=%v", isRerun, got, choices, err)
		}
	}
}

func TestSetupCancellationNeverInstallsLogsInOrSaves(t *testing.T) {
	for _, stage := range []string{"Choose your CFO", "Install claude", "Sign in to claude"} {
		t.Run(stage, func(t *testing.T) {
			flow := Flow{
				Detect: func(_ context.Context, name string) Agent {
					return Agent{ID: name, Name: name, Installed: stage == "Sign in to claude"}
				},
				Choose: func(title string, _ []string, selected int) (int, error) {
					if strings.Split(title, "\n")[0] == stage {
						return 0, ErrCancelled
					}
					return selected, nil
				},
				Install: func(string) error { t.Fatal("unexpected install"); return nil },
				Login:   func(string) error { t.Fatal("unexpected login"); return nil },
				Save:    func(string) error { t.Fatal("unexpected save"); return nil },
			}
			_, err := flow.Run(context.Background(), "", false)
			if !errors.Is(err, ErrCancelled) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestSetupFailedSignInRemainsUnverifiedAndOffersRetry(t *testing.T) {
	var titles []string
	flow := Flow{
		Detect: func(_ context.Context, name string) Agent {
			return Agent{ID: name, Name: name, Installed: true, Reason: "Sign-in could not be verified"}
		},
		Choose: func(title string, _ []string, selected int) (int, error) {
			titles = append(titles, title)
			if len(titles) > 2 {
				return 0, ErrCancelled
			}
			return selected, nil
		},
		Login: func(string) error { return nil },
		Save:  func(string) error { t.Fatal("saved unverified agent"); return nil },
	}
	_, err := flow.Run(context.Background(), "", false)
	if !errors.Is(err, ErrCancelled) || len(titles) != 3 || titles[2] != "Sign in to claude\nSign-in could not be verified" {
		t.Fatalf("titles=%q err=%v", titles, err)
	}
}

func TestSetupEscapeReturnsToTheAgentChoiceWithoutInstalling(t *testing.T) {
	var titles []string
	flow := Flow{
		Detect: func(_ context.Context, name string) Agent {
			return Agent{ID: name, Name: name, Installed: name == "codex", SignedIn: name == "codex"}
		},
		Choose: func(title string, _ []string, _ int) (int, error) {
			titles = append(titles, title)
			switch len(titles) {
			case 1:
				return 0, nil
			case 2:
				return 0, ErrBack
			case 3:
				return 1, nil
			default:
				t.Fatal("setup did not return to the choice")
				return 0, ErrCancelled
			}
		},
		Install: func(string) error { t.Fatal("installed on Escape"); return nil },
		Save: func(name string) error {
			if name != "codex" {
				t.Fatalf("saved %s", name)
			}
			return nil
		},
	}
	name, err := flow.Run(context.Background(), "", false)
	if err != nil || name != "codex" || len(titles) != 3 || titles[2] != "Choose your CFO" {
		t.Fatalf("name=%s titles=%q error=%v", name, titles, err)
	}
}
