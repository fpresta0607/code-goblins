package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	if args[0] != "respond" {
		checked, err := capturePipelineLaunch(ctx, h, root, reader, meta, branch, selection)
		if err != nil {
			return err
		}
		nonce, err := randomLaunchValue()
		if err != nil {
			return err
		}
		generationValue, err := randomLaunchValue()
		if err != nil {
			return err
		}
		generation := cfoValidationGenerationPrefix + generationValue
		sqlitePath, err := exec.LookPath("sqlite3")
		if err != nil {
			return errors.New("pipeline: sqlite3 executable required for managed native launch")
		}
		sqlitePath, err = filepath.Abs(sqlitePath)
		if err != nil {
			return err
		}
		cfoExecutablePath, cfoExecutableSHA256, err := currentCFOExecutableEvidence()
		if err != nil {
			return err
		}
		contract := pipelineLaunchContract{Version: 1, Status: pipelineLaunchContractPending, TaskID: id, PolicyHash: selection.Hash, Project: meta.Project, Checked: checked.Start, ConfigSHA256: checked.ConfigSHA256, LaunchNonce: nonce, ValidationGeneration: generation, SQLitePath: sqlitePath, CFOExecutablePath: cfoExecutablePath, CFOExecutableSHA256: cfoExecutableSHA256}
		contractPath := filepath.Join(expectedTmp, pipelineLaunchContractName)
		if err := savePipelineLaunchContract(contractPath, contract); err != nil {
			return err
		}
		claimPath := filepath.Join(expectedTmp, pipelineLaunchClaimName)
		if err := savePipelineLaunchClaim(claimPath, pipelineLaunchClaimForContract(contract)); err != nil {
			return err
		}
		binding := pipelineNativeGateBindingForContract(contract, meta.Worktree)
		bindingPath := pipelineNativeGateBindingPath(h.State, contract.Checked.RepoID)
		if err := createPipelineNativeGateBinding(bindingPath, binding); err != nil {
			return err
		}
		bindingSettled := false
		bindingLaunched := false
		defer func() {
			if !bindingSettled {
				if bindingLaunched {
					err = errors.Join(err, revokePipelineNativeGateBinding(bindingPath, binding))
				} else {
					err = errors.Join(err, retirePipelineNativeGateBinding(bindingPath, binding))
				}
			}
		}()
		contractRetained := false
		defer func() {
			if contractRetained {
				return
			}
			current, loadErr := loadPipelineLaunchContract(contractPath)
			if loadErr != nil {
				err = errors.Join(err, loadErr)
				return
			}
			if !samePipelineLaunchContractIdentity(current, contract) {
				err = errors.Join(err, errors.New("pipeline: managed launch contract identity changed before revocation"))
				return
			}
			revoked := current
			revoked.Status = pipelineLaunchContractRevoked
			if revokeErr := transitionPipelineLaunchContract(contractPath, current, revoked); revokeErr != nil {
				err = errors.Join(err, revokeErr)
			}
		}()
		latest, err := capturePipelineLaunch(ctx, h, root, reader, meta, branch, selection)
		if err != nil {
			return err
		}
		if latest != checked {
			return errors.New("pipeline: launch evidence changed before native invocation")
		}
		nativeArgs := []string{"axi", "run", "--intent", intent, "--launch-nonce", nonce, "--validation-generation", generation}
		bindingLaunched = true
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
		receipt, err := parseLaunchReceipt(result.Stdout)
		if err != nil {
			if result.ExitCode != 0 {
				return fmt.Errorf("pipeline: native command exited %d", result.ExitCode)
			}
			return err
		}
		if err := receipt.verify(checked.Start, nonce, generation, intent); err != nil {
			return err
		}
		current, err := loadPipelineLaunchContract(contractPath)
		if err != nil || !samePipelineLaunchContractIdentity(current, contract) {
			return errors.Join(errors.New("pipeline: managed launch contract changed before receipt verification"), err)
		}
		if current.RunID == "" {
			current, err = bindPipelineLaunchContract(contractPath, current, receipt.RunID, current.Status)
			if err != nil {
				return err
			}
		}
		if current.RunID != receipt.RunID {
			return errors.New("pipeline: native receipt run does not match the managed launch contract")
		}
		var launchState pipeline.NativeLaunchState
		current, launchState, err = verifyPipelineLaunchContract(ctx, reader, contractPath, current, selection)
		if err != nil {
			return err
		}
		if _, err := activatePipelineNativeGateBinding(bindingPath, binding, receipt.RunID, launchState.Worktree); err != nil {
			return err
		}
		if launchState.InvocationCount == 0 {
			contractRetained = true
			bindingSettled = true
			fmt.Fprintf(out, "pipeline launch: bound pending run %s at %s before its first managed agent\n", receipt.RunID, checked.Start.HeadSHA)
			if result.ExitCode != 0 {
				return fmt.Errorf("pipeline: native command exited %d", result.ExitCode)
			}
			return nil
		}
		contractRetained = true
		if !launchState.ReviewProvenance {
			bindingSettled = true
			fmt.Fprintf(out, "pipeline launch: authorized run %s after its first rebase agent; review provenance is pending\n", receipt.RunID)
			if result.ExitCode != 0 {
				return fmt.Errorf("pipeline: native command exited %d", result.ExitCode)
			}
			return nil
		}
		fmt.Fprintf(out, "pipeline launch: verified run %s at %s with trusted %s and primary %s\n", receipt.RunID, checked.Start.HeadSHA, checked.Start.TrustedSHA, checked.Start.EffectivePrimary)
		if result.ExitCode != 0 {
			bindingSettled = true
			return fmt.Errorf("pipeline: native command exited %d", result.ExitCode)
		}
		if err := retirePipelineNativeGateBinding(bindingPath, binding); err != nil {
			return err
		}
		bindingSettled = true
		return nil
	}
	gate, err := reader.Gate(ctx, meta.Project, branch)
	if err != nil {
		return err
	}
	contractPath := filepath.Join(expectedTmp, pipelineLaunchContractName)
	contract, launchState, err := preparePipelineLaunchContract(ctx, reader, contractPath, gate.RunID, selection)
	if err != nil {
		return err
	}
	nativeArgs, err := pipeline.ResponseArgs(selection, gate, response)
	if err != nil {
		return err
	}
	binding := pipelineNativeGateBindingForContract(contract, meta.Worktree)
	bindingPath := pipelineNativeGateBindingPath(h.State, contract.Checked.RepoID)
	if err := ensurePipelineNativeGateBinding(bindingPath, binding, contract.RunID, launchState.Worktree); err != nil {
		return err
	}
	bindingSettled := false
	defer func() {
		if !bindingSettled {
			err = errors.Join(err, revokePipelineNativeGateBinding(bindingPath, binding))
		}
	}()
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
	if contract.Status == pipelineLaunchContractPending || contract.Status == pipelineLaunchContractAuthorized {
		current, loadErr := loadPipelineLaunchContract(contractPath)
		if loadErr != nil || !samePipelineLaunchContractIdentity(current, contract) || current.RunID != contract.RunID {
			return errors.Join(errors.New("pipeline: managed launch contract changed during native response"), loadErr)
		}
		verified, launchState, err := verifyPipelineLaunchContract(ctx, reader, contractPath, current, selection)
		if err != nil {
			return err
		}
		if launchState.InvocationCount > 0 {
			fmt.Fprintf(out, "pipeline launch: verified run %s after its first managed agent\n", verified.RunID)
		}
	}
	if result.ExitCode != 0 {
		bindingSettled = true
		return fmt.Errorf("pipeline: native command exited %d", result.ExitCode)
	}
	if err := retirePipelineNativeGateBinding(bindingPath, binding); err != nil {
		return err
	}
	bindingSettled = true
	return nil
}

