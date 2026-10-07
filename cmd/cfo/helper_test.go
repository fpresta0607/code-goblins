package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

func TestHelperStartHandsTheBriefsTextToTheSupervisor(t *testing.T) {
	// Arrange
	runtime := testCommandRuntime(t)
	var asked supervisor.HelperRequest
	runtime.requestHelper = func(_ home.Home, request supervisor.HelperRequest) (supervisor.HelperStart, error) {
		asked = request
		return supervisor.HelperStart{ID: "g1-h1", Branch: "feat/x-h1"}, nil
	}
	brief := filepath.Join(t.TempDir(), "helper.md")
	if err := os.WriteFile(brief, []byte("Write the migration.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	// Act
	exit := runWithRuntime([]string{"helper", "start", "g1", "--brief", brief, "--title", "  Accounts   migration "}, &stdout, &stderr, runtime)

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, stderr = %s", exit, stderr.String())
	}
	if asked != (supervisor.HelperRequest{Parent: "g1", Brief: "Write the migration.\n", Title: "Accounts migration"}) {
		t.Errorf("asked %+v, want g1's brief and its title", asked)
	}
	for _, want := range []string{"starting helper g1-h1 on branch feat/x-h1", "--waiting-on g1-h1"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout %q does not say %q", stdout.String(), want)
		}
	}
}

func TestHelperStartSaysWhyTheSupervisorRefused(t *testing.T) {
	// Arrange
	runtime := testCommandRuntime(t)
	runtime.requestHelper = func(home.Home, supervisor.HelperRequest) (supervisor.HelperStart, error) {
		return supervisor.HelperStart{}, errors.New("Only 3.1 GB of memory is free; ask again in ten minutes")
	}
	brief := filepath.Join(t.TempDir(), "helper.md")
	if err := os.WriteFile(brief, []byte("Write the migration.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	// Act
	exit := runWithRuntime([]string{"helper", "start", "g1", "--brief", brief}, &stdout, &stderr, runtime)

	// Assert
	if exit != 1 || !strings.Contains(stderr.String(), "not started: Only 3.1 GB of memory is free; ask again in ten minutes") {
		t.Errorf("exit = %d, stderr = %q; want the refusal and when to ask again", exit, stderr.String())
	}
}

func TestHelperStartRefusesWhatItCannotSend(t *testing.T) {
	brief := filepath.Join(t.TempDir(), "helper.md")
	if err := os.WriteFile(brief, []byte("Write the migration.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		args []string
		exit int
		want string
	}{
		{"no task", []string{"helper", "start"}, 2, "your task ID comes first"},
		{"no brief", []string{"helper", "start", "g1"}, 2, "--brief <file> is required"},
		{"a title on two lines", []string{"helper", "start", "g1", "--brief", brief, "--title", "two\nlines"}, 2, "a title is one line"},
		{"a brief that is not there", []string{"helper", "start", "g1", "--brief", brief + ".missing"}, 1, "helper.md.missing"},
		{"an unknown subcommand", []string{"helper", "begin"}, 2, `unknown subcommand "begin"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			runtime := testCommandRuntime(t)
			isAsked := false
			runtime.requestHelper = func(home.Home, supervisor.HelperRequest) (supervisor.HelperStart, error) {
				isAsked = true
				return supervisor.HelperStart{}, nil
			}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runWithRuntime(c.args, &stdout, &stderr, runtime)

			// Assert
			if exit != c.exit || !strings.Contains(stderr.String(), c.want) || isAsked {
				t.Errorf("exit = %d, stderr = %q, asked = %t; want exit %d naming %q and nothing asked", exit, stderr.String(), isAsked, c.exit, c.want)
			}
		})
	}
}
