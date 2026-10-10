package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/onboarding"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// launchSelectionFile is where a task keeps the launch selection of the gate
// run it last started. no-mistakes reads it once, as the run starts.
const launchSelectionFile = "launch-selection.json"

// launchSelectionArgs gives a version 6 gate run its own agents. It writes the
// task's launch selection, which names the harness the task runs on with its
// model and effort for every gate role and then the fallback the operator
// named when that one is signed in, and returns the arguments under which
// no-mistakes applies it to this run alone and proves it at the trusted commit
// before any agent starts. One shared daemon so serves tasks on different
// harnesses at once, and no gate starts a harness nobody named.
func launchSelectionArgs(ctx context.Context, commands execx.Runner, root string, meta state.TaskMeta, selection pipeline.Selection, trusted string, out io.Writer) ([]string, error) {
	if err := pipeline.GateHarness(meta.Harness); err != nil {
		return nil, fmt.Errorf("%w. Task %s runs on %s, so switch it with cfo switch %s --harness <harness>", err, meta.ID, meta.Harness, meta.ID)
	}
	own, err := gateAgent(root, meta)
	if err != nil {
		return nil, err
	}
	isSignedOut := false
	chain, err := selection.Policy.GateChain(own, func(name string) bool {
		isSignedOut = !isHarnessSignedIn(ctx, commands, name)
		return !isSignedOut
	})
	if err != nil {
		return nil, err
	}
	data, err := pipeline.LaunchSelection(trusted, chain)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(meta.TaskTmp, launchSelectionFile)
	if err := fsx.AtomicWriteFile(path, data); err != nil {
		return nil, err
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	fallback := selection.Policy.Fallback
	said := fmt.Sprintf("pipeline gate agent: %s %s %s, the task's own harness", own.Harness, own.Model, own.Effort)
	switch {
	case len(chain) > 1:
		said += fmt.Sprintf(", then the named fallback %s %s %s", fallback.Harness, fallback.Model, fallback.Effort)
	case isSignedOut:
		said += fmt.Sprintf(". The named fallback %s is not signed in, so it is left out", fallback.Harness)
	case fallback != (pipeline.Reviewer{}):
		said += ", with no fallback named on another harness"
	default:
		said += ", with no fallback named"
	}
	fmt.Fprintln(out, said)
	// The nonce binds this launch to its own run, and the generation to the
	// policy the task is frozen at.
	return []string{"--launch-nonce", "cfo-" + hex.EncodeToString(nonce), "--validation-generation", selection.Hash, "--launch-assertion", path}, nil
}

// gateAgent is the harness a task runs on with the model and effort its gate
// runs it at: the task's own, and where its record names none, the machine
// config's agent_config for that harness, which is the operator's default.
func gateAgent(root string, meta state.TaskMeta) (pipeline.Reviewer, error) {
	own := pipeline.Reviewer{Harness: meta.Harness, Model: meta.Model, Effort: meta.Effort}
	// A task record says "default" for a model or effort its harness chose.
	if own.Model == "default" {
		own.Model = ""
	}
	if own.Effort == "default" {
		own.Effort = ""
	}
	if own.Model != "" && own.Effort != "" {
		return own, nil
	}
	config, err := fsx.ReadFile(filepath.Join(root, "config.yaml"))
	if err != nil {
		return own, err
	}
	machine, err := pipeline.MachineProfile(config, own.Harness)
	if err != nil {
		return own, err
	}
	if own.Model == "" {
		own.Model = machine.Model
	}
	if own.Effort == "" {
		own.Effort = machine.Effort
	}
	if own.Model == "" || own.Effort == "" {
		return own, fmt.Errorf("pipeline: task %s names no model or no effort for its gate on %s, and a gate proves both. Name them with cfo switch %s --model <model> --effort <effort>, or as the machine's default under agent_config.%s in the no-mistakes config", meta.ID, own.Harness, meta.ID, own.Harness)
	}
	return own, nil
}

// isHarnessSignedIn asks a harness's own status command whether somebody is
// signed in, read as the quick start reads it. A harness that is missing or
// does not answer plainly is not signed in.
func isHarnessSignedIn(ctx context.Context, commands execx.Runner, name string) bool {
	detector := onboarding.Detector{
		// The status command finds the program itself. This name only tells
		// the detector that a native terminal could start it.
		LookPath: func(program string) (string, error) { return program + ".exe", nil },
		Probe: func(ctx context.Context, program string, args ...string) (execx.Result, error) {
			return commands.Run(ctx, execx.Request{Name: program, Args: args})
		},
	}
	return detector.Detect(ctx, name).State == onboarding.Ready
}

// isLaunchSelectionUnknown reports a no-mistakes build that has no launch
// assertion at all, or one that cannot apply it.
func isLaunchSelectionUnknown(result execx.Result) bool {
	said := append(append([]byte(nil), result.Stdout...), result.Stderr...)
	return bytes.Contains(said, []byte("unknown flag: --launch-assertion")) || bytes.Contains(said, []byte("unreadable launch assertion"))
}

// restartLaunchSelection is the launch selection of the run Resume starts in
// place of a paused one, for a task whose policy runs its gate on its own
// harness, and nothing for any other task. That start goes past cfo pipeline
// run, so it carries the selection itself.
func restartLaunchSelection(ctx context.Context, commands execx.Runner, gate pipeline.Reader, meta state.TaskMeta) ([]string, error) {
	if meta.Mode != "no-mistakes" || meta.PipelineHash == "" {
		return nil, nil
	}
	selection, err := pipeline.LoadSelection(filepath.Join(meta.TaskTmp, "pipeline.json"))
	if err != nil {
		return nil, err
	}
	if selection.Policy.Version < 6 {
		return nil, nil
	}
	trusted, err := gate.TrustedHead(ctx, meta.Project)
	if err != nil {
		return nil, err
	}
	return launchSelectionArgs(ctx, commands, gate.Root, meta, selection, trusted, io.Discard)
}
