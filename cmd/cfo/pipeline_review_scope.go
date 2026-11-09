package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const reviewScopeJournalFile = "review-scope-decisions.jsonl"

type reviewScopeJournal struct {
	path    string
	file    string
	request []byte
	digest  string
}

// prepareReviewScopeJournal keeps the exact request this task's scope
// decision sends, and refuses any later one that would change a decision the
// journal already holds for the same identity or Review round. An identical
// replay is left to native, which makes it idempotent against the same still
// parked round.
func prepareReviewScopeJournal(taskTemporary string, request []byte) (*reviewScopeJournal, error) {
	var decision pipeline.ReviewScopeDecision
	if err := json.Unmarshal(request, &decision); err != nil {
		return nil, errors.New("pipeline: managed scope decision is unreadable")
	}
	path := filepath.Join(taskTemporary, reviewScopeJournalFile)
	lines, err := fsx.ReadLines(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("pipeline: read Review scope evidence: %w", err)
	}
	for _, line := range lines {
		var record struct {
			ScopeDecision json.RawMessage `json:"scope_decision"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil || len(record.ScopeDecision) == 0 {
			return nil, errors.New("pipeline: Review scope evidence is unreadable; inspect the parked gate before any replay")
		}
		var previous pipeline.ReviewScopeDecision
		if err := json.Unmarshal(record.ScopeDecision, &previous); err != nil {
			return nil, errors.New("pipeline: Review scope evidence contains an unreadable decision")
		}
		if previous.DecisionID == decision.DecisionID || (previous.RunID == decision.RunID && previous.StepResultID == decision.StepResultID && previous.RoundID == decision.RoundID) {
			if !bytes.Equal(record.ScopeDecision, request) {
				return nil, errors.New("pipeline: Review scope authority differs from its durable decision or round binding")
			}
		}
	}
	file, err := os.CreateTemp(taskTemporary, "review-scope-decision-*.json")
	if err != nil {
		return nil, fmt.Errorf("pipeline: create native scope decision file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		os.Remove(file.Name())
		return nil, fmt.Errorf("pipeline: protect native scope decision file: %w", err)
	}
	if _, err := file.Write(request); err != nil {
		file.Close()
		os.Remove(file.Name())
		return nil, fmt.Errorf("pipeline: write native scope decision file: %w", err)
	}
	if err := file.Close(); err != nil {
		os.Remove(file.Name())
		return nil, fmt.Errorf("pipeline: close native scope decision file: %w", err)
	}
	digest := sha256.Sum256(request)
	return &reviewScopeJournal{path: path, file: file.Name(), request: request, digest: hex.EncodeToString(digest[:])}, nil
}

func (j *reviewScopeJournal) Record(h home.Home, taskID string, result execx.Result, nativeErr error) error {
	record := struct {
		At             string          `json:"at"`
		RequestSHA256  string          `json:"request_sha256"`
		ScopeDecision  json.RawMessage `json:"scope_decision"`
		Stdout         string          `json:"stdout"`
		Stderr         string          `json:"stderr"`
		ExitCode       int             `json:"exit_code"`
		ExecutionError string          `json:"execution_error,omitempty"`
	}{
		At:            time.Now().UTC().Format(time.RFC3339Nano),
		RequestSHA256: j.digest,
		ScopeDecision: j.request,
		Stdout:        string(result.Stdout),
		Stderr:        string(result.Stderr),
		ExitCode:      result.ExitCode,
	}
	if nativeErr != nil {
		record.ExecutionError = nativeErr.Error()
	}
	journal, err := fsx.OpenAppend(j.path, 0o600)
	if err != nil {
		return fmt.Errorf("pipeline: could not retain native Review scope outcome; inspect native status before any replay: %w", err)
	}
	if err := json.NewEncoder(journal).Encode(record); err != nil {
		journal.Close()
		return fmt.Errorf("pipeline: could not retain native Review scope outcome; inspect native status before any replay: %w", err)
	}
	if err := journal.Close(); err != nil {
		return fmt.Errorf("pipeline: could not retain native Review scope outcome; inspect native status before any replay: %w", err)
	}
	return state.AppendStatus(h.State, taskID, fmt.Sprintf("pipeline-review-scope: %s native_exit=%d result=%s", j.describe(), result.ExitCode, reviewScopeJournalFile))
}

// describe names the exact decision and request digest the journal keeps, so
// the task's status log points at the evidence without repeating its bytes.
func (j *reviewScopeJournal) describe() string {
	var decision pipeline.ReviewScopeDecision
	if err := json.Unmarshal(j.request, &decision); err != nil {
		return "decision=unreadable request=" + j.digest
	}
	return fmt.Sprintf("decision=%s run=%s round=%s request=%s", decision.DecisionID, decision.RunID, decision.RoundID, j.digest)
}
