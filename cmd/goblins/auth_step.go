package main

import (
	"context"
	"fmt"
	"io"

	"github.com/fpresta0607/code-goblins/internal/home"
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

// waitForCard waits until card closes and prints what came of it: each
// plain value he entered as NAME=value, each secret by name only, and his
// Done, his Dismiss or why it closed otherwise. A card whose step he takes on
// the board's screen of the goblin's own browser closes once he hands the
// browser back, and his words come with its Done. It exits 0 once the step is
// done, cardDismissed when he dismissed it, and 1 otherwise.
func waitForCard(ctx context.Context, h home.Home, card string, stdout, stderr io.Writer) int {
	fmt.Fprintf(stderr, "waiting on card %s in the Command Center\n", card)
	request, err := supervisor.WaitCredential(ctx, h.State, card, nil)
	if err != nil {
		fmt.Fprintf(stderr, "goblins auth request: %v\n", err)
		return 1
	}
	switch request.State {
	case "granted":
		if request.Reason != "" {
			fmt.Fprintf(stdout, "card %s granted %s to %s\n%s\n", card, request.GrantService, request.Task, request.Reason)
		} else {
			fmt.Fprintf(stdout, "card %s granted %s to %s; re-source your auth.ps1\n", card, request.GrantService, request.Task)
		}
	case "saved":
		fmt.Fprintf(stdout, "card %s is done: every value arrived\n", card)
	case "done":
		fmt.Fprintf(stdout, "card %s is done: the step is taken\n", card)
		if request.Reason != "" {
			fmt.Fprintln(stdout, request.Reason)
		}
	case "dismissed":
		fmt.Fprintf(stdout, "he skipped card %s: go on without it\n", card)
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
