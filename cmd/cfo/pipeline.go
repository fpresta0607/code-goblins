package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/gatedb"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/wake"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

func runPipeline(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "cfo pipeline: config-drift, config-apply, migrate, run, respond, recover, handback, terminal-preflight, validate-authored or validate-terminal required")
		return 2
	}
	if args[0] != "config-drift" && args[0] != "config-apply" && args[0] != "migrate" && args[0] != "run" && args[0] != "respond" && args[0] != "recover" && args[0] != "handback" && args[0] != "terminal-preflight" && args[0] != "validate-authored" && args[0] != "validate-terminal" {
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
	commands := gatedb.Runner{Next: execx.OSRunner{}}
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
		if err != nil || len(result.Drift) == 0 {
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
	var intent, intentFile, wantBranch, wantRun, admissionFile, scopeDecision, correctedFindings string
	isAdmission := args[0] == "validate-authored" || args[0] == "validate-terminal"
	isPreflight := args[0] == "terminal-preflight"
	var shouldResume, shouldRevalidatePublication bool
	var receipt string
	var response pipeline.Response
	if args[0] == "run" {
		flags.StringVar(&intent, "intent", "", "task intent")
		flags.StringVar(&intentFile, "intent-file", "", "the file that holds the task intent")
	} else if args[0] == "respond" {
		flags.StringVar(&response.Action, "action", "", "fix or approve")
		flags.StringVar(&response.Findings, "findings", "", "comma-separated finding IDs")
		flags.StringVar(&response.Instructions, "instructions", "", "guidance for selected findings")
		flags.StringVar(&response.Accept, "accept", "", "with approve: every open ask-user and auto-fix finding the CEO, or the task's own goblin on its run's spent review, accepts as it stands")
		flags.StringVar(&response.Reason, "reason", "", "with approve of the test step: the operator's reason the native exception record keeps")
		flags.StringVar(&scopeDecision, "scope-decision", "", "with a Review fix: an answered task decision sequence whose exact accepted scope reaches native; a blank one is refused")
	} else if isAdmission {
		flags.StringVar(&admissionFile, "admission-file", "", "controller-owned native validation admission JSON")
		if args[0] == "validate-authored" {
			flags.StringVar(&correctedFindings, "findings", "", "with --run, every finding of the run's handed-back Test or Lint step that the task's own goblin's one commit corrects")
		}
	} else if args[0] == "handback" {
		flags.BoolVar(&shouldResume, "resume", false, "submit the authored correction to the same native run")
		flags.StringVar(&receipt, "receipt", "", "the native release receipt required with --resume")
		flags.BoolVar(&shouldRevalidatePublication, "revalidate-publication", false, "release a post-validation publication with historical evidence pinned; primary CFO only")
	}
	if args[0] == "run" || args[0] == "respond" || args[0] == "recover" || args[0] == "handback" || isAdmission {
		flags.StringVar(&wantBranch, "branch", "", "the branch, checked out in one of the task's worktrees, to act on")
	}
	if args[0] == "respond" || args[0] == "recover" || args[0] == "handback" || isPreflight || args[0] == "validate-authored" {
		flags.StringVar(&wantRun, "run", "", "the run, the latest of a branch checked out in one of the task's worktrees, to act on")
	}
	if err := flags.Parse(args[2:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("pipeline: unexpected arguments")
	}
	isOwnerCorrection := correctedFindings != ""
	if isAdmission && (isOwnerCorrection != (wantRun != "") || isOwnerCorrection && admissionFile != "") {
		return errors.New("pipeline: validate-authored takes --admission-file, or --run <run> with --findings <ids> for its own goblin's correction of a handed-back step")
	}
	if isAdmission && !isOwnerCorrection && strings.TrimSpace(admissionFile) == "" {
		return errors.New("pipeline: --admission-file is required")
	}
	if isPreflight && wantRun == "" {
		return errors.New("pipeline: terminal-preflight requires --run <predecessor>")
	}
	if isOwnerCorrection {
		if err := ownGoblin(h.State, id); err != nil {
			return fmt.Errorf("pipeline: validate-authored --findings is task %s's own goblin's submission of its correction (%v); the CFO admits any other correction with cfo pipeline validate-authored %s --admission-file <file>", id, err, id)
		}
	} else if isAdmission || isPreflight || shouldRevalidatePublication {
		cfo := supervisor.CFOConnection{State: h.State}
		var release func()
		err := errors.New("gate agents and goblins cannot act as the registered primary CFO")
		if os.Getenv(gateAgentVariable) == "" && os.Getenv(harness.RoleVariable) != harness.RoleGoblin {
			_, release, err = cfo.CallerIdentity()
		}
		if err != nil {
			if shouldRevalidatePublication {
				return fmt.Errorf("pipeline: --revalidate-publication requires the registered primary CFO: %w", err)
			}
			if isAdmission {
				return fmt.Errorf("pipeline: validation admission and successor selection require the registered primary CFO: %w", err)
			}
			if goblinErr := ownGoblin(h.State, id); goblinErr != nil {
				return fmt.Errorf("pipeline: terminal preflight requires the registered primary CFO (%v) or the task's own goblin (%v)", err, goblinErr)
			}
		} else {
			defer release()
		}
	}
	if shouldResume != (receipt != "") {
		return errors.New("pipeline: handback --resume requires --receipt, and --receipt requires --resume")
	}
	if intent != "" && intentFile != "" {
		return errors.New("pipeline: give the intent with --intent-file or with --intent, not both")
	}
	if intentFile != "" {
		text, err := fsx.ReadFile(intentFile)
		if err != nil {
			return fmt.Errorf("pipeline: read --intent-file: %w", err)
		}
		intent = string(text)
	}
	if args[0] == "run" && strings.TrimSpace(intent) == "" {
		return errors.New("pipeline: --intent-file or --intent is required, and the intent must not be empty")
	}
	if wantBranch != "" && wantRun != "" {
		return errors.New("pipeline: name a branch or a run, not both")
	}
	isReasonGiven := false
	flags.Visit(func(given *flag.Flag) { isReasonGiven = isReasonGiven || given.Name == "reason" })
	if isReasonGiven && response.Reason == "" {
		// An empty --reason, as an unset variable gives, read as no reason would
		// approve a Test exception with none and no audit of it (issue 1585).
		return errors.New("pipeline: --reason is empty; it must say why the exception is approved")
	}
	isScopeDecisionGiven := false
	flags.Visit(func(given *flag.Flag) { isScopeDecisionGiven = isScopeDecisionGiven || given.Name == "scope-decision" })
	if isScopeDecisionGiven && strings.TrimSpace(scopeDecision) == "" {
		// A blank --scope-decision, as an unset variable gives, read as no
		// scope decision would send the ordinary fix and drop the accepted
		// scope the primary CEO answered with.
		return errors.New("pipeline: --scope-decision is empty; it must name an answered task decision sequence")
	}
	scopeDecisionSequence := 0
	scopeDecisionID := ""
	if isScopeDecisionGiven {
		// Accepting a Review scope is the registered primary CFO's own
		// assertion. A gate agent, a goblin or a helper it spawned descends
		// from that process too, so its environment label is what keeps one
		// from minting a decision, and the registration stays open until this
		// command is done so the primary cannot change under it.
		identityErr := errors.New("a gate agent, goblin or helper cannot act as the registered primary CFO")
		if os.Getenv(gateAgentVariable) == "" && os.Getenv(harness.RoleVariable) != harness.RoleGoblin {
			cfo := supervisor.CFOConnection{State: h.State}
			var release func()
			_, release, identityErr = cfo.CallerIdentity()
			if identityErr == nil {
				defer release()
			}
		}
		if identityErr != nil {
			return fmt.Errorf("pipeline: --scope-decision is honoured only from the registered primary CFO: %w", identityErr)
		}
		scopeDecisionSequence, err = supervisor.QuestionSeq(scopeDecision)
		if err != nil {
			return fmt.Errorf("pipeline: --scope-decision %w", err)
		}
		scopeDecisionID = fmt.Sprintf("notify-%s-%d", id, scopeDecisionSequence)
		if scopeDecision != strconv.Itoa(scopeDecisionSequence) && scopeDecision != scopeDecisionID {
			return fmt.Errorf("pipeline: --scope-decision %s is not the question of task %s's decision %d, which is %s", scopeDecision, id, scopeDecisionSequence, scopeDecisionID)
		}
	}
	// acceptedBy is who takes the open findings as they stand: the CFO, or
	// the task itself when its own goblin does.
	acceptedBy := ""
	if response.Accept != "" || response.Reason != "" {
		// Taking open findings as they stand, and the reason an approved Test
		// exception keeps, are the CFO's decisions. The one a goblin makes
		// about its own gate is to take its run's open review findings once
		// the run's review repair cycles are spent (issue 2049), which the
		// gate is checked for below.
		cfo := supervisor.CFOConnection{State: h.State}
		_, release, err := cfo.CallerIdentity()
		switch {
		case err == nil:
			release()
			acceptedBy = "cfo"
		case response.Reason != "":
			given := "--reason"
			if response.Accept != "" {
				given = "--accept and --reason"
			}
			return fmt.Errorf("pipeline: %s is honoured only from the registered primary CFO, since the reason an approved Test exception keeps is never a goblin's: %w", given, err)
		default:
			if goblinErr := ownGoblin(h.State, id); goblinErr != nil {
				return fmt.Errorf("pipeline: --accept is honoured only from the registered primary CFO (%v), or from task %s's own goblin once its run's review repair cycles are spent (%v)", err, id, goblinErr)
			}
			acceptedBy = id
		}
	}
	// This is a pipeline-specific operation lock, not the event/decision
	// ownership ledger. It prevents two CFO commands accepting the same round
	// concurrently, and is deliberately not the cleanup lock: a gate runs for
	// hours, and holding the cleanup lock that long would make an auth refresh
	// report a live task as being cleaned up.
	lockName := state.PipelineLockName(id)
	if _, err := lock.AcquireExclusiveNamed(h.State, lockName); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(h.State, lockName)) }()
	meta, err := state.ReadTaskMeta(h.State, id)
	if err != nil {
		return err
	}
	if meta.Mode != "no-mistakes" {
		// A task of another mode has no gate or frozen pipeline policy, so
		// migration cannot supply one (issue 1438).
		refusal := fmt.Sprintf("pipeline: task %s runs in mode %q, and only a no-mistakes task has a gate to drive or a frozen policy for cfo pipeline migrate to move", meta.ID, meta.Mode)
		switch meta.Mode {
		case "direct-PR":
			refusal += ". A direct-PR task is verified by its pull request's CI: its goblin opens it as a draft, records it with cfo pr check and marks it ready with gh pr ready once, on the head it means to merge"
		case "local-only":
			refusal += fmt.Sprintf(". A local-only task opens no pull request: its goblin stops with a clean branch, which cfo merge-local %s lands", meta.ID)
		}
		return errors.New(refusal)
	}
	if meta.PipelineHash == "" {
		return errors.New("pipeline: task lacks a spawn-time policy; existing tasks require an explicit migration decision")
	}
	expectedTmp := filepath.Join(h.State, "tasktmp", id)
	if !fsx.SamePath(meta.TaskTmp, expectedTmp) {
		return errors.New("pipeline: task temporary path does not match metadata identity")
	}
	if args[0] == "handback" || isAdmission || isPreflight {
		if _, err := os.Lstat(filepath.Join(expectedTmp, policyMigrationJournalName)); err == nil {
			return fmt.Errorf("pipeline: pending policy migration; run cfo pipeline migrate explicitly before %s", args[0])
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("pipeline: policy migration journal cannot be inspected; %s refused", args[0])
		}
	} else if err := resumePipelinePolicyMigration(h, root, meta); err != nil {
		return err
	}
	meta, err = state.ReadTaskMeta(h.State, id)
	if err != nil {
		return err
	}
	if meta.Mode != "no-mistakes" || meta.PipelineHash == "" || !fsx.SamePath(meta.TaskTmp, expectedTmp) {
		return errors.New("pipeline: task metadata changed during policy migration recovery")
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
	if args[0] != "recover" && args[0] != "migrate" && !isAdmission && !isPreflight {
		config := pipeline.Config{Path: filepath.Join(root, "config.yaml"), Policy: selection.Policy}
		drift, err := config.Drift()
		if err != nil {
			return err
		}
		if len(drift) != 0 {
			return fmt.Errorf("pipeline: shared config drift (%s); request idle config-apply, never change a running daemon", strings.Join(drift, ", "))
		}
	}
	if wantRun != "" {
		if isPreflight {
			wantBranch, _, err = reader.AdmissionBranch(ctx, meta.Project, wantRun)
		} else {
			wantBranch, err = reader.RunBranch(ctx, meta.Project, wantRun)
		}
		if err != nil {
			return err
		}
	}
	gated, branch, err := taskWorktree(ctx, commands, meta, wantBranch)
	if err != nil {
		return err
	}
	if branch == "" || branch == "main" || branch == "master" {
		return errors.New("pipeline: isolated feature branch required")
	}
	if args[0] == "migrate" {
		return migratePipelinePolicy(h, root, meta, selection, out)
	}
	if isOwnerCorrection {
		return ownerCorrection(ctx, h, reader, meta, selection, gated, branch, wantRun, correctedFindings, out)
	}
	if isAdmission {
		return pipelineAdmission(ctx, h, reader, meta, selection, gated, branch, args[0], admissionFile, out)
	}
	if isPreflight {
		result, err := commands.Run(ctx, execx.Request{Dir: gated, Env: pipeline.NativeEnv(root), Name: "no-mistakes", Args: []string{"axi", "terminal-preflight", "--predecessor", wantRun}})
		fmt.Fprint(out, string(result.Stdout), string(result.Stderr))
		if err != nil {
			return fmt.Errorf("pipeline: native terminal-preflight failed: %w", err)
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("pipeline: native terminal-preflight exited %d; a native terminal-preflight contract is required; no admission was attempted", result.ExitCode)
		}
		return nil
	}
	if args[0] == "handback" {
		if shouldResume && selection.Policy.HandsBack("ci") {
			isCFO := false
			if os.Getenv(gateAgentVariable) == "" && os.Getenv(harness.RoleVariable) != harness.RoleGoblin {
				cfo := supervisor.CFOConnection{State: h.State}
				if _, release, err := cfo.CallerIdentity(); err == nil {
					release()
					isCFO = true
				}
			}
			if !isCFO {
				if err := checkOwnerCIResume(ctx, h, reader, meta, selection, gated, branch); err != nil {
					return err
				}
			}
		}
		result, err := reader.Handback(ctx, meta.Project, gated, branch, wantRun, receipt, shouldRevalidatePublication, pipeline.NativeEnv(root))
		fmt.Fprint(out, string(result.Stdout), string(result.Stderr))
		if err != nil {
			return fmt.Errorf("pipeline: native handback failed: %w", err)
		}
		if result.ExitCode != 0 {
			if shouldResume && isGateWaitElapsed(result) {
				return fmt.Errorf("%w: the native handback hold elapsed while the same run continues; inspect its status and receipt before an explicit continuation; no replacement run was started", pipeline.ErrUnresolved)
			}
			return fmt.Errorf("pipeline: native handback exited %d; inspect the native refusal above and its status and receipt before any explicit retry; no replacement run was started", result.ExitCode)
		}
		return nil
	}
	if args[0] == "recover" {
		result, err := reader.RecoverKeepLocal(ctx, meta.Project, gated, branch, pipeline.NativeEnv(root))
		if len(result.NativeOutput) > 0 {
			fmt.Fprint(out, string(result.NativeOutput))
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "pipeline custody: recovered run %s; local head preserved at %s; gate branch at %s\n", result.RunID, result.Head, result.GateHead)
		if result.WorktreeEdits != "" {
			fmt.Fprintf(out, "pipeline worktree: run %s's unfinished edits are kept at %s under %s; restore them with git cherry-pick -n %s\n", result.RunID, result.WorktreeEdits, pipeline.WorktreeRef(result.RunID), result.WorktreeEdits)
		}
		return nil
	}
	var nativeArgs []string
	var scopeJournal *reviewScopeJournal
	if args[0] == "respond" {
		gate, err := reader.Gate(ctx, meta.Project, branch)
		if err != nil {
			return err
		}
		if wantRun != "" && gate.RunID != wantRun {
			return fmt.Errorf("pipeline: run %s is no longer the latest run of %s; its gate is run %s's", wantRun, branch, gate.RunID)
		}
		if acceptedBy == id {
			switch {
			case gate.Step != "review":
				return fmt.Errorf("pipeline: a goblin accepts findings only on its run's review step, and this gate is its %s step, whose findings stay the CFO's: ask the CFO with cfo notify %s --blocked \"<question>\"", gate.Step, id)
			case !pipeline.ReviewCyclesSpent(selection, gate):
				return fmt.Errorf("pipeline: a goblin accepts its run's open review findings only once its %d review repair cycles are spent, and run %s's review is in round %d with %d left: fix its auto-fix findings with --action fix --findings <ids>, and ask the CFO about its ask-user ones with cfo notify %s --blocked \"<question>\"", selection.ReviewCycles, gate.RunID, gate.Round, selection.ReviewCycles-gate.Round+1, id)
			}
		}
		// A fix of nothing but a test command that was given no turn runs
		// that command again and asks no repair agent, so it stays the retry
		// of a repaired environment.
		if response.Action == "fix" && selection.Policy.HandsBack(gate.Step) && !(gate.Step == "test" && response.Findings == "test-command-no-turn") {
			return fmt.Errorf("pipeline: under policy version %d the gate repairs no %s failure, so no fix round starts: run %s's findings are task %s's own to fix in one commit, which it submits with cfo pipeline validate-authored %s --run %s --findings <ids>, or for CI with cfo pipeline handback %s --run %s", selection.Policy.Version, gate.Step, gate.RunID, id, id, gate.RunID, id, gate.RunID)
		}
		nativeArgs, err = pipeline.ResponseArgs(selection, gate, response)
		if errors.Is(err, pipeline.ErrUnresolved) && response.Accept == "" && pipeline.ReviewCyclesSpent(selection, gate) {
			return fmt.Errorf("%w, or task %s's own goblin's accept: run %s's review repair cycles are spent, so it may take its open ask-user and auto-fix findings as they stand with --action approve --accept <ids>, naming every one, and put a real defect among them in one follow-up commit after its first CI; any other open finding still needs the CFO", err, id, gate.RunID)
		}
		if err != nil {
			return err
		}
		if scopeDecisionSequence != 0 {
			if gate.Step != "review" || response.Action != "fix" || response.Instructions != "" {
				return errors.New("pipeline: --scope-decision requires a Review fix with no ordinary instructions")
			}
			acceptedScope, err := supervisor.DecisionAnswer(h.State, meta, scopeDecisionSequence)
			if err != nil {
				return fmt.Errorf("pipeline: --scope-decision %d: %w", scopeDecisionSequence, err)
			}
			encoded, err := reader.ReviewScopeDecision(ctx, gated, pipeline.NativeEnv(root), gate, response.Findings, scopeDecisionID, acceptedScope)
			if err != nil {
				return err
			}
			scopeJournal, err = prepareReviewScopeJournal(meta.TaskTmp, encoded)
			if err != nil {
				return err
			}
			defer os.Remove(scopeJournal.file)
			if !slices.Contains(nativeArgs, "--run") {
				nativeArgs = append(nativeArgs, "--run", gate.RunID)
			}
			nativeArgs = append(nativeArgs, "--scope-decision-file", scopeJournal.file)
		}
		if response.Accept != "" {
			audit := fmt.Sprintf("pipeline-findings-accepted: step=%s run=%s round=%d findings=%s by=%s", gate.Step, gate.RunID, gate.Round, response.Accept, acceptedBy)
			if err := state.AppendStatus(h.State, id, audit); err != nil {
				return err
			}
			// The CFO reads every accept a goblin makes, and nothing waits on
			// it: an approve the CFO is never told of is not sent.
			if acceptedBy == id {
				if _, err := wake.Append(h.State, "notify", id, fmt.Sprintf("%s; its own goblin accepted them once its %d review repair cycles were spent, and nothing waits on this", audit, selection.ReviewCycles)); err != nil {
					return fmt.Errorf("pipeline: the CFO cannot be told of this accept, so approve was not sent: %w", err)
				}
				if _, err := wake.PublishEpisode(h.State); err != nil {
					fmt.Fprintf(out, "pipeline: the CFO's notice of this accept is queued, but its wake could not be published: %v\n", err)
				}
			}
		}
		if response.Reason != "" {
			if err := state.AppendStatus(h.State, id, fmt.Sprintf("pipeline-approval-reason: step=%s run=%s round=%d reason=%q by=cfo", gate.Step, gate.RunID, gate.Round, response.Reason)); err != nil {
				return err
			}
		}
	} else {
		environment, err := pipeline.ProjectTestEnvironment(ctx, commands, h.Data, meta.Project, meta.Worktree)
		if err != nil {
			return err
		}
		if len(environment) != 0 {
			if err := pipeline.CheckTestEnvironmentWrapper(root); err != nil {
				return err
			}
		}
		check, err := reader.CheckStart(ctx, meta.Project, gated, branch, intent, selection.Policy)
		if err != nil {
			return err
		}
		var start pipeline.Start
		if check.ReattachRun != "" {
			// The native bounded wait expiring is not a failed run: the run
			// keeps working, and this start reattaches to it. no-mistakes starts
			// a run only from an intent, so a start that names none reattaches
			// to the branch's live run and keeps its intent, head and review
			// budget, where a launch selection of the same run changes nothing.
			start = pipeline.Start{Args: []string{"axi", "run"}}
			fmt.Fprintf(out, "pipeline: reattached to run %s on %s, which keeps its own intent and review budget; no new run starts\n", check.ReattachRun, branch)
		} else {
			start, err = pipeline.StartOf(ctx, commands, gated, pipeline.NativeEnv(root), intent)
			if err != nil {
				return fmt.Errorf("pipeline: %w", err)
			}
			if start.IsOnCommandLine {
				fmt.Fprintln(out, "pipeline: this no-mistakes takes a gate's intent on its command line only, where Microsoft Defender can read a long one as a lure and Windows can refuse the start. no-mistakes 1.86.0 and later take it from a file")
			}
		}
		defer start.Close()
		nativeArgs = start.Args
		if check.ReattachRun == "" && selection.Policy.Version > 5 {
			if err := pipeline.GateHarness(meta.Harness); err != nil {
				return fmt.Errorf("%w. Task %s runs on %s, so switch it with cfo switch %s --harness <harness>", err, meta.ID, meta.Harness, meta.ID)
			}
			launch, err := launchSelectionArgs(ctx, commands, root, meta, selection, check.TrustedHead, out)
			if err != nil {
				return err
			}
			nativeArgs = append(nativeArgs, launch...)
		}
	}
	native := execx.Request{Dir: gated, Env: pipeline.NativeEnv(root), Name: "no-mistakes", Args: nativeArgs}
	result, err := commands.Run(ctx, native)
	// A missed health reply stops native before its request reaches the
	// daemon, so repeating it changes nothing twice. A live daemon that missed
	// it is busy serving other runs, not stopped, so this waits its turn
	// instead of handing the goblin a restart hint and a retry loop of its own.
	for turn := 2; err == nil && pipeline.IsDaemonHealthMissed(result) && turn <= daemonTurns; turn++ {
		daemon, _ := reader.Daemon(ctx)
		if daemon.State != pipeline.DaemonAlive {
			break
		}
		fmt.Fprintf(out, "pipeline: the gate daemon (pid %d) is alive and serving %s, and missed its short health reply before this %s reached it, so it is busy, not stopped; waiting %s for its turn (try %d of %d), and never starting or restarting it\n", daemon.PID, liveRunCount(daemon.LiveRuns), args[0], daemonTurnWait, turn, daemonTurns)
		daemonTurnSleep(daemonTurnWait)
		result, err = commands.Run(ctx, native)
	}
	if len(result.Stdout) > 0 {
		fmt.Fprint(out, string(result.Stdout))
	}
	if len(result.Stderr) > 0 {
		fmt.Fprint(out, string(result.Stderr))
	}
	if scopeJournal != nil {
		if recordErr := scopeJournal.Record(h, meta.ID, result, err); recordErr != nil {
			return errors.Join(err, recordErr)
		}
		if err == nil && result.ExitCode == 0 {
			// Native's exit names its own acknowledgement of the decision ID.
			// The durable record is required as well, so an acknowledged
			// acceptance this controller cannot show is still refused rather
			// than reported as delivered.
			if confirmErr := reader.ConfirmReviewScope(ctx, gated, pipeline.NativeEnv(root), scopeJournal.request); confirmErr != nil {
				return fmt.Errorf("pipeline: native did not confirm the accepted Review scope; the exact outcome is recorded in %s, so inspect its run before any explicit identical replay: %w", reviewScopeJournalFile, confirmErr)
			}
			if auditErr := state.AppendStatus(h.State, meta.ID, "pipeline-review-scope-confirmed: "+scopeJournal.describe()); auditErr != nil {
				return auditErr
			}
		}
	}
	if err != nil {
		if scopeJournal != nil {
			return fmt.Errorf("pipeline: native scope response failed; inspect the recorded outcome before any explicit identical replay: %w", err)
		}
		return fmt.Errorf("pipeline: native command failed: %w", err)
	}
	if result.ExitCode != 0 {
		if scopeJournal != nil {
			return fmt.Errorf("pipeline: native scope response exited %d; its exact outcome is recorded in %s and no ordinary response or retry was attempted", result.ExitCode, reviewScopeJournalFile)
		}
		if args[0] == "run" && selection.Policy.Version > 5 && isLaunchSelectionUnknown(result) {
			return fmt.Errorf("pipeline: native command exited %d. This no-mistakes build takes no launch selection, which policy version %d needs to run a gate on its task's own harness. See docs/pipeline.md for the build that does", result.ExitCode, selection.Policy.Version)
		}
		if args[0] == "run" && isGateWaitElapsed(result) {
			// no-mistakes bounds its hold so a harness tool cap cannot hang it,
			// and says in its own output that an elapsed wait is not a failed
			// run. Name the start that reattaches instead of only the exit
			// code, since the caller's next command is the reattachment.
			live := stillRunningGateRun(ctx, reader, meta.Project, branch)
			where := "the run"
			statusCommand := "no-mistakes axi status"
			if live != "" {
				where = "run " + live
				statusCommand = "no-mistakes axi status --run " + live
			}
			return fmt.Errorf("pipeline: the gate's bounded hold ended with no decision, which is not a gate failure and leaves %s working on %s. Repeat this command to reattach to that run on the same branch, head and intent, starting no new run and resetting no review budget, or read it with %s and answer its next gate with cfo pipeline respond %s", where, branch, statusCommand, meta.ID)
		}
		if pipeline.IsDaemonReplyMissed(result) {
			return daemonReplyMissed(ctx, reader, meta, branch, args[0])
		}
		return fmt.Errorf("pipeline: native command exited %d", result.ExitCode)
	}
	if selection.Policy.Version >= 7 {
		return reportHandback(ctx, h, reader, meta, selection, gated, branch, out)
	}
	return nil
}

// ownGoblin says why this process is not task id's own goblin, or nil when it
// is: it runs in that task's terminal, proven by a proof the terminal was
// given, as a run's end is told only to its own goblin. An agent a gate
// started is none, though it carries the environment of whichever goblin
// started the shared gate daemon.
func ownGoblin(stateDir, id string) error {
	terminal, proof := os.Getenv(host.IDVariable), os.Getenv(host.ProofVariable)
	switch {
	case os.Getenv(gateAgentVariable) != "":
		return errors.New("an agent a gate started speaks for no goblin")
	case os.Getenv(harness.RoleVariable) != harness.RoleGoblin || terminal == "" || proof == "":
		return errors.New("this process runs in no goblin's terminal")
	case terminal != id:
		return fmt.Errorf("this process runs in task %s's terminal, and a goblin accepts only its own task's run", terminal)
	}
	proofs, err := host.Proofs(stateDir, id)
	if err != nil {
		return fmt.Errorf("task %s's terminal proofs cannot be read: %w", id, err)
	}
	if !slices.Contains(proofs, host.ProofSum(proof)) {
		return fmt.Errorf("this process carries no proof task %s's terminal was given", id)
	}
	return nil
}

// A native command a live daemon turned away at its health reply is made
// again daemonTurnWait later, daemonTurns times in all, so a busy daemon's
// next free moment starts it. daemonTurnSleep is how that wait is made.
var (
	daemonTurns     = 4
	daemonTurnWait  = 30 * time.Second
	daemonTurnSleep = time.Sleep
)

// liveRunCount names how many runs the gate daemon serves.
func liveRunCount(runs int) string {
	switch {
	case runs < 0:
		return "runs its database could not count"
	case runs == 1:
		return "1 run"
	}
	return fmt.Sprintf("%d runs", runs)
}

// daemonReplyMissed says what a daemon's missed reply means for this
// command. A live daemon is busy, never stopped, whatever native's own hint
// says, and the run the branch already holds is the one a repeat reaches, so
// a lost or late reply to a start never becomes a second run.
func daemonReplyMissed(ctx context.Context, reader pipeline.Reader, meta state.TaskMeta, branch, command string) error {
	daemon, err := reader.Daemon(ctx)
	if daemon.State != pipeline.DaemonAlive {
		read := ""
		if err != nil {
			read = ": " + err.Error()
		}
		return fmt.Errorf("pipeline: the gate daemon missed its reply and its process reads %s%s. It serves every goblin, so never start or restart it: ask the CEO with cfo notify %s --blocked \"<question>\"", daemon.State, read, meta.ID)
	}
	busy := fmt.Sprintf("pipeline: the gate daemon (pid %d) is alive and serving %s and missed its reply, so it is busy, not stopped: whatever native's hint above says, never start or restart it", daemon.PID, liveRunCount(daemon.LiveRuns))
	run, err := reader.Interruption(ctx, meta.Project, branch)
	if err != nil && !errors.Is(err, pipeline.ErrNoProgress) {
		return fmt.Errorf("%s. The run on %s could not be read (%v), so check it with no-mistakes axi status before you act again", busy, branch, err)
	}
	isLive := err == nil && !run.IsTerminal()
	switch {
	case command == "respond" && isLive:
		return fmt.Errorf("%s. Your answer may have reached run %s, so read it with no-mistakes axi status --run %s before you answer again", busy, run.ID, run.ID)
	case isLive:
		return fmt.Errorf("%s. Run %s already holds %s, so repeating this command in a few minutes reattaches to it and starts no second run", busy, run.ID, branch)
	case command == "run":
		return fmt.Errorf("%s. No run was created on %s, so repeating this command in a few minutes starts it", busy, branch)
	}
	return fmt.Errorf("%s. %s holds no live run now, so read it with no-mistakes axi status before you act again", busy, branch)
}

// stillRunningGateRun is the gate run still working on the branch, and empty
// when the run has finished or its state cannot be read.
func stillRunningGateRun(ctx context.Context, reader pipeline.Reader, project, branch string) string {
	run, err := reader.Interruption(ctx, project, branch)
	if err != nil || run.IsTerminal() {
		return ""
	}
	return run.ID
}

// isGateWaitElapsed reports no-mistakes' own return for a bounded hold that
// ended with no decision, which it marks in the document it prints and which is
// not a gate failure. No other native return carries that line, so a real
// refusal is never reported as a wait.
func isGateWaitElapsed(result execx.Result) bool {
	said := append(append([]byte(nil), result.Stdout...), result.Stderr...)
	return bytes.Contains(said, []byte("elapsed while driving the run"))
}

// taskWorktree is the worktree a gate command acts in and the branch checked
// out there: the task's own worktree, or, for a branch named with --branch or
// --run, whichever of the task's worktrees has it checked out, its own or an
// extra one cfo worktree add recorded. A native command answers the latest
// run of the branch checked out where it runs, so the branch decides where.
func taskWorktree(ctx context.Context, commands execx.Runner, meta state.TaskMeta, branch string) (string, string, error) {
	checkedOut := func(path string) (string, bool) {
		result, err := commands.Run(ctx, execx.Request{Dir: path, Name: "git", Args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}})
		if err != nil || result.ExitCode != 0 {
			return "", false
		}
		return strings.TrimSpace(string(result.Stdout)), true
	}
	if branch == "" {
		head, ok := checkedOut(meta.Worktree)
		if !ok {
			return "", "", errors.New("pipeline: named task branch required")
		}
		return meta.Worktree, head, nil
	}
	for _, path := range append([]string{meta.Worktree}, meta.Extras...) {
		if head, ok := checkedOut(path); !ok || head != branch {
			continue
		}
		if err := worktree.Validate(ctx, worktree.RunnerGit{Commands: commands}, meta.Project, path); err != nil {
			return "", "", err
		}
		return path, branch, nil
	}
	return "", "", fmt.Errorf("pipeline: branch %s is checked out in none of task %s's worktrees", branch, meta.ID)
}

