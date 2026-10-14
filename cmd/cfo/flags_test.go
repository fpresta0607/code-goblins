package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// A word left over after a command's flags, such as a value given without
// its flag or one that holds spaces left unquoted, refused the command with
// exit 2 and nothing said, which reads as a command that worked (#851). Every
// command that takes no further argument names the word and shows its flags.
func TestACommandRefusesAnArgumentItDoesNotTakeSayingWhich(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func(stdout, stderr io.Writer, runtime commandRuntime) int
	}{
		{"auth browser", func(stdout, stderr io.Writer, runtime commandRuntime) int {
			return runAuthBrowser([]string{"github", "stray"}, stdout, stderr, runtime)
		}},
		{"auth fetch", func(stdout, stderr io.Writer, runtime commandRuntime) int {
			return runAuthFetch([]string{"request-1", "stray"}, stdout, stderr, runtime)
		}},
		{"doctor", func(stdout, stderr io.Writer, runtime commandRuntime) int {
			return runDoctor([]string{"stray"}, stdout, stderr, runtime)
		}},
		{"deliver", func(stdout, stderr io.Writer, runtime commandRuntime) int {
			return runDeliver([]string{"stray"}, stdout, stderr, runtime)
		}},
		{"helper start", func(stdout, stderr io.Writer, runtime commandRuntime) int {
			return runHelperStart([]string{"cg-parent", "stray"}, stdout, stderr, runtime)
		}},
		{"pause", func(stdout, stderr io.Writer, runtime commandRuntime) int {
			return runLifecycle("pause", []string{"cg-task", "stray"}, stdout, stderr, runtime)
		}},
		{"native-hook", func(stdout, stderr io.Writer, runtime commandRuntime) int {
			return runNativeHook([]string{"claude", "stray"}, strings.NewReader(""), stdout, stderr, runtime)
		}},
		{"hooks", func(stdout, stderr io.Writer, runtime commandRuntime) int {
			return runNativeSetup([]string{"check", "claude", "stray"}, stdout, stderr, runtime)
		}},
		{"present", func(stdout, stderr io.Writer, runtime commandRuntime) int {
			return runPresent([]string{"stray"}, stdout, stderr, runtime)
		}},
		{"question", func(stdout, stderr io.Writer, runtime commandRuntime) int {
			return runQuestion([]string{"stray"}, stdout, stderr, runtime)
		}},
		{"register", func(stdout, stderr io.Writer, runtime commandRuntime) int {
			return runRegister([]string{"stray"}, stdout, stderr, runtime)
		}},
		{"review", func(stdout, stderr io.Writer, runtime commandRuntime) int {
			return runReview([]string{"stray"}, stdout, stderr, runtime)
		}},
		{"run-request", func(stdout, stderr io.Writer, runtime commandRuntime) int {
			return runRunRequest([]string{"stray"}, stdout, stderr, runtime)
		}},
		{"serve", func(stdout, stderr io.Writer, runtime commandRuntime) int {
			return runServe([]string{"stray"}, stdout, stderr, runtime)
		}},
		{"stop", func(stdout, stderr io.Writer, runtime commandRuntime) int {
			return runStop([]string{"stray"}, stdout, stderr, runtime)
		}},
		{"update", func(stdout, stderr io.Writer, runtime commandRuntime) int {
			return runUpdate([]string{"stray"}, stdout, stderr, runtime)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			runtime := commandRuntime{resolveHome: func() (home.Home, error) {
				t.Fatal("a refused command resolved the home")
				return home.Home{}, nil
			}}
			var stdout, stderr bytes.Buffer

			// Act
			exit := test.run(&stdout, &stderr, runtime)

			// Assert
			hasUsage := strings.Contains(stderr.String(), "  -") || strings.Contains(stderr.String(), "it takes no argument")
			if exit != 2 || !strings.Contains(stderr.String(), "cfo "+test.name+`: unexpected argument "stray"`) || !hasUsage {
				t.Fatalf("exit=%d stderr=%q, want 2 naming the stray argument, with the command's flags or that it takes none", exit, stderr.String())
			}
		})
	}
}
