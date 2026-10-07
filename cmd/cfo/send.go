package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
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
		if err := runtime.sendKey(h, target, *key); err != nil {
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
	err = runtime.sendText(context.Background(), h, target, text)
	if errors.Is(err, fleet.ErrQueuedForToolCall) {
		fmt.Fprintf(stdout, "queued for %s: it is in a turn, so its harness hands the text to it at its next tool call, once the call running now ends, or as its turn ends if it calls no tool first. It was typed once: do not send it again.\n", target)
		return 0
	}
	if err != nil {
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

// nativeTask is the record of the task target names, by its id or as gb-<id>:
// a message or a key reaches a goblin only in its native terminal.
func nativeTask(h home.Home, target string) (state.TaskMeta, error) {
	meta, native := fleet.NativeTask(h.State, target)
	switch {
	case native:
		return meta, nil
	case meta.ID != "":
		return meta, fmt.Errorf("task %s was recorded in Herdr by an older build, and this build reaches a goblin only in a native terminal", meta.ID)
	}
	return meta, fmt.Errorf("unknown task %q; give a task id", target)
}