// migratePipelinePolicy moves a task's frozen snapshot to the current policy,
// which the shared config must already run. It writes only the task's own
// snapshot, metadata and status, never the shared config, so it needs neither
// the daemon stopped nor the machine's gate runs finished.
func migratePipelinePolicy(h home.Home, root string, meta state.TaskMeta, old pipeline.Selection, out io.Writer) (err error) {
	current, err := pipeline.Load(filepath.Join(h.Root, "config", "pipeline.json"))
	if err != nil {
		return err
	}
	next, err := pipeline.MigrateSelection(old, current)
	if err != nil {
		return err
	}
	if err := requireAppliedPipelinePolicy(root, current); err != nil {
		return err
	}
	if next == old {
		fmt.Fprintf(out, "pipeline policy: task %s already uses %s\n", meta.ID, next.Hash)
		return nil
	}
	if _, err := lock.AcquireExclusiveNamed(h.State, state.CleanupLockName(meta.ID)); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(h.State, state.CleanupLockName(meta.ID))) }()
	if _, err := lock.AcquireExclusiveNamed(h.State, state.MetadataLockName(meta.ID)); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(h.State, state.MetadataLockName(meta.ID))) }()

	metaPath := filepath.Join(h.State, meta.ID+".meta")
	values, err := state.ReadMeta(metaPath)
	if err != nil {
		return err
	}
	if values["pipeline_hash"] != old.Hash || values["pipeline_class"] != old.Class {
		return errors.New("pipeline: task metadata changed during policy migration")
	}
	journal := policyMigrationJournal{Version: 1, TaskID: meta.ID, Direction: "forward", Old: old, New: next, Audit: pipelineMigrationAudit(old, next)}
	journalPath := filepath.Join(meta.TaskTmp, policyMigrationJournalName)
	if err := writePolicyMigrationJournal(journalPath, journal); err != nil {
		return err
	}
	if err := applyPolicyMigration(h, meta, journalPath, journal, false); err != nil {
		return err
	}
	fmt.Fprintf(out, "pipeline policy: migrated task %s class %s with %d review cycles; %s -> %s\n", meta.ID, old.Class, old.ReviewCycles, old.Hash, next.Hash)
	return nil
}

