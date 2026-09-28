package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func runLifecycle(action string, args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "task ID is required")
		return 2
	}
	flags := flag.NewFlagSet(action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	generation := flags.String("generation", "", "expected task generation")
	operation := flags.String("operation", "", "idempotent operation identity")
	reason := flags.String("reason", "Requested by the operator", "why the task is changing")
	revision := flags.String("revision", "", "expected queued task revision")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return 2
	}
	if err := state.ValidTaskID(args[0]); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !home.IsPrimary(h) {
		fmt.Fprintln(stderr, "task lifecycle requires a primary home")
		return 1
	}
	if *operation == "" {
		*operation = fmt.Sprintf("op-%d", time.Now().UnixNano())
	}
	request := lifecycle.Request{ID: args[0], Generation: *generation, Operation: *operation, Action: action, Reason: *reason}
	meta, err := state.ReadTaskMeta(h.State, request.ID)
	if err != nil && !(errors.Is(err, os.ErrNotExist) && action == "stop") {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if request.Generation == "" {
		request.Generation = meta.SpawnGen
	}
	if runtime.taskLifecycle == nil {
		fmt.Fprintln(stderr, "task lifecycle is unavailable")
		return 1
	}
	result, err := runtime.taskLifecycle(context.Background(), h, request, *revision)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
