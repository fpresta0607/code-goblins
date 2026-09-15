package main

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	managedOpenAIBaseURL          = "https://chatgpt.com/backend-api/codex"
	managedChatGPTBaseURL         = "https://chatgpt.com/backend-api/"
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
	managed := false
	if inspect {
		managed, err = authorizeNativeGateAgent(context.Background(), h, reader, worktree)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	cmdArgs, cmdEnv := nativeGateDelegation(args, os.Environ(), managed)
	if managed {
		if err := requireManagedNativeGateChatGPT(context.Background(), execx.OSRunner{}, cmdEnv); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	cmd := exec.Command("codex", cmdArgs...)
	cmd.Env = cmdEnv
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
	billingKeys := make(map[string]struct{}, len(harness.HarnessBillingKeys)+2)
	for _, key := range harness.HarnessBillingKeys {
		billingKeys[strings.ToUpper(key)] = struct{}{}
	}
	billingKeys["OPENROUTER_API_KEY"] = struct{}{}
	billingKeys["OPENAI_BASE_URL"] = struct{}{}
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
	return append(append([]string(nil), args...),
		"-c", `model_provider="openai"`,
		"-c", `forced_login_method="chatgpt"`,
		"-c", `openai_base_url="`+managedOpenAIBaseURL+`"`,
		"-c", `chatgpt_base_url="`+managedChatGPTBaseURL+`"`)
}

func nativeGateDelegation(args, env []string, managed bool) ([]string, []string) {
	if !managed {
		return args, env
	}
	return subscriptionOnlyNativeGateArguments(args), subscriptionOnlyNativeGateEnvironment(env)
}

func requireManagedNativeGateChatGPT(ctx context.Context, commands execx.Runner, env []string) error {
	result, err := commands.Run(ctx, execx.Request{Env: env, Name: "codex", Args: subscriptionOnlyNativeGateArguments([]string{"login", "status"})})
	status := strings.TrimSpace(string(result.Stdout) + string(result.Stderr))
	if err != nil || result.ExitCode != 0 || status != "Logged in using ChatGPT" {
		return errors.New("pipeline: managed native gate requires ChatGPT subscription authentication")
	}
	return nil
}

func currentCFOExecutableEvidence() (string, string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", "", fmt.Errorf("pipeline: resolve current CFO executable: %w", err)
	}
	executable, err = fsx.Canonical(executable)
	if err != nil {
		return "", "", fmt.Errorf("pipeline: canonicalize current CFO executable: %w", err)
	}
	digest, err := fileSHA256(executable)
	if err != nil {
		return "", "", fmt.Errorf("pipeline: hash current CFO executable: %w", err)
	}
	return executable, digest, nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func nativeGateReader(h home.Home, root, worktree string) (pipeline.Reader, bool, error) {
	repoID, runID, ok := nativeGateWorktreeIdentity(root, worktree)
	if !ok {
		return pipeline.Reader{}, false, nil
	}
	contract, ok, err := findScopedPipelineLaunchContract(h.State, repoID, runID)
	if err != nil {
		return pipeline.Reader{}, false, err
	}
	if !ok {
		sqlitePath, err := exec.LookPath("sqlite3")
		if err != nil {
			return pipeline.Reader{}, false, nil
		}
		sqlitePath, err = filepath.Abs(sqlitePath)
		if err != nil {
			return pipeline.Reader{}, false, errors.New("pipeline: invalid native gate sqlite path")
		}
		reader := pipeline.Reader{Root: root, Commands: execx.OSRunner{}, SQLitePath: sqlitePath}
		run, err := reader.NativeRunAtWorktree(context.Background(), worktree)
		if errors.Is(err, pipeline.ErrNoNativeRunAtWorktree) {
			return pipeline.Reader{}, false, nil
		}
		if err != nil {
			return pipeline.Reader{}, false, err
		}
		if strings.HasPrefix(run.ValidationGeneration, cfoValidationGenerationPrefix) {
			return pipeline.Reader{}, false, errors.New("pipeline: managed native gate evidence is missing or invalid")
		}
		return pipeline.Reader{}, false, nil
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
		dir := filepath.Join(stateDir, "tasktmp", entry.Name())
		claimPath := filepath.Join(dir, pipelineLaunchClaimName)
		contractPath := filepath.Join(dir, pipelineLaunchContractName)
		claimData, claimErr := os.ReadFile(claimPath)
		contractData, contractErr := os.ReadFile(contractPath)
		if claimErr != nil && !errors.Is(claimErr, os.ErrNotExist) {
			return pipelineLaunchContract{}, false, claimErr
		}
		if contractErr != nil && !errors.Is(contractErr, os.ErrNotExist) {
			return pipelineLaunchContract{}, false, contractErr
		}
		var claimHint struct {
			RepoID string `json:"repo_id"`
			RunID  string `json:"run_id"`
		}
		var contractHint struct {
			RunID   string `json:"run_id"`
			Checked struct {
				RepoID string `json:"repo_id"`
			} `json:"checked"`
		}
		claimScoped := len(claimData) <= 1<<20 && json.Unmarshal(claimData, &claimHint) == nil && claimHint.RepoID == repoID && (claimHint.RunID == "" || claimHint.RunID == runID)
		contractScoped := len(contractData) <= 1<<20 && json.Unmarshal(contractData, &contractHint) == nil && contractHint.Checked.RepoID == repoID && (contractHint.RunID == "" || contractHint.RunID == runID)
		if !claimScoped && !contractScoped {
			continue
		}
		if errors.Is(claimErr, os.ErrNotExist) {
			return pipelineLaunchContract{}, false, errors.New("pipeline: managed native gate claim is missing")
		}
		if errors.Is(contractErr, os.ErrNotExist) {
			return pipelineLaunchContract{}, false, errors.New("pipeline: managed native gate contract is missing")
		}
		claim, err := loadPipelineLaunchClaim(claimPath)
		if err != nil {
			return pipelineLaunchContract{}, false, err
		}
		if claim.TaskID != entry.Name() {
			return pipelineLaunchContract{}, false, errors.New("pipeline: managed launch claim path does not match its task identity")
		}
		contract, err := loadPipelineLaunchContract(contractPath)
		if err != nil {
			return pipelineLaunchContract{}, false, err
		}
		expectedClaim := pipelineLaunchClaimForContract(contract)
		if claim.Version != expectedClaim.Version || claim.TaskID != expectedClaim.TaskID || claim.RepoID != expectedClaim.RepoID || claim.LaunchNonce != expectedClaim.LaunchNonce || claim.ValidationGeneration != expectedClaim.ValidationGeneration || claim.RunID != expectedClaim.RunID {
			return pipelineLaunchContract{}, false, errors.New("pipeline: managed launch claim does not match its contract")
		}
		if contract.RunID != "" && contract.RunID != runID {
			return pipelineLaunchContract{}, false, errors.New("pipeline: managed launch contract does not match its native worktree")
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

func authorizeNativeGateAgent(ctx context.Context, h home.Home, reader pipeline.Reader, worktree string) (bool, error) {
	run, err := reader.NativeRunAtWorktree(ctx, worktree)
	if errors.Is(err, pipeline.ErrNoNativeRunAtWorktree) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !strings.HasPrefix(run.ValidationGeneration, cfoValidationGenerationPrefix) {
		return false, nil
	}
	if run.LaunchNonce == "" {
		return false, errors.New("pipeline: native run has an incomplete managed launch identity")
	}
	contract, err := findPipelineLaunchContract(h.State, run.LaunchNonce, run.ValidationGeneration)
	if err != nil {
		return false, err
	}
	if !fsx.SamePath(reader.SQLitePath, contract.SQLitePath) {
		return false, errors.New("pipeline: managed launch sqlite path changed before agent authorization")
	}
	executable, digest, err := currentCFOExecutableEvidence()
	if err != nil {
		return false, err
	}
	if !fsx.SamePath(executable, contract.CFOExecutablePath) || digest != contract.CFOExecutableSHA256 {
		return false, errors.New("pipeline: managed native gate CFO executable identity changed")
	}
	switch contract.Status {
	case pipelineLaunchContractPending:
		if contract.RunID != "" && contract.RunID != run.RunID {
			return false, errors.New("pipeline: receipt-bound pending launch contract does not match the active run")
		}
		if run.InvocationCount != 0 {
			return false, errors.New("pipeline: pending managed launch contract cannot authorize a later agent")
		}
	case pipelineLaunchContractAuthorized, pipelineLaunchContractVerified:
		if contract.RunID != run.RunID {
			return false, errors.New("pipeline: bound managed launch contract does not match the active run")
		}
	default:
		return false, errors.New("pipeline: managed launch contract is revoked")
	}
	err = reader.VerifyNativeAgent(ctx, worktree, pipeline.NativeAgentExpectation{
		RunID: run.RunID, Project: contract.Project, RepoID: contract.Checked.RepoID,
		Branch: contract.Checked.Branch, SubmittedHeadSHA: contract.Checked.HeadSHA,
		LaunchNonce: contract.LaunchNonce, ValidationGeneration: contract.ValidationGeneration,
		DefaultBranch: contract.Checked.DefaultBranch, TrustedSHA: contract.Checked.TrustedSHA,
		TaskConfigSHA256: contract.Checked.TaskConfigSHA256, TrustedConfigSHA256: contract.Checked.TrustedConfigSHA256,
		GlobalConfigSHA256: contract.ConfigSHA256, EffectivePrimary: contract.Checked.EffectivePrimary,
	})
	if err != nil {
		return false, err
	}
	if contract.Status == pipelineLaunchContractPending {
		contractPath := filepath.Join(h.State, "tasktmp", contract.TaskID, pipelineLaunchContractName)
		if _, err := bindPipelineLaunchContract(contractPath, contract, run.RunID, pipelineLaunchContractAuthorized); err != nil {
			return false, err
		}
	}
	return true, nil
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
	if contract.Version != 1 || state.ValidTaskID(contract.TaskID) != nil || contract.PolicyHash == "" || contract.Project == "" || contract.SQLitePath == "" || contract.CFOExecutablePath == "" || checked.RepoID == "" || checked.Branch == "" || checked.HeadSHA == "" || checked.DefaultBranch == "" || checked.TrustedSHA == "" || checked.EffectivePrimary != "codex" {
		return errors.New("pipeline: incomplete managed launch contract")
	}
	for _, value := range []string{contract.ConfigSHA256, contract.CFOExecutableSHA256, checked.TaskConfigSHA256, checked.TrustedConfigSHA256} {
		if !validHexBytes(value, 32) {
			return errors.New("pipeline: invalid managed launch evidence digest")
		}
	}
	if !validHexBytes(contract.LaunchNonce, 16) || !strings.HasPrefix(contract.ValidationGeneration, cfoValidationGenerationPrefix) || !validHexBytes(strings.TrimPrefix(contract.ValidationGeneration, cfoValidationGenerationPrefix), 16) {
		return errors.New("pipeline: invalid managed launch identity")
	}
	switch contract.Status {
	case pipelineLaunchContractPending, pipelineLaunchContractRevoked:
	case pipelineLaunchContractAuthorized, pipelineLaunchContractVerified:
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
	absExecutable, err := filepath.Abs(contract.CFOExecutablePath)
	if err != nil || !fsx.SamePath(absExecutable, contract.CFOExecutablePath) {
		return errors.New("pipeline: managed launch CFO executable path must be absolute")
	}
	return nil
}

func validHexBytes(value string, size int) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == size
}