const policyMigrationJournalName = "pipeline-migration.json"

type policyMigrationJournal struct {
	Version   int                `json:"version"`
	TaskID    string             `json:"task_id"`
	Direction string             `json:"direction"`
	Old       pipeline.Selection `json:"old"`
	New       pipeline.Selection `json:"new"`
	Audit     string             `json:"audit"`
}

func pipelineMigrationAudit(old, next pipeline.Selection) string {
	return fmt.Sprintf("pipeline-policy-migrated: class=%s review_cycles=%d old=%s new=%s", old.Class, old.ReviewCycles, old.Hash, next.Hash)
}

func (j policyMigrationJournal) validate() error {
	if j.Version != 1 || state.ValidTaskID(j.TaskID) != nil || j.Direction != "forward" && j.Direction != "rollback" {
		return errors.New("pipeline: invalid policy migration journal")
	}
	if err := j.Old.Validate(); err != nil {
		return errors.New("pipeline: invalid old policy in migration journal")
	}
	if err := j.New.Validate(); err != nil {
		return errors.New("pipeline: invalid new policy in migration journal")
	}
	want, err := pipeline.MigrateSelection(j.Old, j.New.Policy)
	// Two snapshots of one version differ only from version 6, in the fallback
	// the operator names, and MigrateSelection refuses every other such pair.
	if err != nil || j.Old.Policy.Version > j.New.Policy.Version || j.Old == j.New || want != j.New || j.Old.Class != j.New.Class || j.Old.ReviewCycles != j.New.ReviewCycles || j.Audit != pipelineMigrationAudit(j.Old, j.New) {
		return errors.New("pipeline: inconsistent policy migration journal")
	}
	return nil
}

