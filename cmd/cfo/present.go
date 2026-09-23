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

func runPresent(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	f := flag.NewFlagSet("present", flag.ContinueOnError)
	f.SetOutput(stderr)
	var a supervisor.BoardActivity
	f.StringVar(&a.ID, "id", "", "stable activity ID; reuse for updates and end")
	f.StringVar(&a.TaskID, "task", "", "reporting task ID, run from that goblin's own pane; omit only from verified primary CFO context")
	f.StringVar(&a.Generation, "generation", "", "optional: the task's spawn generation, refused when it is no longer current")
	f.StringVar(&a.Kind, "kind", "", "browser or review")
	f.StringVar(&a.URL, "url", "", "https, or plain http on this machine or the tailnet, without credentials, query or fragment")
	f.StringVar(&a.State, "state", "active", "active or ended")
	ttl := f.Duration("ttl", 5*time.Minute, "evidence expiry, at most 30m; refresh only while activity is actually live")
	if err := f.Parse(args); err != nil || f.NArg() != 0 {
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	a.At = time.Now().UTC()
	a.Until = a.At.Add(*ttl)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := supervisor.PublishPresentation(ctx, h, &herdr.Client{Commands: execx.OSRunner{}}, a); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "Presentation reported:", a.ID)
	return 0
}
