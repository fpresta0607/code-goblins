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
	"unicode"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/treehouse"
)

const (
	spawnLockName      = ".spawn.lock"
	launchSettle       = 300 * time.Millisecond
	launchConfirmPoll  = 1500 * time.Millisecond
	launchConfirmTries = 80
	registrationPoll   = 500 * time.Millisecond
	registrationTries  = 40
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
	Herdr       *herdr.Client
	Treehouse   treehouse.Service
	Harness     harness.Registry
	StateDir    string
	Project     string
	Sleep       func(context.Context, time.Duration) error
	ReleaseLock func(string, string) error
}

// Spawn creates and launches exactly one local ship or scout task.
func (s Service) Spawn(ctx context.Context, req Request) (result Result, err error) {
	if err := state.ValidTaskID(req.ID); err != nil {
		return Result{}, err
	}
	if err := validateRequestLineValues(req); err != nil {
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
	taskTmp := filepath.Join(s.StateDir, "tasktmp", req.ID)
	if err := validateLineValues("project", project, "herdr session", herdrClient.Session, "tasktmp", taskTmp); err != nil {
		return Result{}, err
	}

	if err := os.MkdirAll(s.StateDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("spawn: create state directory: %w", err)
	}
	if _, err := lock.AcquireExclusiveNamed(s.StateDir, spawnLockName); err != nil {
		return Result{}, fmt.Errorf("spawn: acquire spawn lock: %w", err)
	}
	defer func() {
		if releaseErr := s.releaseTaskLock(s.StateDir, spawnLockName); releaseErr != nil {
			releaseErr = fmt.Errorf("spawn: release spawn lock: %w", releaseErr)
			if err == nil {
				err = releaseErr
			} else {
				err = errors.Join(err, releaseErr)
			}
		}
	}()
	if err := rejectTaskIDAlias(s.StateDir, req.ID); err != nil {
		return Result{}, err
	}

	if err := herdrClient.EnsureServer(ctx); err != nil {
		return Result{}, fmt.Errorf("spawn: ensure Herdr server: %w", err)
	}
	if err := herdrClient.Preflight(ctx); err != nil {
		return Result{}, fmt.Errorf("spawn: Herdr compatibility preflight: %w", err)
	}
	container, err := herdrClient.EnsureContainer(ctx, project)
	if err != nil {
		return Result{}, fmt.Errorf("spawn: ensure Herdr container: %w", err)
	}
	if err := validateContainer(container); err != nil {
		return Result{}, err
	}
	endpoint, err := herdrClient.CreateTask(ctx, container, "gb-"+req.ID, project)
	if err != nil {
		return Result{}, fmt.Errorf("spawn: create Herdr task tab: %w", err)
	}
	if err := validateEndpoint(endpoint); err != nil {
		return Result{}, err
	}

	worktree, err := s.Treehouse.Acquire(ctx, project, "gb-"+req.ID)
	if err != nil {
		return Result{}, fmt.Errorf("spawn: acquire treehouse worktree: %w", err)
	}
	result = partialResult(req, project, taskTmp, endpoint, worktree.Path)
	launchSubmitted := false
	fail := func(result Result, cause error) (Result, error) {
		line := "failed: " + bounded(normalizeStatusDetail(cause.Error()), 1000)
		if err := state.AppendStatus(s.StateDir, result.Meta.ID, line); err != nil {
			cause = errors.Join(cause, fmt.Errorf("spawn: record launch failure: %w", err))
		}
		if !launchSubmitted {
			if err := s.Treehouse.Return(ctx, project, worktree.Path); err != nil {
				cause = errors.Join(cause, fmt.Errorf("spawn: return treehouse worktree after launch failure: %w", err))
			}
		}
		return result, cause
	}
	if err := validateLineValues("worktree", worktree.Path); err != nil {
		return fail(result, err)
	}
	git, err := s.treehouseGit()
	if err != nil {
		return fail(result, err)
	}
	if err := treehouse.Validate(ctx, git, project, worktree.Path); err != nil {
		return fail(result, fmt.Errorf("spawn: validate treehouse worktree: %w", err))
	}
	if err := s.Treehouse.Freshen(ctx, worktree.Path); err != nil {
		return fail(result, fmt.Errorf("spawn: freshen treehouse worktree: %w", err))
	}

	if err := adapter.Validate(ctx, herdrClient.Commands); err != nil {
		return fail(result, fmt.Errorf("spawn: validate harness %s: %w", req.Harness, err))
	}
	if err := os.MkdirAll(filepath.Join(taskTmp, "gotmp"), 0o755); err != nil {
		return fail(result, fmt.Errorf("spawn: create task temporary directory: %w", err))
	}
	launch, err := adapter.Build(harness.LaunchSpec{
		BriefPath: req.BriefPath,
		TaskTmp:   taskTmp,
		Model:     req.Model,
		Effort:    req.Effort,
	})
	if err != nil {
		return fail(result, fmt.Errorf("spawn: build harness launch: %w", err))
	}
	launch.Dir = worktree.Path
	line, err := launch.PowerShellLine()
	if err != nil {
		return fail(result, fmt.Errorf("spawn: render Windows harness launch: %w", err))
	}
	if err := herdrClient.SendLiteral(ctx, endpoint.Target, line); err != nil {
		return fail(result, fmt.Errorf("spawn: send harness launch: %w", err))
	}
	if err := s.sleep(ctx, launchSettle); err != nil {
		return fail(result, fmt.Errorf("spawn: wait before launch submit: %w", err))
	}
	if err := herdrClient.SendKey(ctx, endpoint.Target, "Enter"); err != nil {
		return fail(result, fmt.Errorf("spawn: submit harness launch: %w", err))
	}
	launchSubmitted = true
	if launch.FollowUpPrompt != "" {
		// A bare launch (Kimi) needs its composer ready before the follow-up
		// lands; text sent during harness boot is lost silently. Wait for the
		// agent to register with Herdr instead of a fixed settle.
		if err := s.waitForAgentRegistered(ctx, &herdrClient, endpoint.Target); err != nil {
			return fail(result, fmt.Errorf("spawn: wait for harness registration: %w", err))
		}
		if err := s.sleep(ctx, launchSettle); err != nil {
			return fail(result, fmt.Errorf("spawn: wait before follow-up prompt: %w", err))
		}
		if err := herdrClient.SendLiteral(ctx, endpoint.Target, launch.FollowUpPrompt); err != nil {
			return fail(result, fmt.Errorf("spawn: send follow-up prompt: %w", err))
		}
		if err := s.sleep(ctx, launchSettle); err != nil {
			return fail(result, fmt.Errorf("spawn: wait before follow-up submit: %w", err))
		}
		if err := herdrClient.SendKey(ctx, endpoint.Target, "Enter"); err != nil {
			return fail(result, fmt.Errorf("spawn: submit follow-up prompt: %w", err))
		}
	}
	if err := s.confirmLaunch(ctx, &herdrClient, endpoint.Target, launch.ConfirmMarkers); err != nil {
		return fail(result, err)
	}

	result.Meta.SpawnGen = fmt.Sprintf("s%d", time.Now().UTC().UnixNano())
	if err := state.WriteTaskMeta(s.StateDir, result.Meta); err != nil {
		return fail(result, fmt.Errorf("spawn: publish task metadata: %w", err))
	}
	result.Output = successOutput(result.Meta)
	return result, nil
}

func (s Service) project(req Request) (string, error) {
	project := req.Project
	if project == "" {
		project = s.Project
	}
	if project == "" {
		return "", errors.New("spawn: project is required")
	}
	info, err := os.Stat(project)
	if err != nil {
		return "", fmt.Errorf("spawn: project %q: %w", project, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("spawn: project %q is not a directory", project)
	}
	canonical, err := fsx.Canonical(project)
	if err != nil {
		return "", fmt.Errorf("spawn: canonicalize project %q: %w", project, err)
	}
	return canonical, nil
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
	return validateLineValues(
		"Herdr endpoint session", endpoint.Target.Session,
		"Herdr endpoint workspace_id", endpoint.WorkspaceID,
		"Herdr endpoint tab_id", endpoint.TabID,
		"Herdr endpoint pane_id", endpoint.PaneID,
	)
}

func validateContainer(container herdr.Container) error {
	if container.Session == "" || container.WorkspaceID == "" {
		return errors.New("spawn: Herdr container returned missing session or workspace_id")
	}
	return validateLineValues(
		"Herdr container session", container.Session,
		"Herdr container workspace_id", container.WorkspaceID,
		"Herdr container seeded tab_id", container.SeededDefaultTab,
	)
}

func validateRequestLineValues(req Request) error {
	return validateLineValues(
		"request project", req.Project,
		"request brief", req.BriefPath,
		"request kind", req.Kind,
		"request mode", req.Mode,
		"request harness", string(req.Harness),
		"request model", req.Model,
		"request effort", req.Effort,
		"request session", req.Session,
	)
}

func validateLineValues(values ...string) error {
	for index := 0; index < len(values); index += 2 {
		name, value := values[index], values[index+1]
		for _, char := range value {
			if unicode.IsControl(char) {
				return fmt.Errorf("spawn: %s contains control character %U", name, char)
			}
		}
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

func partialResult(req Request, project, taskTmp string, endpoint herdr.Endpoint, worktree string) Result {
	meta := state.TaskMeta{
		ID:               req.ID,
		Window:           endpoint.Target.String(),
		EndpointTaskID:   req.ID,
		Worktree:         worktree,
		Project:          project,
		Harness:          string(req.Harness),
		Kind:             req.Kind,
		TaskTmp:          taskTmp,
		Model:            valueOrDefault(req.Model),
		Effort:           valueOrDefault(req.Effort),
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
	return Result{Meta: meta, Endpoint: endpoint}
}

// waitForAgentRegistered polls until Herdr reports a registered agent for the
// pane, which is the earliest safe moment to type into a booted harness TUI.
func (s Service) waitForAgentRegistered(ctx context.Context, client *herdr.Client, target herdr.Target) error {
	for attempt := 0; attempt < registrationTries; attempt++ {
		if attempt > 0 {
			if err := s.sleep(ctx, registrationPoll); err != nil {
				return fmt.Errorf("spawn: wait for agent registration: %w", err)
			}
		}
		status, err := client.AgentStatus(ctx, target)
		if err != nil {
			continue
		}
		if status == herdr.AgentAlive {
			return nil
		}
	}
	return fmt.Errorf("spawn: harness agent did not register with Herdr within %ds", int(registrationPoll.Seconds()*registrationTries))
}

// confirmLaunch waits for the launched harness to report working, confirming
// any harness-declared blocking dialog (such as the Claude workspace trust
// prompt shown for every fresh worktree) with a single Enter keypress.
func (s Service) confirmLaunch(ctx context.Context, client *herdr.Client, target herdr.Target, markers []string) error {
	for attempt := 0; attempt < launchConfirmTries; attempt++ {
		if attempt > 0 {
			if err := s.sleep(ctx, launchConfirmPoll); err != nil {
				return fmt.Errorf("spawn: wait while confirming harness launch: %w", err)
			}
		}
		state, err := client.WaitForWorking(ctx, target, 0, 1)
		if err != nil {
			return fmt.Errorf("spawn: confirm harness launch: %w", err)
		}
		switch state {
		case herdr.SubmitWorking:
			return nil
		case herdr.SubmitBlocked:
			if len(markers) == 0 {
				continue
			}
			capture, err := client.Capture(ctx, target, 60, false)
			if err != nil || !containsMarker(capture, markers) {
				continue
			}
			if err := client.SendKey(ctx, target, "Enter"); err != nil {
				return fmt.Errorf("spawn: confirm harness launch dialog: %w", err)
			}
		}
	}
	return fmt.Errorf("spawn: harness launch did not report working within %ds", int(launchConfirmPoll.Seconds()*launchConfirmTries))
}

// containsMarker matches against whitespace-normalized text: pane captures
// wrap long dialog sentences across lines at the viewport width, so a marker
// containing spaces never survives verbatim in the raw capture.
func containsMarker(capture string, markers []string) bool {
	compact := stripWhitespace(capture)
	for _, marker := range markers {
		if strings.Contains(compact, stripWhitespace(marker)) {
			return true
		}
	}
	return false
}

func stripWhitespace(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, text)
}

func (s Service) releaseTaskLock(dir, name string) error {
	if s.ReleaseLock != nil {
		return s.ReleaseLock(dir, name)
	}
	return lock.ReleaseExclusiveNamed(dir, name)
}

// rejectTaskIDAlias prevents Windows task-state collisions before a spawn can
// create any Herdr or worktree resources. Task IDs remain case-preserving in
// metadata, but their retained state artifact paths are not case-distinct on
// Windows.
func rejectTaskIDAlias(stateDir, id string) error {
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return fmt.Errorf("spawn: list task state artifacts: %w", err)
	}
	for _, entry := range entries {
		extension := filepath.Ext(entry.Name())
		if entry.IsDir() || (!strings.EqualFold(extension, ".meta") && !strings.EqualFold(extension, ".status")) {
			continue
		}
		existingID := strings.TrimSuffix(entry.Name(), extension)
		if taskIDsAlias(id, existingID) {
			return fmt.Errorf("spawn: task ID %q conflicts case-insensitively with retained task state for %q", id, existingID)
		}
	}

	taskTmp := filepath.Join(stateDir, "tasktmp")
	entries, err = os.ReadDir(taskTmp)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("spawn: list task temporary directories: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() && taskIDsAlias(id, entry.Name()) {
			return fmt.Errorf("spawn: task ID %q conflicts case-insensitively with retained task temporary directory %q", id, entry.Name())
		}
	}
	return nil
}

func taskIDsAlias(id, existingID string) bool {
	return state.ValidTaskID(existingID) == nil && strings.EqualFold(existingID, id)
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