func writePolicyMigrationJournal(path string, journal policyMigrationJournal) error {
	if err := journal.validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, append(data, '\n'))
}

func loadPolicyMigrationJournal(path string) (policyMigrationJournal, error) {
	data, err := fsx.ReadFile(path)
	if err != nil {
		return policyMigrationJournal{}, err
	}
	if len(data) > 1<<20 {
		return policyMigrationJournal{}, errors.New("pipeline: policy migration journal is too large")
	}
	var journal policyMigrationJournal
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&journal); err != nil {
		return policyMigrationJournal{}, errors.New("pipeline: invalid policy migration journal")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return policyMigrationJournal{}, errors.New("pipeline: trailing policy migration journal data")
	}
	return journal, journal.validate()
}

func resumePipelinePolicyMigration(h home.Home, root string, meta state.TaskMeta) (err error) {
	journalPath := filepath.Join(meta.TaskTmp, policyMigrationJournalName)
	journal, err := loadPolicyMigrationJournal(journalPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if journal.TaskID != meta.ID {
		return errors.New("pipeline: policy migration journal belongs to another task")
	}
	if err := requireAppliedPipelinePolicy(root, journal.New.Policy); err != nil {
		return err
	}
	if _, err := lock.AcquireExclusiveNamed(h.State, state.CleanupLockName(meta.ID)); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(h.State, state.CleanupLockName(meta.ID))) }()
	if _, err := lock.AcquireExclusiveNamed(h.State, state.MetadataLockName(meta.ID)); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(h.State, state.MetadataLockName(meta.ID))) }()
	return applyPolicyMigration(h, meta, journalPath, journal, true)
}

