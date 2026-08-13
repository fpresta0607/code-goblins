// Package spawn creates one local Windows-native Herdr task and publishes its
// durable identity only after the typed harness has started working.
package spawn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/treehouse"
)

const (
	spawnLockPrefix = ".spawn-"
	spawnLockSuffix = ".lock"
	launchSettle    = 300 * time.Millisecond
	launchWait      = 3 * time.Second
	launchPolls     = 4
)

// Request is the complete local task creation input. Ship delivery posture is
// explicit while scouts deliberately omit it.
type Request struct {
	ID        string
	Project   string
	BriefPath string
	Kind      string
	Mode      string
	Yolo      bool
	Harness   harness.Kind
	Model     string
	Effort    string
	Session   string
}

// Result contains the exact published task identity and user-facing outcome.
type Result struct {
	Meta     state.TaskMeta
	Endpoint herdr.Endpoint
	Output   string
}

// Service owns one local Herdr spawn. Its collaborators are injected through
// their established package seams so operation ordering remains deterministic.
type Service struct {
	Herdr     *herdr.Client
	Treehouse treehouse.Service
	Harness   harness.Registry
	StateDir  string
	Project   string
	Sleep     func(context.Context, time.Duration) error
}

// Spawn creates and launches exactly one local ship or scout task.
func (s Service) Spawn(ctx context.Context, req Request) (Result, error) {
	if err := state.ValidTaskID(req.ID); err != nil {
		return Result{}, err
	}
	project, err := s.project(req)
	if err != nil {
		return Result{}, err
	}
	if err := validateRequest(req); err != nil {
		return Result{}, err
	}
	if err := requireBrief(req); err != nil {
		return Result{}, err
	}
	if err := validateDeliveryContract(req); err != nil {
		return Result{}, err
	}
	adapter, err := s.Harness.Get(req.Harness)
	if err != nil {
		return Result{}, err
	}
	if s.Herdr == nil {
		return Result{}, errors.New("spawn: Herdr client is required")
	}
	herdrClient := *s.Herdr
	if req.Session != "" {
		herdrClient.Session = req.Session
	}

	if err := os.MkdirAll(s.StateDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("spawn: create state directory: %w", err)
	}
	lockName := spawnLockPrefix + req.ID + spawnLockSuffix
	if _, err := lock.AcquireExclusiveNamed(s.StateDir, lockName); err != nil {
		return Result{}, fmt.Errorf("spawn: acquire task lock for %s: %w", req.ID, err)
	}
	defer func() { _ = lock.ReleaseNamed(s.StateDir, lockName) }()

	container, err := herdrClient.EnsureContainer(ctx, project)
	if err != nil {
		return Result{}, fmt.Errorf("spawn: ensure Herdr container: %w", err)
	}
	endpoint, err := herdrClient.CreateTask(ctx, container, "fm-"+req.ID, project)
	if err != nil {
		return Result{}, fmt.Errorf("spawn: create Herdr task tab: %w", err)
	}
	if err := validateEndpoint(endpoint); err != nil {
		return Result{}, err
	}

	pane := herdr.Pane{Client: &herdrClient, Target: endpoint.Target}
	worktree, err := s.Treehouse.Acquire(ctx, pane, project)
	if err != nil {
		return Result{}, fmt.Errorf("spawn: acquire treehouse worktree: %w", err)
	}
	git, err := s.treehouseGit()
	if err != nil {
		return Result{}, err
	}
	if err := treehouse.Validate(ctx, git, project, worktree.Path); err != nil {
		return Result{}, fmt.Errorf("spawn: validate treehouse worktree: %w", err)
	}
	if err := s.Treehouse.Freshen(ctx, worktree.Path); err != nil {
		return Result{}, fmt.Errorf("spawn: freshen treehouse worktree: %w", err)
	}

	if err := adapter.Validate(ctx, herdrClient.Commands); err != nil {
		return Result{}, fmt.Errorf("spawn: validate harness %s: %w", req.Harness, err)
	}
	taskTmp := filepath.Join(s.StateDir, "tasktmp", req.ID)
	if err := os.MkdirAll(filepath.Join(taskTmp, "gotmp"), 0o755); err != nil {
		return Result{}, fmt.Errorf("spawn: create task temporary directory: %w", err)
	}
	launch, err := adapter.Build(harness.LaunchSpec{
		BriefPath: req.BriefPath,
		TaskTmp:   taskTmp,
		Model:     req.Model,
		Effort:    req.Effort,
	})
	if err != nil {
		return Result{}, fmt.Errorf("spawn: build harness launch: %w", err)
	}
	line, err := launch.PowerShellLine()
	if err != nil {
		return Result{}, fmt.Errorf("spawn: render Windows harness launch: %w", err)
	}
	if err := herdrClient.SendLiteral(ctx, endpoint.Target, line); err != nil {
		return s.launchFailure(req.ID, fmt.Errorf("spawn: send harness launch: %w", err))
	}
	if err := s.sleep(ctx, launchSettle); err != nil {
		return s.launchFailure(req.ID, fmt.Errorf("spawn: wait before launch submit: %w", err))
	}
	if err := herdrClient.SendKey(ctx, endpoint.Target, "Enter"); err != nil {
		return s.launchFailure(req.ID, fmt.Errorf("spawn: submit harness launch: %w", err))
	}
	working, err := herdrClient.WaitForWorking(ctx, endpoint.Target, launchWait, launchPolls)
	if err != nil {
		return s.launchFailure(req.ID, fmt.Errorf("spawn: confirm harness launch: %w", err))
	}
	if working != herdr.SubmitWorking {
		return s.launchFailure(req.ID, fmt.Errorf("spawn: harness launch did not report working: %s", working))
	}

	meta := state.TaskMeta{
		ID:               req.ID,
		Window:           endpoint.Target.String(),
		EndpointTaskID:   req.ID,
		Worktree:         worktree.Path,
		Project:          project,
		Harness:          string(req.Harness),
		Kind:             req.Kind,
		TaskTmp:          taskTmp,
		Model:            valueOrDefault(req.Model),
		Effort:           valueOrDefault(req.Effort),
		SpawnGen:         fmt.Sprintf("s%d", time.Now().UTC().UnixNano()),
		Backend:          "herdr",
		HerdrSession:     endpoint.Target.Session,
		HerdrWorkspaceID: endpoint.WorkspaceID,
		HerdrTabID:       endpoint.TabID,
		HerdrPaneID:      endpoint.PaneID,
	}
	if req.Kind == "ship" {
		meta.Mode = req.Mode
		meta.Yolo = yoloString(req.Yolo)
	}
	if err := state.WriteTaskMeta(s.StateDir, meta); err != nil {
		return Result{}, fmt.Errorf("spawn: publish task metadata: %w", err)
	}
	return Result{Meta: meta, Endpoint: endpoint, Output: successOutput(meta)}, nil
}

