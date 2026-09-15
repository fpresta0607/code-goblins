package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const (
	nativeGateMarker              = "--cfo-native-gate"
	cfoValidationGenerationPrefix = "cfo-v1-"
)

func stripNativeGateMarker(args []string) ([]string, bool) {
	marker := -1
	if len(args) >= 2 && args[0] == "exec" && args[1] == nativeGateMarker {
		marker = 1
	} else if len(args) >= 3 && args[0] == "exec" && args[1] == "resume" && args[2] == nativeGateMarker {
		marker = 2
	}
	if marker < 0 {
		return args, false
	}
	clean := append([]string(nil), args[:marker]...)
	clean = append(clean, args[marker+1:]...)
	return clean, true
}

func runNativeGateAgent(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	h, err := home.Resolve()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	root, err := pipeline.DefaultRoot()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	worktree, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	reader, inspect, err := nativeGateReader(h, root, worktree)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if inspect {
		if err := authorizeNativeGateAgent(context.Background(), h, reader, worktree); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	cmd := exec.Command("codex", subscriptionOnlyNativeGateArguments(args)...)
	cmd.Env = subscriptionOnlyNativeGateEnvironment(os.Environ())
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(stderr, "cfo: start codex: %v\n", err)
		return 1
	}
	return 0
}

func subscriptionOnlyNativeGateEnvironment(env []string) []string {
	billingKeys := make(map[string]struct{}, len(harness.HarnessBillingKeys)+1)
	for _, key := range harness.HarnessBillingKeys {
		billingKeys[strings.ToUpper(key)] = struct{}{}
	}
	billingKeys["OPENROUTER_API_KEY"] = struct{}{}
	clean := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, blocked := billingKeys[strings.ToUpper(name)]; blocked {
				continue
			}
		}
		clean = append(clean, entry)
	}
	return clean
}

func subscriptionOnlyNativeGateArguments(args []string) []string {
	return append(append([]string(nil), args...), "-c", `model_provider="openai"`)
}

func nativeGateReader(h home.Home, root, worktree string) (pipeline.Reader, bool, error) {
	repoID, runID, ok := nativeGateWorktreeIdentity(root, worktree)
	if !ok {
		return pipeline.Reader{}, false, nil
	}
	contract, ok, err := findScopedPipelineLaunchContract(h.State, repoID, runID)
	if err != nil || !ok {
		return pipeline.Reader{}, false, err
	}
	sqlitePath, err := filepath.Abs(contract.SQLitePath)
	if err != nil || !fsx.SamePath(sqlitePath, contract.SQLitePath) {
		return pipeline.Reader{}, false, errors.New("pipeline: invalid managed native gate sqlite path")
	}
	return pipeline.Reader{Root: root, Commands: execx.OSRunner{}, SQLitePath: sqlitePath}, true, nil
}

func nativeGateWorktreeIdentity(root, worktree string) (string, string, bool) {
	data, err := os.ReadFile(filepath.Join(worktree, ".git"))
	if err != nil || len(data) > 4096 {
		return "", "", false
	}
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "gitdir: ") {
		return "", "", false
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(line, "gitdir: "))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(worktree, gitDir)
	}
	rel, err := filepath.Rel(filepath.Join(root, "repos"), filepath.Clean(gitDir))
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", false
	}
	parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	if len(parts) != 3 || !strings.HasSuffix(strings.ToLower(parts[0]), ".git") || !strings.EqualFold(parts[1], "worktrees") {
		return "", "", false
	}
	repoID := strings.TrimSuffix(parts[0], filepath.Ext(parts[0]))
	runID := filepath.Base(filepath.Clean(worktree))
	if repoID == "" || runID == "" || runID == "." || runID == string(filepath.Separator) {
		return "", "", false
	}
	return repoID, runID, true
}

