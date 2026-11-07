package pipeline

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// ReviewScopeDecision is the exact record the native engine takes for an
// explicitly accepted Review scope. The managed controller authenticates the
// decision's maker through its own primary registration, so the record carries
// the native engine's controller-assertion authority and never an actor name.
type ReviewScopeDecision struct {
	Version          int             `json:"version"`
	DecisionID       string          `json:"decision_id"`
	Authority        string          `json:"authority"`
	RunID            string          `json:"run_id"`
	StepResultID     string          `json:"step_result_id"`
	RoundID          string          `json:"round_id"`
	SourceHeadSHA    string          `json:"source_head_sha"`
	SourceBaseSHA    string          `json:"source_base_sha"`
	TrustedConfigSHA string          `json:"trusted_config_sha"`
	Finding          json.RawMessage `json:"finding"`
	AcceptedScope    string          `json:"accepted_scope"`
}

type nativeStep struct {
	ID            string `json:"id"`
	Name          string `json:"step_name"`
	Status        string `json:"status"`
	LatestRoundID string `json:"latest_round_id"`
}

type nativeRun struct {
	ID      string       `json:"id"`
	HeadSHA string       `json:"head_sha"`
	BaseSHA string       `json:"base_sha"`
	Steps   []nativeStep `json:"steps"`
}

// nativeReviewRound is one persisted Review round as `axi rounds --json`
// publishes it, which is the only typed record of the round's source,
// configuration and full findings.
type nativeReviewRound struct {
	ID               string            `json:"id"`
	StepResultID     string            `json:"step_result_id"`
	ReviewedHeadSHA  *string           `json:"reviewed_head_sha"`
	TrustedConfigSHA *string           `json:"trusted_config_sha"`
	Round            int               `json:"round"`
	Findings         []json.RawMessage `json:"findings"`
}

type nativeRounds struct {
	SchemaVersion  int                 `json:"schema_version"`
	RunID          string              `json:"run_id"`
	Step           string              `json:"step"`
	Rounds         []nativeReviewRound `json:"rounds"`
	ScopeDecisions []json.RawMessage   `json:"scope_decisions"`
}