func nativeLaunchExpectation(contract pipelineLaunchContract, selection pipeline.Selection) pipeline.NativeLaunchExpectation {
	return pipeline.NativeLaunchExpectation{
		RunID: contract.RunID, Project: contract.Project, RepoID: contract.Checked.RepoID,
		Branch: contract.Checked.Branch, SubmittedHeadSHA: contract.Checked.HeadSHA,
		LaunchNonce: contract.LaunchNonce, ValidationGeneration: contract.ValidationGeneration,
		TrustedSHA: contract.Checked.TrustedSHA, Primary: contract.Checked.EffectivePrimary,
		PrimaryModel: selection.Policy.Primary.Model, GlobalConfigSHA256: contract.ConfigSHA256,
	}
}

func preparePipelineLaunchContract(ctx context.Context, reader pipeline.Reader, path, runID string, selection pipeline.Selection) (pipelineLaunchContract, pipeline.NativeLaunchState, error) {
	contract, err := loadPipelineLaunchContract(path)
	if err != nil {
		return pipelineLaunchContract{}, pipeline.NativeLaunchState{}, err
	}
	if contract.Status == pipelineLaunchContractPending && contract.RunID == "" {
		bound := contract
		bound.RunID = runID
		if _, err := reader.VerifyNativeLaunch(ctx, nativeLaunchExpectation(bound, selection)); err != nil {
			return pipelineLaunchContract{}, pipeline.NativeLaunchState{}, err
		}
		bound, err = bindPipelineLaunchContract(path, contract, runID, pipelineLaunchContractPending)
		if err != nil {
			return pipelineLaunchContract{}, pipeline.NativeLaunchState{}, err
		}
		contract = bound
	}
	if contract.RunID != runID {
		return pipelineLaunchContract{}, pipeline.NativeLaunchState{}, errors.New("pipeline: native run does not match the CFO launch contract")
	}
	switch contract.Status {
	case pipelineLaunchContractVerified:
		launchState, err := reader.VerifyNativeLaunch(ctx, nativeLaunchExpectation(contract, selection))
		return contract, launchState, err
	case pipelineLaunchContractPending, pipelineLaunchContractAuthorized:
		return verifyPipelineLaunchContract(ctx, reader, path, contract, selection)
	default:
		return pipelineLaunchContract{}, pipeline.NativeLaunchState{}, errors.New("pipeline: native run lacks an active CFO launch contract")
	}
}