func findScopedPipelineLaunchContract(stateDir, repoID, runID string) (pipelineLaunchContract, bool, error) {
	entries, err := os.ReadDir(filepath.Join(stateDir, "tasktmp"))
	if errors.Is(err, os.ErrNotExist) {
		return pipelineLaunchContract{}, false, nil
	}
	if err != nil {
		return pipelineLaunchContract{}, false, fmt.Errorf("pipeline: read managed launch contracts: %w", err)
	}
	var match *pipelineLaunchContract
	for _, entry := range entries {
		if !entry.IsDir() || state.ValidTaskID(entry.Name()) != nil {
			continue
		}
		path := filepath.Join(stateDir, "tasktmp", entry.Name(), pipelineLaunchContractName)
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return pipelineLaunchContract{}, false, err
		}
		var hint struct {
			Status  string `json:"status"`
			RunID   string `json:"run_id"`
			Checked struct {
				RepoID string `json:"repo_id"`
			} `json:"checked"`
		}
		if json.Unmarshal(data, &hint) != nil || hint.Checked.RepoID != repoID {
			continue
		}
		scoped := hint.RunID == runID || hint.RunID == "" && hint.Status == pipelineLaunchContractPending
		if !scoped {
			continue
		}
		contract, err := loadPipelineLaunchContract(path)
		if err != nil {
			return pipelineLaunchContract{}, false, err
		}
		if contract.TaskID != entry.Name() {
			return pipelineLaunchContract{}, false, errors.New("pipeline: managed launch contract path does not match its task identity")
		}
		if match != nil {
			return pipelineLaunchContract{}, false, errors.New("pipeline: duplicate managed launch contracts for native worktree")
		}
		copy := contract
		match = &copy
	}
	if match == nil {
		return pipelineLaunchContract{}, false, nil
	}
	return *match, true, nil
}

func authorizeNativeGateAgent(ctx context.Context, h home.Home, reader pipeline.Reader, worktree string) error {
	run, err := reader.NativeRunAtWorktree(ctx, worktree)
	if errors.Is(err, pipeline.ErrNoNativeRunAtWorktree) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.HasPrefix(run.ValidationGeneration, cfoValidationGenerationPrefix) {
		return nil
	}
	if run.LaunchNonce == "" {
		return errors.New("pipeline: native run has an incomplete managed launch identity")
	}
	contract, err := findPipelineLaunchContract(h.State, run.LaunchNonce, run.ValidationGeneration)
	if err != nil {
		return err
	}
	if !fsx.SamePath(reader.SQLitePath, contract.SQLitePath) {
		return errors.New("pipeline: managed launch sqlite path changed before agent authorization")
	}
	switch contract.Status {
	case pipelineLaunchContractPending:
		if contract.RunID != "" && contract.RunID != run.RunID {
			return errors.New("pipeline: receipt-bound pending launch contract does not match the active run")
		}
		if run.InvocationCount != 0 {
			return errors.New("pipeline: pending managed launch contract cannot authorize a later agent")
		}
	case pipelineLaunchContractVerified:
		if contract.RunID != run.RunID {
			return errors.New("pipeline: verified managed launch contract does not match the active run")
		}
	default:
		return errors.New("pipeline: managed launch contract is revoked")
	}
	return reader.VerifyNativeAgent(ctx, worktree, pipeline.NativeAgentExpectation{
		RunID: run.RunID, Project: contract.Project, RepoID: contract.Checked.RepoID,
		Branch: contract.Checked.Branch, SubmittedHeadSHA: contract.Checked.HeadSHA,
		LaunchNonce: contract.LaunchNonce, ValidationGeneration: contract.ValidationGeneration,
		DefaultBranch: contract.Checked.DefaultBranch, TrustedSHA: contract.Checked.TrustedSHA,
		TaskConfigSHA256: contract.Checked.TaskConfigSHA256, TrustedConfigSHA256: contract.Checked.TrustedConfigSHA256,
		GlobalConfigSHA256: contract.ConfigSHA256, EffectivePrimary: contract.Checked.EffectivePrimary,
	})
}

