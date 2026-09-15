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
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const (
	nativeGateMarker              = "--cfo-native-gate"
	cfoValidationGenerationPrefix = "cfo-v1-"
	nativeGateRuntimeName         = "native-gate-runtime.json"
)

type nativeGateRuntime struct {
	Version    int    `json:"version"`
	SQLitePath string `json:"sqlite_path"`
}

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
	reader, inspect, err := nativeGateReader(h, root)
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
	cmd := exec.Command("codex", args...)
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

func nativeGateReader(h home.Home, root string) (pipeline.Reader, bool, error) {
	runtime, err := loadNativeGateRuntime(filepath.Join(h.State, nativeGateRuntimeName))
	if errors.Is(err, os.ErrNotExist) {
		sqlitePath, lookupErr := exec.LookPath("sqlite3")
		if lookupErr != nil {
			return pipeline.Reader{}, false, nil
		}
		runtime = nativeGateRuntime{Version: 1, SQLitePath: sqlitePath}
	} else if err != nil {
		return pipeline.Reader{}, false, err
	}
	sqlitePath, err := filepath.Abs(runtime.SQLitePath)
	if err != nil || runtime.Version != 1 || !fsx.SamePath(sqlitePath, runtime.SQLitePath) {
		return pipeline.Reader{}, false, errors.New("pipeline: invalid managed native gate runtime")
	}
	return pipeline.Reader{Root: root, Commands: execx.OSRunner{}, SQLitePath: sqlitePath}, true, nil
}

func saveNativeGateRuntime(stateDir string, runtime nativeGateRuntime) error {
	data, err := json.Marshal(runtime)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(stateDir, nativeGateRuntimeName), append(data, '\n'))
}

func loadNativeGateRuntime(path string) (nativeGateRuntime, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nativeGateRuntime{}, err
	}
	var runtime nativeGateRuntime
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&runtime); err != nil {
		return nativeGateRuntime{}, errors.New("pipeline: invalid managed native gate runtime")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nativeGateRuntime{}, errors.New("pipeline: trailing managed native gate runtime data")
	}
	return runtime, nil
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
		contract, err := loadPipelineLaunchContract(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return pipelineLaunchContract{}, err
		}
		if contract.TaskID != entry.Name() {
			return pipelineLaunchContract{}, errors.New("pipeline: managed launch contract path does not match its task identity")
		}
		if contract.LaunchNonce != nonce || contract.ValidationGeneration != generation {
			continue
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
		if contract.RunID != "" {
			return errors.New("pipeline: invalid managed launch contract lifecycle")
		}
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
