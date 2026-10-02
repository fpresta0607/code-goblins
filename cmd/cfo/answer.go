package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

func runAnswer(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(stderr, "cfo answer: a question ID or a goblin notify's wake sequence is required")
		return 2
	}
	f := flag.NewFlagSet("answer", flag.ContinueOnError)
	f.SetOutput(stderr)
	option := f.String("option", "", "the choice that answers the question, in full or by its first word such as a")
	note := f.String("note", "", "text the goblin receives after the choice")
	recordOnly := f.Bool("record-only", false, "record on the board a choice already given another way, sending nothing; takes the question ID, and for a goblin's question only a notify already acknowledged or answered")
	in := f.String("in", "", "with --record-only, where the Overlord gave the answer, such as chat; the card then reads as his answer there, recorded by the CFO, and the CFO's own question needs it")
	if err := f.Parse(args[1:]); err != nil || f.NArg() != 0 {
		return 2
	}
	if *in != "" && !*recordOnly {
		fmt.Fprintln(stderr, "cfo answer: --in goes with --record-only")
		return 2
	}
	if strings.TrimSpace(*option) == "" {
		fmt.Fprintln(stderr, "cfo answer: --option is required")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	c := supervisor.CFOConnection{State: h.State}
	if *recordOnly {
		chosen, err := c.RecordAnswer(args[0], *option, *note, *in)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "recorded %s on the board: %s (nothing was sent)\n", args[0], chosen)
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	chosen, queued, err := c.AnswerGoblin(ctx, args[0], *option, *note)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if queued {
		fmt.Fprintf(stdout, "answered %s: %s (queued: the goblin was working and takes it when its current turn ends; recorded, so do not send it again)\n", args[0], chosen)
		return 0
	}
	fmt.Fprintf(stdout, "answered %s: %s\n", args[0], chosen)
	return 0
}
