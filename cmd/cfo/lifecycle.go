package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
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
	reason := flags.String("reason", "", "pause reason: memory, allowance, overlord, dependency, question, ci or deploy")
	until := flags.String("until", "", "pause clearing condition: reset time, task:<id>, pr:<URL>, date:<RFC3339> or question id")
	revision := flags.String("revision", "", "expected queued task revision")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return 2
	}
	if err := state.ValidTaskID(args[0]); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if action == "pause" {
		if _, err := state.NewPauseCondition(*reason, *until, time.Now().UTC()); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	} else {
		if *until != "" {
			fmt.Fprintln(stderr, "--until is only valid for pause")
			return 2
		}
		if *reason == "" {
			*reason = "Requested by the operator"
		}
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
	request := lifecycle.Request{ID: args[0], Generation: *generation, Operation: *operation, Action: action, Reason: *reason, Until: *until}
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

// reachHelpers pauses or stops each live helper of meta as meta itself is
// paused or stopped, through run, the same lifecycle, so each keeps a record,
// a handoff request and a card of its own, and says what became of each. A
// helper paused with its parent keeps the parent's condition, and one its
// parent pauses to wait on stays at work. Each helper's operation is derived
// from its parent's, so a retried parent operation retries the helper's
// rather than starting another.
func reachHelpers(ctx context.Context, h home.Home, meta state.TaskMeta, record *state.Lifecycle, run func(context.Context, home.Home, lifecycle.Request, string) (state.Lifecycle, error)) ([]string, error) {
	helpers, err := state.HelpersOf(h.State, meta.ID)
	if err != nil {
		return nil, err
	}
	// A stop's record keeps the pause before it, which says nothing of the
	// stop.
	var pause *state.PauseCondition
	if record.Action == "pause" {
		pause = record.Pause
	}
	var lines []string
	var problems []error
	for _, helper := range helpers {
		if pause != nil && pause.Reason == "dependency" && strings.EqualFold(pause.Until, "task:"+helper.ID) {
			lines = append(lines, "helper "+helper.ID+" kept at work: "+meta.ID+" waits on it")
			continue
		}
		sum := sha256.Sum256([]byte(record.Operation + "\x00" + helper.ID))
		request := lifecycle.Request{ID: helper.ID, Generation: helper.SpawnGen, Operation: "helper-" + hex.EncodeToString(sum[:8]), Action: record.Action, Reason: "Stopped with its parent " + meta.ID + ": " + record.Reason}
		if pause != nil {
			request.Reason, request.Until = pause.Reason, pause.Until
		}
		result, err := run(ctx, h, request, "")
		if err != nil {
			problems = append(problems, fmt.Errorf("helper %s: %w", helper.ID, err))
			continue
		}
		lines = append(lines, "helper "+helper.ID+" "+result.Phase)
	}
	return lines, errors.Join(problems...)
}