func findPipelineLaunchContract(stateDir, nonce, generation string) (pipelineLaunchContract, error) {
	entries, err := os.ReadDir(filepath.Join(stateDir, "tasktmp"))
	if err != nil {
		return pipelineLaunchContract{}, fmt.Errorf("pipeline: read managed launch contracts: %w", err)
	}
	var match *pipelineLaunchContract
	for _, entry := range entries {
		if !entry.IsDir() || state.ValidTaskID(entry.Name()) != nil {
			continue
		}
		path := filepath.Join(stateDir, "tasktmp", entry.Name(), pipelineLaunchContractName)
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return pipelineLaunchContract{}, err
		}
		var hint struct {
			LaunchNonce          string `json:"launch_nonce"`
			ValidationGeneration string `json:"validation_generation"`
		}
		if len(data) > 1<<20 || json.Unmarshal(data, &hint) != nil || hint.LaunchNonce != nonce || hint.ValidationGeneration != generation {
			continue
		}
		contract, err := decodePipelineLaunchContract(data)
		if err != nil {
			return pipelineLaunchContract{}, err
		}
		if contract.TaskID != entry.Name() {
			return pipelineLaunchContract{}, errors.New("pipeline: managed launch contract path does not match its task identity")
		}
		if match != nil {
			return pipelineLaunchContract{}, errors.New("pipeline: duplicate managed launch contracts")
		}
		copy := contract
		match = &copy
	}
	if match == nil {
		return pipelineLaunchContract{}, errors.New("pipeline: managed native run has no matching CFO launch contract")
	}
	return *match, nil
}

func loadPipelineLaunchContract(path string) (pipelineLaunchContract, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return pipelineLaunchContract{}, err
	}
	return decodePipelineLaunchContract(data)
}

func decodePipelineLaunchContract(data []byte) (pipelineLaunchContract, error) {
	if len(data) > 1<<20 {
		return pipelineLaunchContract{}, errors.New("pipeline: managed launch contract is too large")
	}
	var contract pipelineLaunchContract
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&contract); err != nil {
		return pipelineLaunchContract{}, errors.New("pipeline: invalid managed launch contract")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return pipelineLaunchContract{}, errors.New("pipeline: trailing managed launch contract data")
	}
	if err := validatePipelineLaunchContract(contract); err != nil {
		return pipelineLaunchContract{}, err
	}
	return contract, nil
}

func validatePipelineLaunchContract(contract pipelineLaunchContract) error {
	checked := contract.Checked
	if contract.Version != 1 || state.ValidTaskID(contract.TaskID) != nil || contract.PolicyHash == "" || contract.Project == "" || contract.SQLitePath == "" || checked.RepoID == "" || checked.Branch == "" || checked.HeadSHA == "" || checked.DefaultBranch == "" || checked.TrustedSHA == "" || checked.EffectivePrimary != "codex" {
		return errors.New("pipeline: incomplete managed launch contract")
	}
	for _, value := range []string{contract.ConfigSHA256, checked.TaskConfigSHA256, checked.TrustedConfigSHA256} {
		if !validHexBytes(value, 32) {
			return errors.New("pipeline: invalid managed launch evidence digest")
		}
	}
	if !validHexBytes(contract.LaunchNonce, 16) || !strings.HasPrefix(contract.ValidationGeneration, cfoValidationGenerationPrefix) || !validHexBytes(strings.TrimPrefix(contract.ValidationGeneration, cfoValidationGenerationPrefix), 16) {
		return errors.New("pipeline: invalid managed launch identity")
	}
	switch contract.Status {
	case pipelineLaunchContractPending, pipelineLaunchContractRevoked:
	case pipelineLaunchContractVerified:
		if strings.TrimSpace(contract.RunID) == "" {
			return errors.New("pipeline: invalid managed launch contract lifecycle")
		}
	default:
		return errors.New("pipeline: invalid managed launch contract lifecycle")
	}
	absProject, err := filepath.Abs(contract.Project)
	if err != nil || !fsx.SamePath(absProject, contract.Project) {
		return errors.New("pipeline: managed launch project path must be absolute")
	}
	absSQLite, err := filepath.Abs(contract.SQLitePath)
	if err != nil || !fsx.SamePath(absSQLite, contract.SQLitePath) {
		return errors.New("pipeline: managed launch sqlite path must be absolute")
	}
	return nil
}

func validHexBytes(value string, size int) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == size
}
