package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

func runSend(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "cfo send: target is required")
		return 2
	}
	target := args[0]
	if strings.HasPrefix(target, "-") {
		fmt.Fprintf(stderr, "cfo send: unknown flag %q\n", target)
		return 2
	}
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(stderr)
	key := fs.String("key", "", "named terminal key")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	text := strings.Join(fs.Args(), " ")
	if *key != "" && text != "" {
		fmt.Fprintln(stderr, "cfo send: --key cannot be combined with text")
		return 2
	}
	if *key == "" && text == "" {
		fmt.Fprintln(stderr, "cfo send: text or --key is required")
		return 2
	}
	if runtime.resolveHome == nil {
		fmt.Fprintln(stderr, "cfo send: command runtime is incomplete")
		return 1
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *key != "" {
		if runtime.sendKey == nil {
			fmt.Fprintln(stderr, "cfo send: command runtime is incomplete")
			return 1
		}
		if err := runtime.sendKey(context.Background(), h, target, *key); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "sent key %s to %s\n", *key, target)
		return 0
	}
	if runtime.sendText == nil {
		fmt.Fprintln(stderr, "cfo send: command runtime is incomplete")
		return 1
	}
	if err := runtime.sendText(context.Background(), h, target, text); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "sent %s\n", target)
	return 0
}

// nativePromptSince proves a native goblin took a prompt by its own hooks'
// report, spooled or in the supervisor's store of home h.
func nativePromptSince(h home.Home) func(taskID, generation string, since time.Time) (bool, error) {
	return func(taskID, generation string, since time.Time) (bool, error) {
		return supervisor.NativePromptSince(h.State, taskID, generation, since)
	}
}
