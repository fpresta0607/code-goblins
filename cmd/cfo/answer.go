package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

func runAnswer(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(stderr, "cfo answer: a goblin question ID or its notify's wake sequence is required")
		return 2
	}
	f := flag.NewFlagSet("answer", flag.ContinueOnError)
	f.SetOutput(stderr)
	option := f.String("option", "", "the choice that answers the question, in full or by its first word such as a")
	note := f.String("note", "", "text the goblin receives after the choice")
	if err := f.Parse(args[1:]); err != nil || f.NArg() != 0 {
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
	c := supervisor.CFOConnection{State: h.State, Herdr: &herdr.Client{Commands: execx.OSRunner{}}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	chosen, err := c.AnswerGoblin(ctx, args[0], *option, *note)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "answered %s: %s\n", args[0], chosen)
	return 0
}
