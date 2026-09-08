package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

func runPipeline(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "cfo pipeline: config-drift, config-apply, run or respond required")
		return 2
	}
	if args[0] != "config-drift" && args[0] != "config-apply" && args[0] != "run" && args[0] != "respond" {
		fmt.Fprintln(stderr, "cfo pipeline: unknown command")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	root, err := pipeline.DefaultRoot()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	commands := execx.OSRunner{}
	if err := pipelineCommand(context.Background(), h, root, commands, args, stdout); err != nil {
		fmt.Fprintln(stderr, err)
		if errors.Is(err, pipeline.ErrUnresolved) {
			return 3
		}
		return 1
	}
	return 0
}

func pipelineCommand(ctx context.Context, h home.Home, root string, commands execx.Runner, args []string, out io.Writer) (err error) {
	reader := pipeline.Reader{Root: root, Commands: commands}
	if args[0] == "config-drift" || args[0] == "config-apply" {
		if len(args) != 1 {
			return errors.New("pipeline: config commands take no arguments")
		}
		policy, err := pipeline.Load(filepath.Join(h.Root, "config", "pipeline.json"))
		if err != nil {
			return err
		}
		config := pipeline.Config{Path: filepath.Join(root, "config.yaml"), Policy: policy, Idle: reader.Idle}
		drift, err := config.Drift()
		if err != nil {
			return err
		}
		if len(drift) == 0 {
			fmt.Fprintln(out, "pipeline config: no drift")
		} else {
			fmt.Fprintln(out, "pipeline config drift:", strings.Join(drift, ", "))
		}
		if args[0] == "config-drift" {
			return nil
		}
		result, err := config.Apply(ctx)
		if result.Backup != "" {
			fmt.Fprintln(out, "pipeline config backup:", result.Backup)
		}
		if err != nil {
			return err
		}
		fmt.Fprintln(out, "pipeline config: applied in idle window; daemon remains stopped")
		return nil
	}
	if len(args) < 2 {
		return errors.New("pipeline: task ID required")
	}
	id := args[1]
	if err := state.ValidTaskID(id); err != nil {
		return err
	}
	flags := flag.NewFlagSet("pipeline", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var intent string
	var response pipeline.Response
	if args[0] == "run" {
		flags.StringVar(&intent, "intent", "", "task intent")
	} else {
		flags.StringVar(&response.Action, "action", "", "fix or approve")
		flags.StringVar(&response.Findings, "findings", "", "comma-separated finding IDs")
		flags.StringVar(&response.Instructions, "instructions", "", "guidance for selected findings")
	}
	if err := flags.Parse(args[2:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("pipeline: unexpected arguments")
	}
	if args[0] == "run" && strings.TrimSpace(intent) == "" {
		return errors.New("pipeline: --intent is required")
	}
	// This is a short-lived operation lock, not the event/decision ownership
	// ledger. It prevents two CFO commands accepting the same round concurrently.
	lockName := state.CleanupLockName(id)
	if _, err := lock.AcquireExclusiveNamed(h.State, lockName); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(h.State, lockName)) }()
	meta, err := state.ReadTaskMeta(h.State, id)
	if err != nil {
		return err
	}
	if meta.Mode != "no-mistakes" || meta.PipelineHash == "" {
		return errors.New("pipeline: task lacks a spawn-time policy; existing tasks require an explicit migration decision")
	}
	expectedTmp := filepath.Join(h.State, "tasktmp", id)
	if !fsx.SamePath(meta.TaskTmp, expectedTmp) {
		return errors.New("pipeline: task temporary path does not match metadata identity")
	}
	selection, err := pipeline.LoadSelection(filepath.Join(expectedTmp, "pipeline.json"))
	if err != nil {
		return err
	}
	if selection.Hash != meta.PipelineHash || selection.Class != meta.PipelineClass {
		return errors.New("pipeline: task policy differs from its spawn metadata")
	}
	if err := worktree.Validate(ctx, worktree.RunnerGit{Commands: commands}, meta.Project, meta.Worktree); err != nil {
		return err
	}
	config := pipeline.Config{Path: filepath.Join(root, "config.yaml"), Policy: selection.Policy}
	drift, err := config.Drift()
	if err != nil {
		return err
	}
	if len(drift) != 0 {
		return fmt.Errorf("pipeline: shared config drift (%s); request idle config-apply, never change a running daemon", strings.Join(drift, ", "))
	}
	branchResult, err := commands.Run(ctx, execx.Request{Dir: meta.Worktree, Name: "git", Args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}})
	if err != nil || branchResult.ExitCode != 0 {
		return errors.New("pipeline: named task branch required")
	}
	branch := strings.TrimSpace(string(branchResult.Stdout))
	if branch == "" || branch == "main" || branch == "master" {
		return errors.New("pipeline: isolated feature branch required")
	}
	nativeArgs := []string{"axi", "run", "--intent", intent}
	if args[0] == "respond" {
		gate, err := reader.Gate(ctx, meta.Project, branch)
		if err != nil {
			return err
		}
		nativeArgs, err = pipeline.ResponseArgs(selection, gate, response)
		if err != nil {
			return err
		}
	} else {
		if err := reader.CheckStart(ctx, meta.Project, meta.Worktree, branch, selection.Policy); err != nil {
			return err
		}
	}
	result, err := commands.Run(ctx, execx.Request{Dir: meta.Worktree, Env: []string{"NM_HOME=" + root}, Name: "no-mistakes", Args: nativeArgs})
	if len(result.Stdout) > 0 {
		fmt.Fprint(out, string(result.Stdout))
	}
	if len(result.Stderr) > 0 {
		fmt.Fprint(out, string(result.Stderr))
	}
	if err != nil {
		return fmt.Errorf("pipeline: native command failed: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("pipeline: native command exited %d", result.ExitCode)
	}
	return nil
}
