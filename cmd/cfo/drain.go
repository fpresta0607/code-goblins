package main

import (
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// runDrain prints every unacknowledged wake record and any pending recovery
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
	ackBlocking := fs.Bool("ack-blocking", false, "also retire EVERY blocked/failed notify at or below --ack-through, which it refuses on its own")
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

	// One acknowledgement is one hold of the wake lock (wake.Acknowledge): the
	// records first, then the episode only when the queue it leaves is empty,
	// so a partial ack can never retire an episode whose records are still
	// queued. What it left is what is listed, with no second read.
	//
	// A blocked/failed notify is a goblin waiting on a CFO decision. Acking it
	// retires the only durable record of that question, so a drain whose output
	// was truncated - piped through tail, say - can bury it and leave the goblin
	// parked forever. Those records are therefore refused unless --ack-blocking
	// says the operator has actually read them. Only --ack-blocking retires
	// them, and it is range-scoped: it retires every one at or below the
	// sequence, so the operator retires them deliberately after seeing the
	// full listing. A notify the Overlord already answered on the board is
	// not refused: the goblin has its answer.
	if ackSet || genSet {
		ack := wake.Ack{ShouldRetireQuestions: *ackBlocking}
		if ackSet {
			ack.Through = ackThrough
		}
		if genSet {
			ack.Generation = recoveryGen
		}
		acknowledged, err := wake.Acknowledge(h.State, ack)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if len(acknowledged.Refused) > 0 {
			fmt.Fprintln(stderr, "cfo drain: refusing to ack goblins that are waiting on you:")
			for _, rec := range acknowledged.Refused {
				fmt.Fprintf(stderr, "  %d  %s  %s\n", rec.Seq, rec.Key, rec.Detail)
			}
			fmt.Fprintln(stderr, "answer each with `cfo send <id> \"...\"`, then re-run with --ack-blocking to retire EVERY question listed above.")
			fmt.Fprintln(stderr, "--ack-blocking is range-scoped, not per-record: it retires all of them, and the list above is the whole set. The ack floor only moves forward, so there is no way to retire a later question while keeping an earlier one - to hold one open, handle it first or re-run --ack-through below its sequence.")
			return 1
		}
		if acknowledged.HasGenerationMoved {
			// The sequence ack is kept (idempotent and forward-only); only the
			// episode ack is skipped. Exit 0 either way.
			fmt.Fprintln(stdout, "recovery generation moved, re-run: cfo drain")
		}
		if err := renderDrain(h.State, acknowledged.Pending, acknowledged.Episode, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}

	records, err := wake.Pending(h.State)
	if err == nil {
		var episode wake.Episode
		if episode, err = wake.ReadEpisode(h.State); err == nil {
			err = renderDrain(h.State, records, episode, stdout)
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// renderDrain hands the wake queue's raw pending records and current episode
// to wake.Render, the shared renderer behind both this command and the
// session-start digest's WAKE QUEUE section. See wake.Render's doc comment
// for the four output shapes it prints. While AFK mode is on its notice
// comes first, so every wake tells the CFO it is on and what its authority
// covers, and the drain still ends with its ack line.
func renderDrain(stateDir string, records []wake.Record, episode wake.Episode, stdout io.Writer) error {
	for _, line := range afk.NoticeFor(stateDir) {
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return err
		}
	}
	return wake.Render(stdout, records, episode, time.Now().UTC())
}