func requireAppliedPipelinePolicy(root string, policy pipeline.Policy) error {
	config := pipeline.Config{Path: filepath.Join(root, "config.yaml"), Policy: policy}
	drift, err := config.Drift()
	if err != nil {
		return err
	}
	if len(drift) != 0 {
		return fmt.Errorf("pipeline: apply current shared config before migrating tasks (%s)", strings.Join(drift, ", "))
	}
	return nil
}

var errPolicyMigrationAuditUncertain = errors.New("pipeline: policy migration audit state is uncertain")

func applyPolicyMigration(h home.Home, meta state.TaskMeta, journalPath string, journal policyMigrationJournal, resuming bool) error {
	if journal.Direction == "rollback" {
		return rollbackPolicyMigration(h, meta, journalPath, journal, nil)
	}
	if err := journal.New.Save(filepath.Join(meta.TaskTmp, "pipeline.json")); err != nil {
		return rollbackPolicyMigration(h, meta, journalPath, journal, err)
	}
	if err := setPolicyMigrationMeta(filepath.Join(h.State, meta.ID+".meta"), journal, true); err != nil {
		return rollbackPolicyMigration(h, meta, journalPath, journal, err)
	}
	if err := ensurePolicyMigrationAudit(h.State, meta.ID, journal.Audit, resuming); err != nil {
		if errors.Is(err, errPolicyMigrationAuditUncertain) {
			return err
		}
		return rollbackPolicyMigration(h, meta, journalPath, journal, err)
	}
	return os.Remove(journalPath)
}

