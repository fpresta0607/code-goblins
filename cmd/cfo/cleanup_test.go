package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

func cleanupTestRuntime(h home.Home, cleanup func(context.Context, home.Home, string, bool) (string, error)) commandRuntime {
	return commandRuntime{
		resolveHome: func() (home.Home, error) { return h, nil },
		cleanup:     cleanup,
	}
}

func primaryHomeFixture(t *testing.T) home.Home {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	return home.Home{Root: root, State: state, Data: filepath.Join(root, "data")}
}

func TestCleanupHelpPrintsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := runCleanup([]string{"--help"}, &stdout, &stderr, commandRuntime{})
	if exit != 0 || stderr.Len() != 0 {
		t.Fatalf("cleanup --help exit=%d stderr=%q, want 0", exit, stderr.String())
	}
	if !strings.Contains(stdout.String(), "usage: cfo cleanup") {
		t.Fatalf("cleanup --help stdout=%q, want usage line", stdout.String())
	}
}

func TestCleanupArgumentValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing id", nil, "task ID is required"},
		{"unknown flag", []string{"--force"}, `unknown flag "--force"`},
		{"extra arguments", []string{"g1", "g2"}, "unexpected arguments"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exit := runCleanup(test.args, &stdout, &stderr, commandRuntime{})
			if exit != 2 || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("exit=%d stderr=%q, want 2 with %q", exit, stderr.String(), test.want)
			}
		})
	}
}

func TestCleanupRefusesNonPrimaryHome(t *testing.T) {
	h := home.Home{Root: t.TempDir(), State: t.TempDir(), Data: t.TempDir()}
	called := false
	runtime := cleanupTestRuntime(h, func(context.Context, home.Home, string, bool) (string, error) {
		called = true
		return "", nil
	})

	var stdout, stderr bytes.Buffer
	exit := runCleanup([]string{"g1"}, &stdout, &stderr, runtime)
	if exit != 1 || !strings.Contains(stderr.String(), "not a primary home") {
		t.Fatalf("exit=%d stderr=%q, want non-primary refusal", exit, stderr.String())
	}
	if called {
		t.Fatal("cleanup service ran against a non-primary home")
	}
}

func TestCleanupDelegatesToService(t *testing.T) {
	h := primaryHomeFixture(t)
	var gotID string
	runtime := cleanupTestRuntime(h, func(_ context.Context, home home.Home, id string, _ bool) (string, error) {
		gotID = id
		if home.State != h.State {
			t.Errorf("home = %+v, want the resolved home", home)
		}
		return "cleaned g1 worktree=C:\\work\\g1", nil
	})

	var stdout, stderr bytes.Buffer
	exit := runCleanup([]string{"g1"}, &stdout, &stderr, runtime)
	if exit != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q, want success", exit, stderr.String())
	}
	if gotID != "g1" {
		t.Errorf("id = %q, want g1", gotID)
	}
	if stdout.String() != "cleaned g1 worktree=C:\\work\\g1\n" {
		t.Errorf("stdout=%q, want the service output", stdout.String())
	}
}

func TestCleanupSurfacesServiceFailure(t *testing.T) {
	h := primaryHomeFixture(t)
	runtime := cleanupTestRuntime(h, func(context.Context, home.Home, string, bool) (string, error) {
		return "", errors.New("cleanup: worktree is dirty")
	})

	var stdout, stderr bytes.Buffer
	exit := runCleanup([]string{"g1"}, &stdout, &stderr, runtime)
	if exit != 1 || !strings.Contains(stderr.String(), "worktree is dirty") || stdout.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q, want the service refusal on stderr", exit, stdout.String(), stderr.String())
	}
}
