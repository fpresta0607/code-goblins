package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// runDrain prints the deduped pending wake queue and any pending recovery
// episode, then the ack command line an operator runs to retire them; with
// --ack-through and/or --recovery-generation it performs those acks first.
// Drain never creates a home's state/ directory itself: with no flags given
// it only reads, and a missing state/ reads as an empty queue with no
// episode, same as a home that has never woken.
func runDrain(h home.Home, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("drain", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ackThrough := fs.Int("ack-through", 0, "acknowledge wake records through this sequence")
	recoveryGen := fs.Int("recovery-generation", 0, "acknowledge the recovery episode at this generation")
	ackBlocking := fs.Bool("ack-blocking", false, "also retire blocked/failed notifies, which --ack-through refuses on its own")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Flag presence must be detected with Visit, never by value: --ack-through 0
	// is a legitimate argument a value check cannot distinguish from an absent flag.
	var ackSet, genSet bool
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "ack-through":
			ackSet = true
		case "recovery-generation":
			genSet = true
		}
	})

	// Ack ordering is fixed: apply AckThrough first, then AckEpisode only when
	// the resulting queue is empty, so a partial ack can never retire an
	// episode whose records are still queued.

	// A blocked/failed notify is a goblin waiting on a CFO decision. Acking it
	// retires the only durable record of that question, so a drain whose output
	// was truncated — piped through tail, say — can bury it and leave the goblin
	// parked forever. Those records are therefore refused unless --ack-blocking
	// says the operator has actually read them.
	if ackSet && !*ackBlocking {
		blocking, err := blockingAtOrBelow(h.State, *ackThrough)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if len(blocking) > 0 {
			fmt.Fprintln(stderr, "cfo drain: refusing to ack a goblin that is waiting on you:")
			for _, rec := range blocking {
				fmt.Fprintf(stderr, "  %d  %s  %s\n", rec.Seq, rec.Key, rec.Detail)
			}
			fmt.Fprintln(stderr, "answer it with `cfo send <id> \"...\"`, or re-run with --ack-blocking to retire it unanswered")
			return 1
		}
	}

	if ackSet {
		if err := wake.AckThrough(h.State, *ackThrough); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}

	if genSet {
		pending, err := wake.Pending(h.State)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if len(pending) == 0 {
			if err := wake.AckEpisode(h.State, *recoveryGen); err != nil {
				if !errors.Is(err, wake.ErrGenerationMismatch) {
					fmt.Fprintln(stderr, err)
					return 1
				}
				// The sequence ack above is kept (idempotent and forward-only);
				// only the episode ack is skipped. Exit 0 either way.
				fmt.Fprintln(stdout, "recovery generation moved, re-run: cfo drain")
			}
		}
	}

	if err := renderDrain(h.State, stdout); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// blockingAtOrBelow returns the pending notifies at or below seq that report a
// goblin blocked or failed — the ones that carry a question only the CFO can
// answer. It reads the deduped queue on purpose: a later notify for the same
// goblin (a `done`, say) supersedes an earlier block, and a block that has
// already been superseded is no longer waiting on anyone.
func blockingAtOrBelow(stateDir string, seq int) ([]wake.Record, error) {
	pending, err := wake.Pending(stateDir)
	if err != nil {
		return nil, err
	}
	var blocking []wake.Record
	for _, rec := range pending {
		if rec.Seq > seq || rec.Kind != "notify" {
			continue
		}
		if strings.HasPrefix(rec.Detail, "blocked:") || strings.HasPrefix(rec.Detail, "failed:") {
			blocking = append(blocking, rec)
		}
	}
	return blocking, nil
}

// renderDrain reads the wake queue's raw pending records and current
// episode, then hands them to wake.Render, the shared renderer behind both
// this command and the session-start digest's WAKE QUEUE section. See
// wake.Render's doc comment for the four output shapes it prints.
func renderDrain(stateDir string, stdout io.Writer) error {
	records, err := wake.Pending(stateDir)
	if err != nil {
		return err
	}
	episode, err := wake.ReadEpisode(stateDir)
	if err != nil {
		return err
	}
	return wake.Render(stdout, records, episode)
}