func rollbackPolicyMigration(h home.Home, meta state.TaskMeta, journalPath string, journal policyMigrationJournal, cause error) error {
	journal.Direction = "rollback"
	journalErr := writePolicyMigrationJournal(journalPath, journal)
	snapshotErr := journal.Old.Save(filepath.Join(meta.TaskTmp, "pipeline.json"))
	metaErr := setPolicyMigrationMeta(filepath.Join(h.State, meta.ID+".meta"), journal, false)
	var removeErr error
	if snapshotErr == nil && metaErr == nil {
		removeErr = os.Remove(journalPath)
	}
	return errors.Join(cause, journalErr, snapshotErr, metaErr, removeErr)
}

func setPolicyMigrationMeta(path string, journal policyMigrationJournal, forward bool) error {
	values, err := state.ReadMeta(path)
	if err != nil {
		return err
	}
	if values["pipeline_class"] != journal.Old.Class || values["pipeline_hash"] != journal.Old.Hash && values["pipeline_hash"] != journal.New.Hash {
		return errors.New("pipeline: task metadata changed during policy migration")
	}
	if forward {
		values["pipeline_hash"] = journal.New.Hash
	} else {
		values["pipeline_hash"] = journal.Old.Hash
	}
	return state.WriteMeta(path, values)
}

func ensurePolicyMigrationAudit(stateDir, id, audit string, resuming bool) error {
	return ensurePolicyMigrationAuditWith(resuming, func() (bool, error) {
		return hasPolicyMigrationAudit(stateDir, id, audit)
	}, func() error {
		return state.AppendStatus(stateDir, id, audit)
	})
}

func ensurePolicyMigrationAuditWith(resuming bool, contains func() (bool, error), appendAudit func() error) error {
	found, err := contains()
	if err != nil {
		if resuming {
			return errors.Join(errPolicyMigrationAuditUncertain, err)
		}
		return err
	}
	if found {
		return nil
	}
	appendErr := appendAudit()
	if appendErr == nil {
		return nil
	}
	found, readErr := contains()
	if found {
		return nil
	}
	if readErr != nil {
		return errors.Join(errPolicyMigrationAuditUncertain, appendErr, readErr)
	}
	return appendErr
}

func hasPolicyMigrationAudit(stateDir, id, audit string) (bool, error) {
	lines, err := state.TailStatus(stateDir, id, int(^uint(0)>>1))
	if err != nil {
		return false, err
	}
	for _, line := range lines {
		_, event := state.SplitStatus(line)
		if event == audit {
			return true, nil
		}
	}
	return false, nil
}
