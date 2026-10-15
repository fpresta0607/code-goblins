package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// cardDismissed is the exit code of a wait on a card he closed: the asker
// goes on without it. cardAnswered is that of a card he answered in words
// instead of taking its step.
const (
	cardDismissed = 3
	cardAnswered  = 4
)

// sendCardStep sends step to the supervisor.
func (r commandRuntime) sendCardStep(h home.Home, step supervisor.CredentialStep) error {
	if r.stepCard != nil {
		return r.stepCard(h, step)
	}
	return supervisor.StepCredential(h, step)
}

// showCardBrowser restarts this terminal's browser session on profile.
func (r commandRuntime) showCardBrowser(ctx context.Context, profile, page string, isHeaded bool) error {
	if r.showBrowser != nil {
		return r.showBrowser(ctx, profile, page, isHeaded)
	}
	return showBrowserSession(execx.OSRunner{})(ctx, profile, page, isHeaded)
}

// waitForCard waits until card closes and prints what came of it: each
// plain value he entered as NAME=value, each secret by name only, and his
// Done, his Dismiss or why it closed otherwise. A card of task's that brings
// its browser session to him shows the session's window the moment he asks
// for it, and runs it headless again on the same profile once the card
// closes, so the session keeps what he did there. It exits 0 once the step is
// done, cardDismissed when he dismissed it, and 1 otherwise.
func waitForCard(ctx context.Context, h home.Home, card, task string, runtime commandRuntime, stdout, stderr io.Writer) int {
	fmt.Fprintf(stderr, "waiting on card %s in the Command Center\n", card)
	profile := ""
	isShown := false
	request, err := supervisor.WaitCredential(ctx, h.State, card, func(open supervisor.CredentialRequest) {
		if !open.Window || open.WindowState != "requested" || isShown {
			return
		}
		meta, err := state.ReadTaskMeta(h.State, task)
		if err == nil && meta.TaskTmp == "" {
			err = fmt.Errorf("task %s has no scratch folder for the window's profile", task)
		}
		if err == nil {
			profile = filepath.Join(meta.TaskTmp, "browser-profile")
			err = runtime.showCardBrowser(ctx, profile, open.Link, true)
		}
		step := supervisor.CredentialStep{ID: card, Task: task, Action: "window-open"}
		if err != nil {
			step.Action, step.Reason = "window-failed", "The window could not open: "+oneLine(err)
			fmt.Fprintf(stderr, "he asked for the window, and it could not open: %v\n", err)
		} else {
			isShown = true
			fmt.Fprintf(stderr, "he asked for the window: your browser session shows %s on his screen\n", open.Link)
		}
		if err := runtime.sendCardStep(h, step); err != nil {
			fmt.Fprintf(stderr, "the board was not told: %v\n", err)
		}
	})
	if isShown {
		hide, cancel := context.WithTimeout(context.Background(), browserHandTimeout)
		if err := runtime.showCardBrowser(hide, profile, "", false); err != nil {
			fmt.Fprintf(stderr, "your browser session could not run headless again: %v\n", err)
		} else {
			fmt.Fprintln(stdout, "your browser session runs headless again on the profile he used, so it keeps what he did there")
		}
		cancel()
	}
	if err != nil {
		fmt.Fprintf(stderr, "cfo auth request: %v\n", err)
		return 1
	}
	switch request.State {
	case "saved":
		fmt.Fprintf(stdout, "card %s is done: every value arrived\n", card)
	case "done":
		fmt.Fprintf(stdout, "card %s is done: the step is taken\n", card)
	case "dismissed":
		fmt.Fprintf(stdout, "he closed card %s: go on without it\n", card)
		return cardDismissed
	case "answered":
		fmt.Fprintf(stdout, "he answered instead: %s\n", request.Reason)
		return cardAnswered
	default:
		fmt.Fprintf(stdout, "card %s was %s: %s\n", card, request.State, request.Reason)
		return 1
	}
	for _, name := range request.Plain {
		if value, isReceived := request.Values[name]; isReceived {
			fmt.Fprintf(stdout, "%s=%s\n", name, value)
		}
	}
	for _, name := range request.Names {
		fmt.Fprintf(stdout, "%s is in the %s vault, never shown here; re-source your auth.ps1 to read it\n", name, request.Project)
	}
	return 0
}

// showBrowserSession restarts this terminal's chrome-devtools-axi session on
// profile: with a window at page, or headless. Each later command of the
// session reuses the browser it starts.
func showBrowserSession(runner execx.Runner) func(context.Context, string, string, bool) error {
	return func(ctx context.Context, profile, page string, isHeaded bool) error {
		if err := runBrowserTool(ctx, runner, nil, "stop"); err != nil {
			return err
		}
		env := append(os.Environ(), "CHROME_DEVTOOLS_AXI_USER_DATA_DIR="+profile)
		if isHeaded {
			env = append(env, "CHROME_DEVTOOLS_AXI_HEADED=1")
		}
		if err := runBrowserTool(ctx, runner, env, "start"); err != nil {
			return err
		}
		if page == "" {
			return nil
		}
		result, err := runner.Run(ctx, execx.Request{Name: "chrome-devtools-axi", Args: []string{"open", page}, Env: env, KillTree: true})
		if err != nil {
			return fmt.Errorf("chrome-devtools-axi open: %w", err)
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("chrome-devtools-axi open exited %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
		}
		return nil
	}
}
