package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TaskLabel is the label every container of a task carries, with the task's
// id, so docker ps --filter label=<TaskLabel>=<task> names each of them.
const TaskLabel = "code-goblins.sandbox"

// The folders a container sees: the checkout it works in and its scratch
// folder, and nothing else of the host.
const (
	WorkDir    = "/work"
	ScratchDir = "/scratch"
)

// Run is one command a container runs for a task.
type Run struct {
	// Task is the task the container runs for.
	Task string
	// Name is the container's name, unique to this run.
	Name string
	// Image is the image it runs, the project's devcontainer's.
	Image string
	// Checkout is the folder it works in, mounted at WorkDir: the task's
	// worktree, or a gate's checkout of it.
	Checkout string
	// Scratch is the folder it writes its temporary files in, mounted at
	// ScratchDir, empty for none.
	Scratch string
	// Cwd is the folder of the checkout the command runs in.
	Cwd string
	// Command is the program and its arguments the container runs.
	Command []string
}

// DockerArguments are the arguments of the docker run that runs r, after
// docker itself. The container is removed when it ends, keeps no capability
// to gain privileges, and sees the checkout and the scratch folder alone,
// with the checkout's .git, the link to the home's git folder or a gate's
// own git folder, mounted read-only over itself, so nothing in the container
// can make host git read another repository. It is given no variable of the
// host's: no credential reaches it.
func DockerArguments(r Run) ([]string, error) {
	if len(r.Command) == 0 {
		return nil, fmt.Errorf("sandbox: name the command to run in the container after --")
	}
	if !imagePattern.MatchString(r.Image) {
		return nil, fmt.Errorf("sandbox: %q is not one image reference", r.Image)
	}
	if strings.ContainsAny(r.Scratch, `,"`) {
		return nil, fmt.Errorf("sandbox: the scratch folder %s has a comma or a quote in its path, which docker's --mount cannot name", r.Scratch)
	}
	workDir, err := containerDir(r.Checkout, r.Cwd)
	if err != nil {
		return nil, err
	}
	args := []string{"run", "--rm", "--init", "--name", r.Name, "--label", TaskLabel + "=" + r.Task, "--security-opt", "no-new-privileges",
		"--mount", bindMount(r.Checkout, WorkDir, false)}
	if _, err := os.Lstat(filepath.Join(r.Checkout, ".git")); err == nil {
		args = append(args, "--mount", bindMount(filepath.Join(r.Checkout, ".git"), WorkDir+"/.git", true))
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("sandbox: read the checkout's .git: %w", err)
	}
	if r.Scratch != "" {
		args = append(args, "--mount", bindMount(r.Scratch, ScratchDir, false), "--env", "TMPDIR="+ScratchDir)
	}
	args = append(args, "--workdir", workDir, r.Image)
	return append(args, r.Command...), nil
}

// bindMount is a --mount value that binds source on the host at target.
// Docker reads the value as comma-separated fields, so a source with a comma
// or a quote is one it cannot be given.
func bindMount(source, target string, isReadOnly bool) string {
	mount := "type=bind,source=" + source + ",target=" + target
	if isReadOnly {
		mount += ",readonly"
	}
	return mount
}

// containerDir is where in the container cwd, a folder of checkout, is.
func containerDir(checkout, cwd string) (string, error) {
	if strings.ContainsAny(checkout, `,"`) {
		return "", fmt.Errorf("sandbox: the checkout %s has a comma or a quote in its path, which docker's --mount cannot name", checkout)
	}
	if cwd == "" {
		return WorkDir, nil
	}
	relative, err := filepath.Rel(checkout, cwd)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("sandbox: %s is outside the checkout %s, so the container cannot run there", cwd, checkout)
	}
	if relative == "." {
		return WorkDir, nil
	}
	return WorkDir + "/" + filepath.ToSlash(relative), nil
}

// ShellCommand is the command a gate's Test step gives as cmd.exe /c
// <string>, as a container's shell runs it: sh -c <string>. Any other
// command runs as it is.
func ShellCommand(command []string) []string {
	if len(command) == 3 && strings.EqualFold(strings.TrimSuffix(filepath.Base(command[0]), ".exe"), "cmd") && strings.EqualFold(command[1], "/c") {
		return []string{"sh", "-c", command[2]}
	}
	return command
}
