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
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

func runPipeline(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "cfo pipeline: config-drift, config-apply, migrate, run, respond or recover required")
		return 2
	}
	if args[0] != "config-drift" && args[0] != "config-apply" && args[0] != "migrate" && args[0] != "run" && args[0] != "respond" && args[0] != "recover" {
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
	} else if args[0] == "respond" {
		flags.StringVar(&response.Action, "action", "", "fix or approve")
		flags.StringVar(&response.Findings, "findings", "", "comma-separated finding IDs")
		flags.StringVar(&response.Instructions, "instructions", "", "guidance for selected findings")
		flags.StringVar(&response.Accept, "accept", "", "with approve: every open ask-user and auto-fix finding the CFO accepts as it stands")
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
	if response.Accept != "" {
		// Taking open findings as they stand is the CFO's decision, never a
		// goblin's about its own gate.
		cfo := supervisor.CFOConnection{State: h.State, Herdr: &herdr.Client{Commands: commands}}
		_, release, err := cfo.CallerIdentity(ctx)
		if err != nil {
			return fmt.Errorf("pipeline: --accept is honoured only from the registered primary CFO: %w", err)
		}
		release()
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
	if meta.Mode != "no-mistakes" || meta.PipelineHash == "" {
		return errors.New("pipeline: task lacks a spawn-time policy; existing tasks require an explicit migration decision")
	}
	expectedTmp := filepath.Join(h.State, "tasktmp", id)
	if !fsx.SamePath(meta.TaskTmp, expectedTmp) {
		return errors.New("pipeline: task temporary path does not match metadata identity")
	}
	if err := resumePipelinePolicyMigration(ctx, h, reader, meta); err != nil {
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
	if args[0] != "recover" && args[0] != "migrate" {
		config := pipeline.Config{Path: filepath.Join(root, "config.yaml"), Policy: selection.Policy}
		drift, err := config.Drift()
		if err != nil {
			return err
		}
		if len(drift) != 0 {
			return fmt.Errorf("pipeline: shared config drift (%s); request idle config-apply, never change a running daemon", strings.Join(drift, ", "))
		}
	}
	branchResult, err := commands.Run(ctx, execx.Request{Dir: meta.Worktree, Name: "git", Args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}})
	if err != nil || branchResult.ExitCode != 0 {
		return errors.New("pipeline: named task branch required")
	}
	branch := strings.TrimSpace(string(branchResult.Stdout))
	if branch == "" || branch == "main" || branch == "master" {
		return errors.New("pipeline: isolated feature branch required")
	}
	if args[0] == "migrate" {
		return migratePipelinePolicy(ctx, h, root, reader.Idle, meta, selection, out)
	}
	if args[0] == "recover" {
		result, err := reader.RecoverKeepLocal(ctx, meta.Project, meta.Worktree, branch, nativeEnv(root))
		if len(result.NativeOutput) > 0 {
			fmt.Fprint(out, string(result.NativeOutput))
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "pipeline custody: recovered run %s; local and gate head preserved at %s\n", result.RunID, result.Head)
		return nil
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
		if response.Accept != "" {
			if err := state.AppendStatus(h.State, id, fmt.Sprintf("pipeline-findings-accepted: step=%s run=%s round=%d findings=%s by=cfo", gate.Step, gate.RunID, gate.Round, response.Accept)); err != nil {
				return err
			}
		}
	} else {
		if err := reader.CheckStart(ctx, meta.Project, meta.Worktree, branch, selection.Policy); err != nil {
			return err
		}
	}
	result, err := commands.Run(ctx, execx.Request{Dir: meta.Worktree, Env: nativeEnv(root), Name: "no-mistakes", Args: nativeArgs})
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

func migratePipelinePolicy(ctx context.Context, h home.Home, root string, idle func(context.Context) (func() error, error), meta state.TaskMeta, old pipeline.Selection, out io.Writer) (err error) {
	current, err := pipeline.Load(filepath.Join(h.Root, "config", "pipeline.json"))
	if err != nil {
		return err
	}
	next, err := pipeline.MigrateSelection(old, current)
	if err != nil {
		return err
	}
	releaseIdle, err := idle(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, releaseIdle()) }()
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
	if err != nil || j.Old.Policy.Version != 1 || j.New.Policy.Version != 2 || want != j.New || j.Old.Class != j.New.Class || j.Old.ReviewCycles != j.New.ReviewCycles || j.Audit != pipelineMigrationAudit(j.Old, j.New) {
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
	data, err := os.ReadFile(path)
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

func resumePipelinePolicyMigration(ctx context.Context, h home.Home, reader pipeline.Reader, meta state.TaskMeta) (err error) {
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
	releaseIdle, err := reader.Idle(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, releaseIdle()) }()
	if err := requireAppliedPipelinePolicy(reader.Root, journal.New.Policy); err != nil {
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

// nativeEnv points the native engine at the resolved root while leaving it the
// CFO's own environment. execx replaces rather than merges a non-nil Env, so a
// bare NM_HOME entry would strip PATH, USERPROFILE and TEMP and the engine
// could resolve neither its tools nor a home directory.
func nativeEnv(root string) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		// Windows matches environment names without case, so an existing
		// NM_HOME has to be dropped rather than left beside the override.
		name, _, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(name, "NM_HOME") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "NM_HOME="+root)
}
