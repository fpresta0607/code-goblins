package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
)

// startCFOSession brings the CFO's session to the front. When the board
// reaches no live CFO it starts Claude Code as the CFO in Herdr, in the
// project this terminal is in or one the Overlord picks, and then hands the
// terminal to herdr, which attaches with the CFO's tab in front. Inside a
// Herdr pane there is nothing to attach, so it only says where the CFO is.
func startCFOSession(ctx context.Context, runtime commandRuntime, cfoLive bool, stdout, stderr io.Writer) int {
	if !cfoLive {
		project, err := pickProject(ctx, runtime, stdout)
		if err != nil {
			fmt.Fprintf(stderr, "goblins: %v\n", err)
			return 1
		}
		if err := runtime.startCFO(ctx, project); err != nil {
			fmt.Fprintf(stderr, "goblins: the CFO session could not be started in Herdr: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "\nThe CFO starts in %s.\n", project)
	}
	if os.Getenv("HERDR_PANE_ID") != "" {
		fmt.Fprintln(stdout, "The CFO is in Herdr's cfo tab.")
		return 0
	}
	return runtime.attachHerdr()
}

// pickProject is the git checkout this terminal is in, or else one of the
// checkouts under the projects root: the only one, or the one the Overlord
// picks by number.
func pickProject(ctx context.Context, runtime commandRuntime, stdout io.Writer) (string, error) {
	if top, err := runtime.gitTop(ctx); err == nil && top != "" {
		return top, nil
	}
	var root string
	var err error
	if runtime.projectsRoot != nil {
		root, err = runtime.projectsRoot()
	}
	if err != nil || root == "" {
		return "", errors.New("this terminal is in no git checkout and no projects root is set: cd into a project, or record the folder that holds them with cfo install --projects-root <dir>")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("read the projects root %s: %w", root, err)
	}
	var checkouts []string
	for _, entry := range entries {
		// Stat rather than the entry's own type, so a junction to a checkout
		// kept on another drive counts as the folder it is.
		checkout := filepath.Join(root, entry.Name())
		if info, err := os.Stat(checkout); err != nil || !info.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(checkout, ".git")); err == nil {
			checkouts = append(checkouts, checkout)
		}
	}
	switch len(checkouts) {
	case 0:
		return "", fmt.Errorf("no git checkout under the projects root %s: cd into a project first", root)
	case 1:
		return checkouts[0], nil
	}
	fmt.Fprintln(stdout, "\nPick the project the CFO starts in:")
	for index, checkout := range checkouts {
		fmt.Fprintf(stdout, "  %d  %s\n", index+1, filepath.Base(checkout))
	}
	fmt.Fprint(stdout, "Project number: ")
	line, err := bufio.NewReader(runtime.stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("no project was picked")
	}
	number, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || number < 1 || number > len(checkouts) {
		return "", fmt.Errorf("%q is not one of the project numbers 1 to %d", strings.TrimSpace(line), len(checkouts))
	}
	return checkouts[number-1], nil
}

// gitTop is the git checkout the working directory is in.
func gitTop(ctx context.Context) (string, error) {
	result, err := execx.OSRunner{}.Run(ctx, execx.Request{Name: "git", Args: []string{"rev-parse", "--show-toplevel"}})
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", errors.New("not a git checkout")
	}
	return filepath.Clean(strings.TrimSpace(string(result.Stdout))), nil
}

// startCFOInHerdr starts the CFO in the fleet's own Herdr session.
func startCFOInHerdr(ctx context.Context, project string) error {
	return startCFOWith(ctx, &herdr.Client{Commands: execx.OSRunner{}, Session: herdrSession()}, project)
}

// startCFOWith makes sure Herdr's server runs, finds or creates the CFO's tab
// in the fleet workspace, starts Claude Code there as the CFO unless an agent
// already runs in it, and brings the tab to the front.
func startCFOWith(ctx context.Context, client *herdr.Client, project string) error {
	if err := client.EnsureServer(ctx); err != nil {
		return err
	}
	container, err := client.EnsureContainer(ctx, project)
	if err != nil {
		return err
	}
	endpoint, running, err := client.CFOTab(ctx, container, project)
	if err != nil {
		return err
	}
	if !running {
		if err := client.AgentStart(ctx, endpoint.Target, "cfo", "claude", nil); err != nil {
			return err
		}
	}
	return client.Focus(ctx, endpoint)
}

// attachHerdr hands this terminal to herdr, which attaches to the fleet's
// session, and returns its exit code when the Overlord leaves it.
func attachHerdr() int {
	command := exec.Command("herdr", "--session", herdrSession())
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "goblins: herdr could not be started: %v\n", err)
		return 1
	}
	return 0
}