func (s Service) project(req Request) (string, error) {
	project := req.Project
	if project == "" {
		project = s.Project
	}
	if project == "" {
		return "", errors.New("spawn: project is required")
	}
	absolute, err := filepath.Abs(project)
	if err != nil {
		return "", fmt.Errorf("spawn: resolve project path: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("spawn: project %q: %w", absolute, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("spawn: project %q is not a directory", absolute)
	}
	return filepath.Clean(absolute), nil
}

func validateRequest(req Request) error {
	switch req.Kind {
	case "ship":
		if !validMode(req.Mode) {
			return fmt.Errorf("spawn: ship task requires mode no-mistakes, direct-PR, or local-only")
		}
	case "scout":
		if req.Mode != "" || req.Yolo {
			return errors.New("spawn: scout task must not include mode or yolo")
		}
	default:
		return fmt.Errorf("spawn: unsupported task kind %q", req.Kind)
	}
	return nil
}

func requireBrief(req Request) error {
	if req.BriefPath == "" {
		return errors.New("spawn: brief path is required")
	}
	absolute, err := filepath.Abs(req.BriefPath)
	if err != nil {
		return fmt.Errorf("spawn: resolve brief path: %w", err)
	}
	if !filepath.IsAbs(req.BriefPath) {
		return errors.New("spawn: brief path must be absolute")
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return fmt.Errorf("spawn: brief %q: %w", absolute, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("spawn: brief %q is not a regular file", absolute)
	}
	return nil
}

func validateDeliveryContract(req Request) error {
	if req.Kind != "ship" {
		return nil
	}
	data, err := os.ReadFile(req.BriefPath)
	if err != nil {
		return fmt.Errorf("spawn: read brief delivery contract: %w", err)
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		mode, found := strings.CutPrefix(line, "Delivery contract: mode=")
		if !found {
			continue
		}
		fields := strings.Fields(mode)
		if len(fields) == 0 {
			return fmt.Errorf("spawn: brief delivery contract for %s has an empty mode", req.ID)
		}
		mode = fields[0]
		if mode != req.Mode {
			return fmt.Errorf("spawn: delivery mismatch for %s: brief says mode=%s, request says mode=%s", req.ID, mode, req.Mode)
		}
		return nil
	}
	return nil
}

func validateEndpoint(endpoint herdr.Endpoint) error {
	if endpoint.Target.Session == "" || endpoint.Target.Pane == "" || endpoint.WorkspaceID == "" || endpoint.TabID == "" || endpoint.PaneID == "" {
		return errors.New("spawn: Herdr task creation returned missing workspace_id, tab_id, or root pane_id")
	}
	if endpoint.Target.Pane != endpoint.PaneID {
		return errors.New("spawn: Herdr task creation returned mismatched target and pane IDs")
	}
	return nil
}

func (s Service) treehouseGit() (treehouse.Git, error) {
	if s.Treehouse.Git != nil {
		return s.Treehouse.Git, nil
	}
	if s.Treehouse.Commands == nil {
		return nil, errors.New("spawn: treehouse Git dependency is required")
	}
	return treehouse.RunnerGit{Commands: s.Treehouse.Commands, Sleep: s.Treehouse.Sleep}, nil
}

func (s Service) launchFailure(id string, cause error) (Result, error) {
	line := "failed: " + bounded(normalizeStatusDetail(cause.Error()), 1000)
	if err := state.AppendStatus(s.StateDir, id, line); err != nil {
		return Result{}, fmt.Errorf("%w; spawn: record launch failure: %v", cause, err)
	}
	return Result{}, cause
}

func (s Service) sleep(ctx context.Context, duration time.Duration) error {
	if s.Sleep != nil {
		return s.Sleep(ctx, duration)
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func validMode(mode string) bool {
	switch mode {
	case "no-mistakes", "direct-PR", "local-only":
		return true
	default:
		return false
	}
}

func valueOrDefault(value string) string {
	if value == "" {
		return "default"
	}
	return value
}

func yoloString(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func successOutput(meta state.TaskMeta) string {
	if meta.Kind == "ship" {
		return fmt.Sprintf("spawned %s harness=%s kind=%s mode=%s yolo=%s window=%s worktree=%s", meta.ID, meta.Harness, meta.Kind, meta.Mode, meta.Yolo, meta.Window, meta.Worktree)
	}
	return fmt.Sprintf("spawned %s harness=%s kind=%s window=%s worktree=%s", meta.ID, meta.Harness, meta.Kind, meta.Window, meta.Worktree)
}

func bounded(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func normalizeStatusDetail(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
}