func verifyPipelineLaunchContract(ctx context.Context, reader pipeline.Reader, path string, contract pipelineLaunchContract, selection pipeline.Selection) (pipelineLaunchContract, pipeline.NativeLaunchState, error) {
	launchState, err := reader.VerifyNativeLaunch(ctx, nativeLaunchExpectation(contract, selection))
	if err != nil {
		return pipelineLaunchContract{}, pipeline.NativeLaunchState{}, err
	}
	if launchState.InvocationCount == 0 {
		if contract.Status != pipelineLaunchContractPending && contract.Status != pipelineLaunchContractAuthorized && contract.Status != pipelineLaunchContractVerified {
			return pipelineLaunchContract{}, pipeline.NativeLaunchState{}, errors.New("pipeline: native run lacks an active CFO launch contract")
		}
		return contract, launchState, nil
	}
	if !launchState.ReviewProvenance {
		switch contract.Status {
		case pipelineLaunchContractAuthorized:
			return contract, launchState, nil
		case pipelineLaunchContractPending:
			authorized := contract
			authorized.Status = pipelineLaunchContractAuthorized
			if err := transitionPipelineLaunchContract(path, contract, authorized); err != nil {
				return pipelineLaunchContract{}, pipeline.NativeLaunchState{}, err
			}
			return authorized, launchState, nil
		default:
			return pipelineLaunchContract{}, pipeline.NativeLaunchState{}, errors.New("pipeline: native run lacks an active CFO launch contract")
		}
	}
	if contract.Status == pipelineLaunchContractVerified {
		return contract, launchState, nil
	}
	if contract.Status != pipelineLaunchContractPending && contract.Status != pipelineLaunchContractAuthorized {
		return pipelineLaunchContract{}, pipeline.NativeLaunchState{}, errors.New("pipeline: native run lacks an active CFO launch contract")
	}
	verified := contract
	verified.Status = pipelineLaunchContractVerified
	if err := transitionPipelineLaunchContract(path, contract, verified); err != nil {
		return pipelineLaunchContract{}, pipeline.NativeLaunchState{}, err
	}
	return verified, launchState, nil
}

