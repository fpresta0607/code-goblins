package main

import (
	"errors"
	"flag"
	"fmt"
	"io"

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

// renderDrain prints the wake queue's three output shapes: an empty queue
// with no pending episode, an empty queue with a pending episode, or the
// full deduped listing. The header count and rendered rows are the deduped
// presentation; the ack-through sequence a WAKE_ACK_REQUIRED line carries is
// always the highest raw pending sequence, since records the fold collapsed
// still need to be retired.
func renderDrain(stateDir string, stdout io.Writer) error {
	records, err := wake.Pending(stateDir)
	if err != nil {
		return err
	}
	episode, err := wake.ReadEpisode(stateDir)
	if err != nil {
		return err
	}

	if len(records) == 0 && !episode.Pending {
		_, err := fmt.Fprintln(stdout, "WAKE QUEUE: empty")
		return err
	}

	deduped := wake.Deduped(records)
	if _, err := fmt.Fprintf(stdout, "WAKE QUEUE: %d pending\n", len(deduped)); err != nil {
		return err
	}
	for _, rec := range deduped {
		line := fmt.Sprintf("  %d  %-6s  ", rec.Seq, rec.Kind)
		if rec.Key != rec.Kind {
			line += rec.Key + ": "
		}
		line += rec.Detail
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return err
		}
	}

	if !episode.Pending {
		return nil
	}

	maxSeq := 0
	for _, rec := range records {
		if rec.Seq > maxSeq {
			maxSeq = rec.Seq
		}
	}
	if _, err := fmt.Fprintf(stdout, "RECOVERY EPISODE: pending, generation %d\n", episode.Gen); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "WAKE_ACK_REQUIRED: cfo drain --ack-through %d --recovery-generation %d\n", maxSeq, episode.Gen)
	return err
}
