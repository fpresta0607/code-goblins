package spawn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/sandbox"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// dockerOnPath leaves PATH holding only a folder with a docker command when
// isPresent, or no docker at all.
func dockerOnPath(t *testing.T, isPresent bool) {
	t.Helper()
	dir := t.TempDir()
	if isPresent {
		writeFile(t, filepath.Join(dir, "docker.bat"), "@exit /b 0\r\n")
	}
	t.Setenv("PATH", dir)
	t.Setenv("PATHEXT", ".BAT")
}

// A brief that asks for a container on a machine whose docker cannot be
// found is refused before anything starts, and never starts on the host.
func TestASandboxBriefIsRefusedWhereDockerIsMissing(t *testing.T) {
	// Arrange
	f := newFixture(t)
	briefAsking(t, f, "credentials: none\nsandbox: needed")
	dockerOnPath(t, false)

	// Act
	_, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "docker is not on PATH") {
		t.Fatalf("Spawn = %v, want the start refused for docker", err)
	}
	if _, readErr := state.ReadTaskMeta(f.stateDir, f.request.ID); !os.IsNotExist(readErr) {
		t.Errorf("the refused start left a task record: %v", readErr)
	}
}

func TestSandboxNeededReadsTheBriefAndAHelpersParent(t *testing.T) {
	for _, test := range []struct {
		name            string
		brief           string
		parentImage     string
		isDockerPresent bool
		shouldBeNeeded  bool
		refusal         string
	}{
		{name: "a brief without the line", brief: "## Authentication\ncredentials: none\n", isDockerPresent: true},
		{name: "a brief with the line", brief: "## Authentication\nsandbox: needed\n", isDockerPresent: true, shouldBeNeeded: true},
		{name: "a helper of a sandbox task", brief: "## Authentication\ncredentials: none\n", parentImage: "node:22", isDockerPresent: true, shouldBeNeeded: true},
		{name: "a brief with the line and no docker", brief: "## Authentication\nsandbox: needed\n", refusal: "docker is not on PATH"},
		{name: "a brief asking for something else", brief: "## Authentication\nsandbox: maybe\n", isDockerPresent: true, refusal: "sandbox"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			if err := state.WriteTaskMeta(stateDir, state.TaskMeta{ID: "parent-1", Kind: "ship", Mode: "no-mistakes", SandboxImage: test.parentImage}); err != nil {
				t.Fatal(err)
			}
			dockerOnPath(t, test.isDockerPresent)

			// Act
			isNeeded, err := Service{StateDir: stateDir}.sandboxNeeded(Request{ID: "helper-1", Parent: "parent-1"}, []byte(test.brief))

			// Assert
			if test.refusal != "" {
				if err == nil || !strings.Contains(err.Error(), test.refusal) {
					t.Fatalf("sandboxNeeded = %v, %v, want a refusal saying %q", isNeeded, err, test.refusal)
				}
				return
			}
			if err != nil || isNeeded != test.shouldBeNeeded {
				t.Fatalf("sandboxNeeded = %v, %v, want %v", isNeeded, err, test.shouldBeNeeded)
			}
		})
	}
}

func TestContainWorktreeRecordsTheImageAndTheGitLink(t *testing.T) {
	for _, test := range []struct {
		name, devcontainer, refusal string
	}{
		{name: "an image", devcontainer: `{"image": "mcr.microsoft.com/devcontainers/go:1"}`},
		{name: "a built image", devcontainer: `{"build": {"dockerfile": "Dockerfile"}}`, refusal: "builds its image"},
		{name: "no devcontainer", refusal: "declares no devcontainer"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			worktree := t.TempDir()
			writeFile(t, filepath.Join(worktree, ".git"), "gitdir: C:/home/project/.git/worktrees/cg-x\n")
			if test.devcontainer != "" {
				writeFile(t, filepath.Join(makeDir(t, filepath.Join(worktree, ".devcontainer")), "devcontainer.json"), test.devcontainer)
			}
			var meta state.TaskMeta

			// Act
			err := containWorktree(&meta, worktree)

			// Assert
			if test.refusal != "" {
				if err == nil || !strings.Contains(err.Error(), test.refusal) || meta.SandboxImage != "" {
					t.Fatalf("containWorktree = %v with image %q, want a refusal saying %q and no image", err, meta.SandboxImage, test.refusal)
				}
				return
			}
			if err != nil || meta.SandboxImage != "mcr.microsoft.com/devcontainers/go:1" || meta.SandboxGitLink != "gitdir: C:/home/project/.git/worktrees/cg-x" {
				t.Fatalf("containWorktree = %v, record %q %q", err, meta.SandboxImage, meta.SandboxGitLink)
			}
		})
	}
}

func TestContainEnvironmentTurnsOffWhatTheWorktreeCouldPlantForHostGit(t *testing.T) {
	for _, test := range []struct {
		name      string
		image     string
		isChanged bool
	}{
		{name: "a sandbox task", image: "node:22", isChanged: true},
		{name: "every other task"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			meta := state.TaskMeta{SandboxImage: test.image, TaskTmp: t.TempDir()}
			env := map[string]string{}

			// Act
			err := containEnvironment(env, meta)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if !test.isChanged {
				if len(env) != 0 || sandboxInstruction(meta) != "" {
					t.Fatalf("env = %v, instruction %q, want neither", env, sandboxInstruction(meta))
				}
				return
			}
			hooksOff := filepath.Join(meta.TaskTmp, sandbox.HooksOffDir)
			if info, err := os.Stat(hooksOff); err != nil || !info.IsDir() {
				t.Fatalf("the hooks folder %s = %v, want it made", hooksOff, err)
			}
			for name, value := range sandbox.HostGitEnvironment(hooksOff) {
				if env[name] != value {
					t.Errorf("%s = %q, want %q", name, env[name], value)
				}
			}
			if env[sandbox.Variable] != sandbox.Needed || !strings.Contains(sandboxInstruction(meta), "cfo sandbox run -- <command>") {
				t.Fatalf("env = %v, instruction %q, want the sandbox named in both", env, sandboxInstruction(meta))
			}
		})
	}
}