const (
	pipelineLaunchContractName       = "pipeline-launch.json"
	pipelineLaunchClaimName          = "pipeline-launch-claim.json"
	pipelineLaunchContractPending    = "pending"
	pipelineLaunchContractAuthorized = "authorized"
	pipelineLaunchContractVerified   = "verified"
	pipelineLaunchContractRevoked    = "revoked"
	pipelineNativeGateBindingPending = "pending"
	pipelineNativeGateBindingActive  = "active"
	pipelineNativeGateBindingRevoked = "revoked"
)

type pipelineLaunchEvidence struct {
	Start        pipeline.StartEvidence
	ConfigSHA256 string
}

type pipelineLaunchContract struct {
	Version              int                    `json:"version"`
	Status               string                 `json:"status"`
	RunID                string                 `json:"run_id,omitempty"`
	TaskID               string                 `json:"task_id"`
	PolicyHash           string                 `json:"policy_hash"`
	Project              string                 `json:"project"`
	Checked              pipeline.StartEvidence `json:"checked"`
	ConfigSHA256         string                 `json:"config_sha256"`
	LaunchNonce          string                 `json:"launch_nonce"`
	ValidationGeneration string                 `json:"validation_generation"`
	SQLitePath           string                 `json:"sqlite_path"`
	CFOExecutablePath    string                 `json:"cfo_executable_path"`
	CFOExecutableSHA256  string                 `json:"cfo_executable_sha256"`
}

type pipelineNativeGateBinding struct {
	Version              int    `json:"version"`
	Status               string `json:"status"`
	TaskID               string `json:"task_id"`
	RepoID               string `json:"repo_id"`
	Project              string `json:"project"`
	TaskWorktree         string `json:"task_worktree"`
	RunID                string `json:"run_id,omitempty"`
	NativeWorktree       string `json:"native_worktree,omitempty"`
	SubmittedHeadSHA     string `json:"submitted_head_sha"`
	LaunchNonce          string `json:"launch_nonce"`
	ValidationGeneration string `json:"validation_generation"`
	CFOExecutablePath    string `json:"cfo_executable_path"`
	CFOExecutableSHA256  string `json:"cfo_executable_sha256"`
}

type pipelineLaunchClaim struct {
	Version              int    `json:"version"`
	TaskID               string `json:"task_id"`
	RepoID               string `json:"repo_id"`
	RunID                string `json:"run_id,omitempty"`
	LaunchNonce          string `json:"launch_nonce"`
	ValidationGeneration string `json:"validation_generation"`
}

func pipelineLaunchClaimForContract(contract pipelineLaunchContract) pipelineLaunchClaim {
	return pipelineLaunchClaim{Version: 1, TaskID: contract.TaskID, RepoID: contract.Checked.RepoID, RunID: contract.RunID, LaunchNonce: contract.LaunchNonce, ValidationGeneration: contract.ValidationGeneration}
}

func savePipelineLaunchClaim(path string, claim pipelineLaunchClaim) error {
	if err := validatePipelineLaunchClaim(claim); err != nil {
		return err
	}
	data, err := json.MarshalIndent(claim, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, append(data, '\n'))
}

func loadPipelineLaunchClaim(path string) (pipelineLaunchClaim, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return pipelineLaunchClaim{}, err
	}
	if len(data) > 1<<20 {
		return pipelineLaunchClaim{}, errors.New("pipeline: managed launch claim is too large")
	}
	var claim pipelineLaunchClaim
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&claim); err != nil {
		return pipelineLaunchClaim{}, errors.New("pipeline: invalid managed launch claim")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return pipelineLaunchClaim{}, errors.New("pipeline: trailing managed launch claim data")
	}
	if err := validatePipelineLaunchClaim(claim); err != nil {
		return pipelineLaunchClaim{}, err
	}
	return claim, nil
}