// ReviewScopeDecision binds one accepted requirement correction to the exact
// parked Review round it answers: the run and step identity, the round and its
// source head and base, its trusted configuration digest, and the complete
// original finding. Every part comes from native typed evidence, so a caller
// cannot mint a decision by naming text or an environment label.
func (r Reader) ReviewScopeDecision(ctx context.Context, worktree string, env []string, gate Gate, findingID, decisionID, acceptedScope string) ([]byte, error) {
	if gate.Step != "review" || gate.RunID == "" || gate.StepID == "" || gate.Round < 1 || strings.Contains(findingID, ",") || strings.TrimSpace(findingID) == "" || strings.TrimSpace(decisionID) == "" || strings.TrimSpace(acceptedScope) == "" {
		return nil, errors.New("pipeline: a scope decision requires one active Review finding and a complete primary decision")
	}
	run, err := r.getNativeRun(ctx, gate.RunID)
	if err != nil {
		return nil, err
	}
	// Native's typed get_run RunInfo carries the run's exact source pair; the
	// round itself and its configuration digest come from native's typed
	// `axi rounds` document below, and the gate supplies the round number.
	if run.ID != gate.RunID || run.HeadSHA == "" || run.BaseSHA == "" {
		return nil, errors.New("pipeline: native RunInfo lacks the exact source binding for this Review gate")
	}
	var reviewStep *nativeStep
	for index := range run.Steps {
		step := &run.Steps[index]
		if step.Name == "review" && step.ID == gate.StepID {
			reviewStep = step
			break
		}
	}
	if reviewStep == nil || reviewStep.LatestRoundID == "" || reviewStep.Status != gate.Status {
		return nil, errors.New("pipeline: native RunInfo does not confirm the parked Review step")
	}
	rounds, err := r.reviewRounds(ctx, worktree, env, gate.RunID)
	if err != nil {
		return nil, err
	}
	if rounds.SchemaVersion != 1 || rounds.RunID != gate.RunID || rounds.Step != "review" {
		return nil, errors.New("pipeline: native Review rounds do not bind this gate")
	}
	var latest *nativeReviewRound
	for index := range rounds.Rounds {
		round := &rounds.Rounds[index]
		if round.ID == reviewStep.LatestRoundID {
			latest = round
			break
		}
	}
	if latest == nil || latest.StepResultID != gate.StepID || latest.Round != gate.Round || latest.ReviewedHeadSHA == nil || *latest.ReviewedHeadSHA != run.HeadSHA || latest.TrustedConfigSHA == nil || *latest.TrustedConfigSHA == "" {
		return nil, errors.New("pipeline: native Review round lacks the exact source, configuration or identity binding")
	}
	var finding json.RawMessage
	for _, candidate := range latest.Findings {
		var identity struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(candidate, &identity); err != nil {
			return nil, errors.New("pipeline: native Review finding is unreadable")
		}
		if identity.ID == findingID {
			if finding != nil {
				return nil, errors.New("pipeline: native Review round has duplicate selected finding IDs")
			}
			// The complete typed finding travels, never only its positional id.
			finding = append(json.RawMessage(nil), candidate...)
		}
	}
	if finding == nil {
		return nil, errors.New("pipeline: selected scope finding is absent from the native Review round")
	}
	decision := ReviewScopeDecision{
		Version:          1,
		DecisionID:       decisionID,
		Authority:        "controller_assertion",
		RunID:            gate.RunID,
		StepResultID:     gate.StepID,
		RoundID:          latest.ID,
		SourceHeadSHA:    run.HeadSHA,
		SourceBaseSHA:    run.BaseSHA,
		TrustedConfigSHA: *latest.TrustedConfigSHA,
		Finding:          finding,
		AcceptedScope:    acceptedScope,
	}
	encoded, err := json.Marshal(decision)
	if err != nil || len(encoded) > 32*1024 {
		return nil, errors.New("pipeline: managed scope decision is invalid or exceeds the native limit")
	}
	return encoded, nil
}

// ConfirmReviewScope requires native's own durable record of exactly the
// decision it acknowledged: the same identity, run, step, round, source,
// configuration, original finding and accepted scope, read back as typed
// rounds evidence. An acknowledgement native cannot be shown to have kept is
// uncertain, and the caller refuses on it rather than falling back.
func (r Reader) ConfirmReviewScope(ctx context.Context, worktree string, env []string, request []byte) error {
	var decision ReviewScopeDecision
	if err := json.Unmarshal(request, &decision); err != nil {
		return errors.New("pipeline: managed scope decision is unreadable")
	}
	if decision.DecisionID == "" || decision.RunID == "" {
		return errors.New("pipeline: managed scope decision lacks its identity")
	}
	rounds, err := r.reviewRounds(ctx, worktree, env, decision.RunID)
	if err != nil {
		return err
	}
	var durable []json.RawMessage
	for _, candidate := range rounds.ScopeDecisions {
		var identity struct {
			DecisionID string `json:"decision_id"`
		}
		if err := json.Unmarshal(candidate, &identity); err != nil {
			return errors.New("pipeline: native scope evidence is unreadable")
		}
		if identity.DecisionID == decision.DecisionID {
			durable = append(durable, candidate)
		}
	}
	if len(durable) != 1 {
		return fmt.Errorf("pipeline: native holds no single durable scope decision %s for run %s", decision.DecisionID, decision.RunID)
	}
	equal, err := sameJSON(durable[0], request)
	if err != nil {
		return err
	}
	if !equal {
		return fmt.Errorf("pipeline: native's durable scope decision %s differs from the accepted request", decision.DecisionID)
	}
	return nil
}

