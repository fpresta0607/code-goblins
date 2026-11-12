package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func pipelineAdmission(ctx context.Context, h home.Home, reader pipeline.Reader, meta state.TaskMeta, selection pipeline.Selection, worktree, branch, operation, file string, out io.Writer) error {
	environment, err := pipeline.ProjectTestEnvironment(ctx, reader.Commands, h.Data, meta.Project, meta.Worktree)
	if err != nil {
		return err
	}
	if len(environment) != 0 {
		if err := pipeline.CheckTestEnvironmentWrapper(reader.Root); err != nil {
			return err
		}
	}
	result, digest, nativeErr := reader.Admit(ctx, meta.Project, worktree, branch, operation, file, selection, pipeline.NativeEnv(reader.Root))
	fmt.Fprint(out, string(result.Stdout), string(result.Stderr))
	if digest == "" {
		return nativeErr
	}
	// Keep the actual receipt, native outcome and cumulative ledger together,
	// including refusals and identical-child observations. This is not a pass.
	record := struct {
		At              string `json:"at"`
		Operation       string `json:"operation"`
		AdmissionSHA256 string `json:"admission_sha256"`
		Stdout          string `json:"stdout"`
		Stderr          string `json:"stderr"`
		ExitCode        int    `json:"exit_code"`
		Error           string `json:"error,omitempty"`
	}{At: time.Now().UTC().Format(time.RFC3339Nano), Operation: operation, AdmissionSHA256: digest, Stdout: string(result.Stdout), Stderr: string(result.Stderr), ExitCode: result.ExitCode}
	if nativeErr != nil {
		record.Error = nativeErr.Error()
	}
	journal, err := fsx.OpenAppend(filepath.Join(meta.TaskTmp, "validation-admissions.jsonl"), 0o600)
	if err != nil {
		return errors.Join(nativeErr, fmt.Errorf("pipeline: could not retain native admission result; inspect native status before retrying: %w", err))
	}
	writeErr := json.NewEncoder(journal).Encode(record)
	if err := errors.Join(writeErr, journal.Close()); err != nil {
		return errors.Join(nativeErr, fmt.Errorf("pipeline: could not retain native admission result; inspect native status before retrying: %w", err))
	}
	if err := state.AppendStatus(h.State, meta.ID, fmt.Sprintf("pipeline-admission: operation=%s assertion=%s native_exit=%d result=validation-admissions.jsonl", operation, digest, result.ExitCode)); err != nil {
		return errors.Join(nativeErr, err)
	}
	if nativeErr != nil {
		return fmt.Errorf("pipeline: native admission failed: %w", nativeErr)
	}
	if result.ExitCode != 0 {
		if isGateWaitElapsed(result) {
			return fmt.Errorf("%w: native admission hold elapsed; inspect its receipt and replay only the identical admission, never an ordinary run", pipeline.ErrUnresolved)
		}
		return fmt.Errorf("pipeline: native %s exited %d; its --admission-file contract is required; no fallback or retry was attempted", operation, result.ExitCode)
	}
	return nil
}