func validatePipelineLaunchClaim(claim pipelineLaunchClaim) error {
	if claim.Version != 1 || state.ValidTaskID(claim.TaskID) != nil || strings.TrimSpace(claim.RepoID) == "" || !validHexBytes(claim.LaunchNonce, 16) || !strings.HasPrefix(claim.ValidationGeneration, cfoValidationGenerationPrefix) || !validHexBytes(strings.TrimPrefix(claim.ValidationGeneration, cfoValidationGenerationPrefix), 16) {
		return errors.New("pipeline: invalid managed launch claim")
	}
	return nil
}

func bindPipelineLaunchContract(path string, from pipelineLaunchContract, runID, status string) (pipelineLaunchContract, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" || status != pipelineLaunchContractPending && status != pipelineLaunchContractAuthorized {
		return pipelineLaunchContract{}, errors.New("pipeline: invalid managed launch binding")
	}
	claimPath := filepath.Join(filepath.Dir(path), pipelineLaunchClaimName)
	claim, err := loadPipelineLaunchClaim(claimPath)
	if err != nil {
		return pipelineLaunchContract{}, err
	}
	expectedClaim := pipelineLaunchClaimForContract(from)
	if claim.Version != expectedClaim.Version || claim.TaskID != expectedClaim.TaskID || claim.RepoID != expectedClaim.RepoID || claim.LaunchNonce != expectedClaim.LaunchNonce || claim.ValidationGeneration != expectedClaim.ValidationGeneration || claim.RunID != "" && claim.RunID != runID {
		return pipelineLaunchContract{}, errors.New("pipeline: managed launch claim does not match its contract")
	}
	if claim.RunID == "" {
		claim.RunID = runID
		if err := savePipelineLaunchClaim(claimPath, claim); err != nil {
			return pipelineLaunchContract{}, err
		}
	}
	to := from
	to.RunID = runID
	to.Status = status
	if err := transitionPipelineLaunchContract(path, from, to); err != nil {
		return pipelineLaunchContract{}, err
	}
	return to, nil
}

func samePipelineLaunchContractIdentity(left, right pipelineLaunchContract) bool {
	left.Status, right.Status = "", ""
	left.RunID, right.RunID = "", ""
	return left == right
}

func pipelineNativeGateBindingPath(stateDir, repoID string) string {
	digest := sha256.Sum256([]byte(repoID))
	return filepath.Join(stateDir, "pipeline-native-gate-"+fmt.Sprintf("%x", digest)+".json")
}

func pipelineNativeGateBindingForContract(contract pipelineLaunchContract, taskWorktree string) pipelineNativeGateBinding {
	return pipelineNativeGateBinding{
		Version: 1, Status: pipelineNativeGateBindingPending, TaskID: contract.TaskID,
		RepoID: contract.Checked.RepoID, Project: contract.Project, TaskWorktree: taskWorktree,
		SubmittedHeadSHA: contract.Checked.HeadSHA, LaunchNonce: contract.LaunchNonce,
		ValidationGeneration: contract.ValidationGeneration, CFOExecutablePath: contract.CFOExecutablePath,
		CFOExecutableSHA256: contract.CFOExecutableSHA256,
	}
}

