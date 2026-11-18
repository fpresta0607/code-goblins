package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/onboarding"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// launchSelectionArgs gives a version 6 gate run its own agents. It writes the
// task's launch selection, which names for each gate role the harness the task
// runs on with that role's model and effort, the lane its spawn chose for the
// role or else the task's own, and then the fallback the operator named when
// that one is signed in, and returns the arguments under which no-mistakes
// applies it to this run alone and proves it at the trusted commit before any
// agent starts. One shared daemon so serves tasks on different harnesses at
// once, and no gate starts a harness nobody named.
func launchSelectionArgs(ctx context.Context, commands execx.Runner, root string, meta state.TaskMeta, selection pipeline.Selection, trusted string, out io.Writer) ([]string, error) {
	own, err := gateAgent(root, meta, meta.Model, meta.Effort)
	if err != nil {
		return nil, err
	}
	roleAgent := func(model, effort string) (pipeline.Reviewer, error) {
		if model == "" {
			return own, nil
		}
		return gateAgent(root, meta, model, effort)
	}
	verify, err := roleAgent(meta.VerifyModel, meta.VerifyEffort)
	if err != nil {
		return nil, err
	}
	review, err := roleAgent(meta.ReviewModel, meta.ReviewEffort)
	if err != nil {
		return nil, err
	}
	fix, err := roleAgent(meta.FixModel, meta.FixEffort)
	if err != nil {
		return nil, err
	}
	isSignedOut := false
	roles, err := selection.Policy.GateRoleChains(verify, review, fix, func(fallback pipeline.Reviewer) bool {
		isSignedOut = !isHarnessSignedIn(ctx, commands, fallback)
		return !isSignedOut
	})
	if err != nil {
		return nil, err
	}
	data, err := pipeline.LaunchSelection(trusted, roles)
	if err != nil {
		return nil, err
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return nil, err
	}
	launch := hex.EncodeToString(random)
	// Each start writes a file of its own, which no-mistakes reads once as the
	// run starts, so no later start can replace it under an earlier one.
	path := filepath.Join(meta.TaskTmp, "launch-selection-"+launch+".json")
	if err := fsx.AtomicWriteFile(path, data); err != nil {
		return nil, err
	}
	fallback := selection.Policy.Fallback
	said := fmt.Sprintf("pipeline gate agent: %s %s %s, the task's own harness", verify.Harness, verify.Model, verify.Effort)
	if verify != own {
		said += ", on its verify lane " + meta.VerifyLane
	}
	switch {
	case len(roles.Primary) > 1:
		said += fmt.Sprintf(", then the named fallback %s %s %s", fallback.Harness, fallback.Model, fallback.Effort)
	case isSignedOut:
		said += fmt.Sprintf(". The named fallback %s is not signed in, so it is left out", fallback.Harness)
	case fallback != (pipeline.Reviewer{}):
		said += ", with no fallback named on another harness"
	default:
		said += ", with no fallback named"
	}
	fmt.Fprintln(out, said)
	// A review role apart from the primary is pinned to its own chain.
	describe := func(agent pipeline.Reviewer, lane string) string {
		if agent == own {
			return fmt.Sprintf("%s %s %s, the task's own model", agent.Harness, agent.Model, agent.Effort)
		}
		return fmt.Sprintf("%s %s %s, its lane %s", agent.Harness, agent.Model, agent.Effort, lane)
	}
	if !slices.Equal(roles.Reviewer, roles.Primary) {
		fmt.Fprintln(out, "pipeline gate reviewer: "+describe(review, meta.ReviewLane))
	}
	if !slices.Equal(roles.Fixer, roles.Primary) {
		line := "pipeline gate fixer: " + describe(fix, meta.FixLane)
		if len(roles.Primary) > 1 {
			line += ", alone, since no-mistakes gives a pinned fixer no fallback"
		}
		fmt.Fprintln(out, line)
	}
	// The nonce binds this launch to its own run, and the generation to the
	// policy the task is frozen at.
	return []string{"--launch-nonce", "ceo-" + launch, "--validation-generation", selection.Hash, "--launch-assertion", path}, nil
}

// gateAgent is the harness a task runs on with a model and effort its gate
// runs it at: the task's own or a role lane's, and where that names none, the
// machine config's agent_config for that harness, which is the operator's
// default.
func gateAgent(root string, meta state.TaskMeta, model, effort string) (pipeline.Reviewer, error) {
	own := pipeline.Reviewer{Harness: meta.Harness, Model: model, Effort: effort}
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
		return own, fmt.Errorf("pipeline: task %s names no model or no effort for its gate on %s, and a gate proves both. Name them with goblins switch %s --model <model> --effort <effort>, or as the machine's default under agent_config.%s in the no-mistakes config", meta.ID, own.Harness, meta.ID, own.Harness)
	}
	return own, nil
}

// isHarnessSignedIn asks agent's harness's own status command whether somebody
// is signed in, read as the quick start reads it. pi is asked about the
// provider agent's model names, since pi signs in per provider. A harness
// that is missing or does not answer plainly is not signed in.
func isHarnessSignedIn(ctx context.Context, commands execx.Runner, agent pipeline.Reviewer) bool {
	detector := onboarding.Detector{
		// The status command finds the program itself. This name only tells
		// the detector that a native terminal could start it.
		LookPath: func(program string) (string, error) { return program + ".exe", nil },
		Probe: func(ctx context.Context, program string, args ...string) (execx.Result, error) {
			return commands.Run(ctx, execx.Request{Name: program, Args: args})
		},
	}
	if agent.Harness == "pi" {
		detector.PiProvider, _, _ = strings.Cut(agent.Model, "/")
	}
	return detector.Detect(ctx, agent.Harness).State == onboarding.Ready
}

// isRoleChainRefused reports a no-mistakes that applies a launch selection
// but runs every gate role on one chain, so it refuses a reviewer or fixer
// apart from the primary: a build before no-mistakes pull request 310.
func isRoleChainRefused(result execx.Result) bool {
	said := append(append([]byte(nil), result.Stdout...), result.Stderr...)
	return bytes.Contains(said, []byte("launch selection names one agent chain for the primary, reviewer and fixer roles"))
}

// isLaunchSelectionUnknown reports a no-mistakes that takes no launch
// selection: a command with no strict launch or no launch assertion, a command
// that proves an assertion but cannot apply one, or a daemon older than its
// command. A selection the daemon read and refused is none of these.
func isLaunchSelectionUnknown(result execx.Result) bool {
	said := append(append([]byte(nil), result.Stdout...), result.Stderr...)
	for _, refusal := range []string{"unknown flag: --launch-", "unknown flag: --validation-generation", "unreadable launch assertion", "cannot honor --launch-assertion"} {
		if bytes.Contains(said, []byte(refusal)) {
			return true
		}
	}
	return false
}
