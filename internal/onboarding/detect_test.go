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

func TestDetectionUsesEachAgentsAuthenticationStatus(t *testing.T) {
	for _, test := range []struct {
		name       string
		args       []string
		output     string
		code       int
		isSignedIn bool
	}{
		{"claude", []string{"auth", "status", "--json"}, `{"loggedIn":true}`, 0, true},
		{"claude", []string{"auth", "status", "--json"}, `{"loggedIn":false}`, 1, false},
		{"codex", []string{"login", "status"}, "Logged in using ChatGPT", 0, true},
		{"codex", []string{"login", "status"}, "Not logged in", 1, false},
		{"pi", []string{"auth", "check", "--provider", "anthropic", "--json", "--no-refresh"}, `{"status":"ready"}`, 0, true},
		{"pi", []string{"auth", "check", "--provider", "anthropic", "--json", "--no-refresh"}, `{"status":"not_ready"}`, 1, false},
	} {
		t.Run(test.name+test.output, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.WriteFile(filepath.Join(directory, "settings.json"), []byte(`{"defaultProvider":"anthropic"}`), 0600); err != nil {
				t.Fatal(err)
			}
			detector := Detector{
				PiDirectory: directory,
				LookPath:    func(name string) (string, error) { return name + ".exe", nil },
				Probe: func(ctx context.Context, name string, args ...string) (execx.Result, error) {
					if name != test.name || !slices.Equal(args, test.args) {
						t.Fatalf("probe %s %q, want %s %q", name, args, test.name, test.args)
					}
					if _, ok := ctx.Deadline(); !ok {
						t.Error("probe has no deadline")
					}
					return execx.Result{Stdout: []byte(test.output), ExitCode: test.code}, nil
				},
			}

			agent := detector.Detect(context.Background(), test.name)

			if !agent.Installed || agent.SignedIn != test.isSignedIn {
				t.Fatalf("agent=%+v", agent)
			}
		})
	}
}

func TestDetectionDoesNotMistakeMissingOrBrokenToolsForSignedIn(t *testing.T) {
	for _, name := range []string{"claude", "codex", "pi"} {
		t.Run(name, func(t *testing.T) {
			detector := Detector{LookPath: func(string) (string, error) { return "", exec.ErrNotFound }, Probe: func(context.Context, string, ...string) (execx.Result, error) {
				t.Fatal("probed absent tool")
				return execx.Result{}, nil
			}}
			agent := detector.Detect(context.Background(), name)
			if agent.Installed || agent.SignedIn || agent.Reason != "Not installed" {
				t.Fatalf("agent=%+v", agent)
			}
			detector.LookPath = func(name string) (string, error) { return name + ".exe", nil }
			detector.PiDirectory = t.TempDir()
			if err := os.WriteFile(filepath.Join(detector.PiDirectory, "settings.json"), []byte(`{"defaultProvider":"anthropic"}`), 0600); err != nil {
				t.Fatal(err)
			}
			detector.Probe = func(context.Context, string, ...string) (execx.Result, error) {
				return execx.Result{}, errors.New("secret diagnostic must not be displayed")
			}
			agent = detector.Detect(context.Background(), name)
			if !agent.Installed || agent.SignedIn || agent.Reason == "" || agent.Reason == "secret diagnostic must not be displayed" {
				t.Fatalf("agent=%+v", agent)
			}
		})
	}
}

func TestDetectionRejectsMalformedOrUnrecognizedAuthReplies(t *testing.T) {
	for _, name := range []string{"claude", "codex", "pi"} {
		for _, output := range []string{"", "{}", "unexpected response", `{"loggedIn":"true"}`} {
			t.Run(name+output, func(t *testing.T) {
				detector := Detector{LookPath: func(name string) (string, error) { return name + ".exe", nil }, Probe: func(context.Context, string, ...string) (execx.Result, error) {
					return execx.Result{Stdout: []byte(output)}, nil
				}}
				detector.PiDirectory = t.TempDir()
				if err := os.WriteFile(filepath.Join(detector.PiDirectory, "settings.json"), []byte(`{"defaultProvider":"anthropic"}`), 0600); err != nil {
					t.Fatal(err)
				}
				agent := detector.Detect(context.Background(), name)
				if agent.SignedIn || agent.Reason != "Sign-in could not be verified" {
					t.Fatalf("agent=%+v", agent)
				}
			})
		}
	}
}

func TestRecommendationKeepsTheSavedChoiceThenPrefersAReadyAgent(t *testing.T) {
	agents := []Agent{{ID: "claude"}, {ID: "codex", Installed: true, SignedIn: true}, {ID: "pi", Installed: true, SignedIn: true}}
	for _, test := range []struct {
		saved string
		want  int
	}{{"", 1}, {"pi", 2}, {"claude", 0}, {"bad", 1}} {
		if got := Recommended(agents, test.saved); got != test.want {
			t.Errorf("saved=%q got=%d want=%d", test.saved, got, test.want)
		}
	}
}