func createPipelineNativeGateBinding(path string, binding pipelineNativeGateBinding) error {
	current, err := loadPipelineNativeGateBinding(path)
	if err == nil && current.Status != pipelineNativeGateBindingRevoked {
		return errors.New("pipeline: active managed native gate binding already exists for repository")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return savePipelineNativeGateBinding(path, binding)
}

func ensurePipelineNativeGateBinding(path string, expected pipelineNativeGateBinding, runID, nativeWorktree string) error {
	current, err := loadPipelineNativeGateBinding(path)
	if errors.Is(err, os.ErrNotExist) {
		current = expected
		current.Status = pipelineNativeGateBindingActive
		current.RunID = runID
		current.NativeWorktree = nativeWorktree
		return savePipelineNativeGateBinding(path, current)
	}
	if err != nil {
		return err
	}
	_, err = activatePipelineNativeGateBinding(path, expected, runID, nativeWorktree)
	return err
}

func activatePipelineNativeGateBinding(path string, expected pipelineNativeGateBinding, runID, nativeWorktree string) (pipelineNativeGateBinding, error) {
	current, err := loadPipelineNativeGateBinding(path)
	if err != nil {
		return pipelineNativeGateBinding{}, err
	}
	if !samePipelineNativeGateBindingIdentity(current, expected) {
		return pipelineNativeGateBinding{}, errors.New("pipeline: managed native gate binding identity changed")
	}
	if current.Status == pipelineNativeGateBindingRevoked {
		return pipelineNativeGateBinding{}, errors.New("pipeline: managed native gate binding is revoked")
	}
	if current.Status == pipelineNativeGateBindingActive {
		if current.RunID != runID || !samePipelineNativeGatePath(current.NativeWorktree, nativeWorktree) {
			return pipelineNativeGateBinding{}, errors.New("pipeline: managed native gate binding belongs to another run or worktree")
		}
		return current, nil
	}
	to := current
	to.Status = pipelineNativeGateBindingActive
	to.RunID = runID
	to.NativeWorktree = nativeWorktree
	if err := transitionPipelineNativeGateBinding(path, current, to); err != nil {
		return pipelineNativeGateBinding{}, err
	}
	return to, nil
}

func revokePipelineNativeGateBinding(path string, expected pipelineNativeGateBinding) error {
	current, err := loadPipelineNativeGateBinding(path)
	if err != nil {
		return err
	}
	if !samePipelineNativeGateBindingIdentity(current, expected) {
		return errors.New("pipeline: managed native gate binding identity changed before revocation")
	}
	if current.Status == pipelineNativeGateBindingRevoked {
		return nil
	}
	revoked := current
	revoked.Status = pipelineNativeGateBindingRevoked
	return transitionPipelineNativeGateBinding(path, current, revoked)
}

func retirePipelineNativeGateBinding(path string, expected pipelineNativeGateBinding) error {
	if err := revokePipelineNativeGateBinding(path, expected); err != nil {
		return err
	}
	return os.Remove(path)
}

func transitionPipelineNativeGateBinding(path string, from, to pipelineNativeGateBinding) error {
	current, err := loadPipelineNativeGateBinding(path)
	if err != nil {
		return err
	}
	if current != from {
		return errors.New("pipeline: managed native gate binding changed during lifecycle transition")
	}
	return savePipelineNativeGateBinding(path, to)
}

func savePipelineNativeGateBinding(path string, binding pipelineNativeGateBinding) error {
	if err := validatePipelineNativeGateBinding(binding); err != nil {
		return err
	}
	data, err := json.MarshalIndent(binding, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, append(data, '\n'))
}

func loadPipelineNativeGateBinding(path string) (pipelineNativeGateBinding, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return pipelineNativeGateBinding{}, err
	}
	if len(data) > 1<<20 {
		return pipelineNativeGateBinding{}, errors.New("pipeline: managed native gate binding is too large")
	}
	var binding pipelineNativeGateBinding
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&binding); err != nil {
		return pipelineNativeGateBinding{}, errors.New("pipeline: invalid managed native gate binding")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return pipelineNativeGateBinding{}, errors.New("pipeline: trailing managed native gate binding data")
	}
	if err := validatePipelineNativeGateBinding(binding); err != nil {
		return pipelineNativeGateBinding{}, err
	}
	return binding, nil
}

