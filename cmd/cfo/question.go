package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

func runQuestion(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	f := flag.NewFlagSet("question", flag.ContinueOnError)
	f.SetOutput(stderr)
	id := f.String("id", "", "stable ID for this CFO question")
	text := f.String("text", "", "the question deliberately escalated to the user")
	recommended := f.String("recommend", "", "exact supplied choice to recommend; omit when there is no recommendation")
	var options []string
	f.Func("option", "one real choice; repeat for each choice; omit for written answers", func(value string) error { options = append(options, value); return nil })
	if err := f.Parse(args); err != nil || f.NArg() != 0 {
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	c := supervisor.CFOConnection{State: h.State, Herdr: &herdr.Client{Commands: execx.OSRunner{}}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.PublishQuestion(ctx, *id, *text, options, *recommended); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "User question published:", *id)
	return 0
}
