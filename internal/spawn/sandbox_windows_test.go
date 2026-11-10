package spawn

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/harness"
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

// hostGitConfiguration is what env gives every git a sandbox task's
// terminal runs, read as git's documentation reads it: the pairs
// GIT_CONFIG_COUNT counts, GIT_CONFIG_KEY_<n> and GIT_CONFIG_VALUE_<n>, and
// the tree GIT_ATTR_SOURCE reads attributes from.
func hostGitConfiguration(env func(string) string) map[string]string {
	configuration := map[string]string{"GIT_ATTR_SOURCE": env("GIT_ATTR_SOURCE")}
	count, _ := strconv.Atoi(env("GIT_CONFIG_COUNT"))
	for index := range count {
		configuration[env("GIT_CONFIG_KEY_"+strconv.Itoa(index))] = env("GIT_CONFIG_VALUE_" + strconv.Itoa(index))
	}
	return configuration
}

// wantHostGit is the configuration a sandbox task's host git runs with:
// hooks from hooksOff, an empty folder, no fsmonitor, no recursion into a
// submodule, no folder it finds taken for a bare repository, and attributes
// from git's empty tree, whose id git publishes.
func wantHostGit(hooksOff string) map[string]string {
	return map[string]string{
		"core.hooksPath":        hooksOff,
		"core.fsmonitor":        "false",
		"submodule.recurse":     "false",
		"diff.ignoreSubmodules": "all",
		"safe.bareRepository":   "explicit",
		"GIT_ATTR_SOURCE":       "4b825dc642cb6eb9a060e54bf8d69288fbee4904",
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
			hooksOff := filepath.Join(meta.TaskTmp, "git-hooks-off")
			if info, err := os.Stat(hooksOff); err != nil || !info.IsDir() {
				t.Fatalf("the hooks folder %s = %v, want it made", hooksOff, err)
			}
			if got := hostGitConfiguration(func(name string) string { return env[name] }); !maps.Equal(got, wantHostGit(hooksOff)) {
				t.Fatalf("host git runs with %v, want %v", got, wantHostGit(hooksOff))
			}
			if !strings.Contains(sandboxInstruction(meta), "cfo sandbox run -- <command>") {
				t.Fatalf("instruction %q, want cfo sandbox run named", sandboxInstruction(meta))
			}
		})
	}
}

// sandboxWorktree gives worktree what a sandbox task's spawn reads of it: a
// devcontainer that names image, and the .git link git writes for a
// worktree, which it returns.
func sandboxWorktree(t *testing.T, worktree, image string) string {
	t.Helper()
	link := "gitdir: " + filepath.ToSlash(filepath.Join(filepath.Dir(worktree), "primary", ".git", "worktrees", "task-7"))
	writeFile(t, filepath.Join(worktree, ".git"), link+"\n")
	writeFile(t, filepath.Join(makeDir(t, filepath.Join(worktree, ".devcontainer")), "devcontainer.json"), `{"image": "`+image+`"}`)
	return link
}

// dockerFirstOnPath puts a docker command first on PATH, before any this
// machine has, and keeps the rest of PATH, which the fake harness is on.
func dockerFirstOnPath(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "docker.bat"), "@exit /b 0\r\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// A sandbox task's spawn records the image its devcontainer names and its
// worktree's .git link, and starts its goblin with host git held off what
// the container could plant and told to run its commands in the container.
func TestASandboxSpawnRecordsItsImageAndHoldsItsHostGit(t *testing.T) {
	// Arrange
	f := newQuickFixture(t)
	briefAsking(t, f.fixture, "credentials: none\nsandbox: needed")
	dockerFirstOnPath(t)
	link := sandboxWorktree(t, f.worktree, "mcr.microsoft.com/devcontainers/go:1")

	// Act
	result, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	record, err := state.ReadMeta(state.TaskMetaPath(f.stateDir, f.request.ID))
	if err != nil {
		t.Fatal(err)
	}
	if record["sandbox_image"] != "mcr.microsoft.com/devcontainers/go:1" || record["sandbox_git"] != link {
		t.Fatalf("record sandbox_image = %q, sandbox_git = %q, want the devcontainer's image and %q", record["sandbox_image"], record["sandbox_git"], link)
	}
	env := named(f.events(t), "env")[0].Env
	hooksOff := filepath.Join(result.Meta.TaskTmp, "git-hooks-off")
	if got := hostGitConfiguration(func(name string) string { return value(env[name]) }); !maps.Equal(got, wantHostGit(hooksOff)) {
		t.Fatalf("the goblin's git runs with %v, want %v", got, wantHostGit(hooksOff))
	}
	if _, written := typedBrief(t, f.fixture, f.request.ID); !strings.Contains(written, "cfo sandbox run -- <command>") {
		t.Fatalf("instruction = %q, want cfo sandbox run named", written)
	}
}

// A relaunch builds its terminal's environment again, so a sandbox task's
// host git is held off what the container could plant only if the relaunch
// sets it too, and its new goblin is told how its commands run.
func TestASandboxTasksRelaunchHoldsItsHostGit(t *testing.T) {
	// Arrange
	f := newSwitchFixture(t, harness.Control{StopCommand: "/exit"})
	f.meta.SandboxImage, f.meta.SandboxGitLink = "node:22", sandboxWorktree(t, f.worktree, "node:22")
	if err := state.WriteTaskMeta(f.stateDir, f.meta); err != nil {
		t.Fatal(err)
	}

	// Act
	if _, err := f.service.Switch(context.Background(), SwitchRequest{ID: f.meta.ID, Model: "gpt-9"}); err != nil {
		t.Fatalf("Switch: %v", err)
	}

	// Assert
	env := named(f.events(t), "env")[0].Env
	hooksOff := filepath.Join(f.meta.TaskTmp, "git-hooks-off")
	if got := hostGitConfiguration(func(name string) string { return value(env[name]) }); !maps.Equal(got, wantHostGit(hooksOff)) {
		t.Fatalf("the new goblin's git runs with %v, want %v", got, wantHostGit(hooksOff))
	}
	if _, written := typedBrief(t, f.fixture, f.meta.ID); !strings.Contains(written, "cfo sandbox run -- <command>") {
		t.Fatalf("instruction = %q, want cfo sandbox run named", written)
	}
}