func validatePipelineNativeGateBinding(binding pipelineNativeGateBinding) error {
	if binding.Version != 1 || state.ValidTaskID(binding.TaskID) != nil || strings.TrimSpace(binding.RepoID) == "" || strings.TrimSpace(binding.Project) == "" || strings.TrimSpace(binding.TaskWorktree) == "" || !validHexBytes(binding.SubmittedHeadSHA, 20) || !validHexBytes(binding.LaunchNonce, 16) || !strings.HasPrefix(binding.ValidationGeneration, cfoValidationGenerationPrefix) || !validHexBytes(strings.TrimPrefix(binding.ValidationGeneration, cfoValidationGenerationPrefix), 16) || !validHexBytes(binding.CFOExecutableSHA256, 32) {
		return errors.New("pipeline: invalid managed native gate binding")
	}
	if binding.Status != pipelineNativeGateBindingPending && binding.Status != pipelineNativeGateBindingActive && binding.Status != pipelineNativeGateBindingRevoked {
		return errors.New("pipeline: invalid managed native gate binding lifecycle")
	}
	if (binding.RunID == "") != (binding.NativeWorktree == "") || binding.Status == pipelineNativeGateBindingPending && binding.RunID != "" || binding.Status == pipelineNativeGateBindingActive && binding.RunID == "" {
		return errors.New("pipeline: invalid managed native gate binding lifecycle")
	}
	for _, path := range []string{binding.Project, binding.TaskWorktree, binding.CFOExecutablePath} {
		absolute, err := filepath.Abs(path)
		if err != nil || !fsx.SamePath(absolute, path) {
			return errors.New("pipeline: managed native gate binding paths must be absolute")
		}
	}
	if binding.NativeWorktree != "" {
		absolute, err := filepath.Abs(binding.NativeWorktree)
		if err != nil || !samePipelineNativeGatePath(absolute, binding.NativeWorktree) || filepath.Base(filepath.Clean(binding.NativeWorktree)) != binding.RunID {
			return errors.New("pipeline: managed native gate binding worktree does not match its run")
		}
	}
	return nil
}

func samePipelineNativeGateBindingIdentity(left, right pipelineNativeGateBinding) bool {
	left.Status, right.Status = "", ""
	left.RunID, right.RunID = "", ""
	left.NativeWorktree, right.NativeWorktree = "", ""
	return left == right
}

func samePipelineNativeGatePath(left, right string) bool {
	left, leftErr := filepath.Abs(left)
	right, rightErr := filepath.Abs(right)
	return leftErr == nil && rightErr == nil && strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func pipelineNativeGateBindingMatchesContract(binding pipelineNativeGateBinding, contract pipelineLaunchContract) bool {
	return binding.TaskID == contract.TaskID && binding.RepoID == contract.Checked.RepoID && fsx.SamePath(binding.Project, contract.Project) && binding.SubmittedHeadSHA == contract.Checked.HeadSHA && binding.LaunchNonce == contract.LaunchNonce && binding.ValidationGeneration == contract.ValidationGeneration && fsx.SamePath(binding.CFOExecutablePath, contract.CFOExecutablePath) && binding.CFOExecutableSHA256 == contract.CFOExecutableSHA256 && (contract.RunID == "" || binding.RunID == contract.RunID)
}

func capturePipelineLaunch(ctx context.Context, h home.Home, root string, reader pipeline.Reader, expected state.TaskMeta, branch string, selection pipeline.Selection) (pipelineLaunchEvidence, error) {
	meta, err := state.ReadTaskMeta(h.State, expected.ID)
	if err != nil {
		return pipelineLaunchEvidence{}, err
	}
	if meta.Mode != "no-mistakes" || meta.Project != expected.Project || meta.Worktree != expected.Worktree || meta.TaskTmp != expected.TaskTmp || meta.PipelineHash != selection.Hash || meta.PipelineClass != selection.Class {
		return pipelineLaunchEvidence{}, errors.New("pipeline: task metadata changed before native invocation")
	}
	latest, err := pipeline.LoadSelection(filepath.Join(meta.TaskTmp, "pipeline.json"))
	if err != nil {
		return pipelineLaunchEvidence{}, err
	}
	if latest != selection {
		return pipelineLaunchEvidence{}, errors.New("pipeline: frozen task policy changed before native invocation")
	}
	if err := worktree.Validate(ctx, worktree.RunnerGit{Commands: reader.Commands}, meta.Project, meta.Worktree); err != nil {
		return pipelineLaunchEvidence{}, err
	}
	configPath := filepath.Join(root, "config.yaml")
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return pipelineLaunchEvidence{}, err
	}
	drift, err := (pipeline.Config{Path: configPath, Policy: selection.Policy}).Drift()
	if err != nil {
		return pipelineLaunchEvidence{}, err
	}
	if len(drift) != 0 {
		return pipelineLaunchEvidence{}, fmt.Errorf("pipeline: shared config drift (%s); request idle config-apply, never change a running daemon", strings.Join(drift, ", "))
	}
	start, err := reader.CheckStartEvidence(ctx, meta.Project, meta.Worktree, branch, selection.Policy)
	if err != nil {
		return pipelineLaunchEvidence{}, err
	}
	return pipelineLaunchEvidence{Start: start, ConfigSHA256: fmt.Sprintf("%x", sha256.Sum256(configData))}, nil
}