// sameJSON compares two documents by their decoded content, since native
// re-encodes the finding it persisted while the request carried the round's
// own encoding of it.
func sameJSON(left, right []byte) (bool, error) {
	var a, b any
	if err := decodeScopeJSON(left, &a); err != nil {
		return false, fmt.Errorf("pipeline: native scope evidence is unreadable: %w", err)
	}
	if err := decodeScopeJSON(right, &b); err != nil {
		return false, fmt.Errorf("pipeline: managed scope decision is unreadable: %w", err)
	}
	return reflect.DeepEqual(a, b), nil
}

func (r Reader) reviewRounds(ctx context.Context, worktree string, env []string, runID string) (nativeRounds, error) {
	result, err := r.Commands.Run(ctx, execx.Request{Dir: worktree, Env: env, Name: "no-mistakes", Args: []string{"axi", "rounds", "--step", "review", "--run", runID, "--json"}})
	if err != nil {
		return nativeRounds{}, fmt.Errorf("pipeline: read native Review rounds: %w", err)
	}
	if result.ExitCode != 0 {
		return nativeRounds{}, fmt.Errorf("pipeline: native Review rounds exited %d", result.ExitCode)
	}
	var document nativeRounds
	if err := decodeScopeJSON(result.Stdout, &document); err != nil {
		return nativeRounds{}, fmt.Errorf("pipeline: native Review rounds are unreadable: %w", err)
	}
	return document, nil
}

func (r Reader) getNativeRun(ctx context.Context, runID string) (nativeRun, error) {
	conn, err := dialNativeScope(ctx, filepath.Join(r.Root, "socket"))
	if err != nil {
		return nativeRun{}, fmt.Errorf("pipeline: native get_run is unavailable: %w", err)
	}
	defer conn.Close()
	deadline := time.Now().Add(30 * time.Second)
	if limit, ok := ctx.Deadline(); ok && limit.Before(deadline) {
		deadline = limit
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nativeRun{}, fmt.Errorf("pipeline: bound native get_run: %w", err)
	}
	request := struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  struct {
			RunID string `json:"run_id"`
		} `json:"params"`
		ID               int64 `json:"id"`
		DeadlineUnixNano int64 `json:"deadline_unix_nano"`
	}{JSONRPC: "2.0", Method: "get_run", ID: 1, DeadlineUnixNano: deadline.UnixNano()}
	request.Params.RunID = runID
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return nativeRun{}, fmt.Errorf("pipeline: send native get_run: %w", err)
	}
	var response struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nativeRun{}, fmt.Errorf("pipeline: read native get_run: %w", err)
		}
		return nativeRun{}, errors.New("pipeline: native get_run closed without a response")
	}
	if err := decodeScopeJSON(scanner.Bytes(), &response); err != nil {
		return nativeRun{}, fmt.Errorf("pipeline: native get_run response is unreadable: %w", err)
	}
	if response.Error != nil {
		return nativeRun{}, fmt.Errorf("pipeline: native get_run refused: %s", response.Error.Message)
	}
	var result struct {
		Run *nativeRun `json:"run"`
	}
	if err := decodeScopeJSON(response.Result, &result); err != nil || result.Run == nil {
		return nativeRun{}, errors.New("pipeline: native get_run returned no typed RunInfo")
	}
	return *result.Run, nil
}

// dialNativeScope opens the native engine's own authenticated local endpoint:
// a Unix socket on Unix, and on Windows the endpoint file naming the loopback
// address, the token and the owning pid, which is answered with that token.
func dialNativeScope(ctx context.Context, endpoint string) (net.Conn, error) {
	dialer := net.Dialer{}
	if runtime.GOOS != "windows" {
		return dialer.DialContext(ctx, "unix", endpoint)
	}
	data, err := fsx.ReadFile(endpoint)
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(string(data), "\n", 3)
	if len(parts) != 3 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return nil, errors.New("invalid native IPC endpoint")
	}
	conn, err := dialer.DialContext(ctx, "tcp", strings.TrimSpace(parts[0]))
	if err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintln(conn, strings.TrimSpace(parts[1])); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func decodeScopeJSON(data []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}
