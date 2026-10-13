package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// browserHandTimeout bounds handing the session over: a bridge's first
// start fetches chrome-devtools-mcp, which takes about 30 seconds cold.
const browserHandTimeout = 2 * time.Minute

// runAuthBrowser lends a goblin's browser session its project's login of a
// provider, or gives it back:
//
//	cfo auth browser <provider> --task <id> [--release]
//
// The supervisor starts the login's Chrome on its own profile, headless and
// reaching only the provider's hosts, and this command points the task's
// chrome-devtools-axi session at it, so the goblin drives that browser as it
// drives its own. The login is lent to one task at a time.
func runAuthBrowser(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(stderr, "usage: cfo auth browser <provider> --task <id> [--release]")
		return 2
	}
	provider := args[0]
	flags := flag.NewFlagSet("auth browser", flag.ContinueOnError)
	flags.SetOutput(stderr)
	task := flags.String("task", "", "your task's ID")
	release := flags.Bool("release", false, "give the login's browser back, which closes it")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return 2
	}
	if state.ValidTaskID(*task) != nil {
		fmt.Fprintln(stderr, "cfo auth browser: --task names your own task's ID")
		return 2
	}
	// The session the command points is the terminal's own, which spawn
	// names after its task: any other would hand the login to a browser
	// session someone else uses, or to the one the tool shares with every
	// program of the user.
	if session := os.Getenv(spawn.BrowserSessionVariable); session != *task {
		fmt.Fprintf(stderr, "cfo auth browser: this terminal's browser session is %q, not task %s's own, so nothing was lent\n", session, *task)
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), browserHandTimeout)
	defer cancel()
	if *release {
		// The session lets go of the browser before it closes, so its next
		// command starts a browser of its own.
		if err := runtime.handBrowser(ctx, ""); err != nil {
			fmt.Fprintf(stderr, "cfo auth browser: %v\n", err)
			return 1
		}
		if _, err := runtime.requestBrowser(h, supervisor.BrowserRequest{Action: "release", Task: *task, Provider: provider}); err != nil {
			fmt.Fprintf(stderr, "cfo auth browser: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "gave the %s login's browser back, and it is closing. Your browser session starts a browser of its own on its next command\n", provider)
		return 0
	}
	lease, err := runtime.requestBrowser(h, supervisor.BrowserRequest{Action: "attach", Task: *task, Provider: provider})
	if err != nil {
		fmt.Fprintf(stderr, "cfo auth browser: %v\n", err)
		return 1
	}
	if err := runtime.handBrowser(ctx, lease.URL); err != nil {
		_, releaseErr := runtime.requestBrowser(h, supervisor.BrowserRequest{Action: "release", Task: *task, Provider: provider})
		fmt.Fprintf(stderr, "cfo auth browser: your browser session could not take the login's browser, which was asked to close: %v\n", err)
		if releaseErr != nil {
			fmt.Fprintf(stderr, "cfo auth browser: closing it failed too: %v\n", releaseErr)
		}
		return 1
	}
	fmt.Fprintf(stdout, "your browser session %s now drives the %s login of %s (%s): a Chrome with no window that reaches only %s's hosts\n", *task, lease.Provider, lease.Account, lease.Login, lease.Provider)
	fmt.Fprintln(stdout, "drive it with chrome-devtools-axi as before, and only while the keys on its pages are masked: never reveal or read a key in it")
	fmt.Fprintf(stdout, "give it back with: cfo auth browser %s --task %s --release (the supervisor closes it after 30 minutes either way)\n", provider, *task)
	return 0
}

// handBrowserSession points this terminal's chrome-devtools-axi session at
// the browser at url, or with no url lets go of it: it stops the session's
// bridge, and starts a new one on url, which every later command of the
// session reuses while that browser answers.
func handBrowserSession(runner execx.Runner) func(context.Context, string) error {
	return func(ctx context.Context, url string) error {
		if err := runBrowserTool(ctx, runner, nil, "stop"); err != nil {
			return err
		}
		if url == "" {
			return nil
		}
		return runBrowserTool(ctx, runner, append(os.Environ(), "CHROME_DEVTOOLS_AXI_BROWSER_URL="+url), "start")
	}
}

func runBrowserTool(ctx context.Context, runner execx.Runner, env []string, command string) error {
	result, err := runner.Run(ctx, execx.Request{Name: "chrome-devtools-axi", Args: []string{command}, Env: env, KillTree: true})
	if err != nil {
		return fmt.Errorf("chrome-devtools-axi %s: %w", command, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("chrome-devtools-axi %s exited %d: %s", command, result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return nil
}