func randomLaunchValue() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("pipeline: create launch identity: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func savePipelineLaunchContract(path string, contract pipelineLaunchContract) error {
	if err := validatePipelineLaunchContract(contract); err != nil {
		return err
	}
	data, err := json.MarshalIndent(contract, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, append(data, '\n'))
}

func transitionPipelineLaunchContract(path string, from, to pipelineLaunchContract) error {
	current, err := loadPipelineLaunchContract(path)
	if err != nil {
		return err
	}
	if current != from {
		return errors.New("pipeline: managed launch contract changed during lifecycle transition")
	}
	return savePipelineLaunchContract(path, to)
}

type nativeLaunchReceipt struct {
	RunID                string
	Disposition          string
	LaunchNonce          string
	ValidationGeneration string
	Branch               string
	HeadSHA              string
	SubmittedHeadSHA     string
	IntentDigest         string
}

func parseLaunchReceipt(output []byte) (nativeLaunchReceipt, error) {
	values := map[string]string{}
	inReceipt := false
	for _, line := range strings.Split(strings.ReplaceAll(string(output), "\r\n", "\n"), "\n") {
		if line == "launch_receipt:" {
			if inReceipt {
				return nativeLaunchReceipt{}, errors.New("pipeline: duplicate native launch receipt")
			}
			inReceipt = true
			continue
		}
		if !inReceipt {
			continue
		}
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "  ") {
			break
		}
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || key == "" || values[key] != "" {
			return nativeLaunchReceipt{}, errors.New("pipeline: malformed native launch receipt")
		}
		value = strings.TrimSpace(value)
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		}
		values[key] = value
	}
	receipt := nativeLaunchReceipt{
		RunID: values["run_id"], Disposition: values["disposition"], LaunchNonce: values["launch_nonce"],
		ValidationGeneration: values["validation_generation"], Branch: values["branch"], HeadSHA: values["head_sha"],
		SubmittedHeadSHA: values["submitted_head_sha"], IntentDigest: values["intent_digest"],
	}
	if !inReceipt || len(values) != 8 || receipt.RunID == "" || receipt.Disposition == "" || receipt.LaunchNonce == "" || receipt.ValidationGeneration == "" || receipt.Branch == "" || receipt.HeadSHA == "" || receipt.SubmittedHeadSHA == "" || receipt.IntentDigest == "" {
		return nativeLaunchReceipt{}, errors.New("pipeline: complete native launch receipt required")
	}
	return receipt, nil
}

func (r nativeLaunchReceipt) verify(checked pipeline.StartEvidence, nonce, generation, intent string) error {
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(intent)))
	if r.Disposition != "created" && r.Disposition != "reused" || r.LaunchNonce != nonce || r.ValidationGeneration != generation || r.Branch != checked.Branch || r.HeadSHA != checked.HeadSHA || r.SubmittedHeadSHA != checked.HeadSHA || r.IntentDigest != digest {
		return errors.New("pipeline: native launch receipt does not match the checked branch, head, generation, and intent")
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
