// Package spawn creates one local Windows-native Herdr task and publishes its
// durable identity as soon as the pane and worktree exist, before the harness
// is confirmed working, so a failed launch stays addressable and cleanable.
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

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

const (
	spawnLockName      = ".spawn.lock"
	launchSettle       = 300 * time.Millisecond
	launchConfirmPoll  = 1500 * time.Millisecond
	launchConfirmTries = 80
	instructionTries   = 60
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

// AuthPreflight resolves one project's credentials before a harness starts,
// so a goblin inherits working CLIs and environment variables instead of
// discovering an unauthenticated service mid-task.
type AuthPreflight interface {
	// Preflight returns the environment to inject, a single-line warning
	// naming anything that is not usable, and the refusal that must stop the
	// dispatch when a blocking service is red. A project with no manifest is
	// not an error: most projects need nothing.
	Preflight(ctx context.Context, project string) (auth.Result, error)
}

// Service owns one local Herdr spawn. Its collaborators are injected through
// their established package seams so operation ordering remains deterministic.
type Service struct {
	Herdr       *herdr.Client
	Worktrees   worktree.Service
	Harness     harness.Registry
	Auth        AuthPreflight
	Commands    execx.Runner
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
	// The spawn lock covers the whole dispatch, not just worktree acquisition:
	// task id alias rejection, Herdr server and container start, pane and tab
	// creation, metadata publication and the harness launch all mutate shared
	// fleet state under it. Dependency provisioning (about 5s pnpm, about 22s
	// uv against warm caches) therefore runs under it too, so concurrent
	// dispatches into install-strategy projects serialize behind each other's
	// installer. That is a chosen property: narrowing the lock to Acquire is a
	// redesign of the spawn critical section, and the cost is a slower
	// concurrent dispatch, never a wrong one.
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
	if err := s.ensureProjectSeeded(ctx, project); err != nil {
		return Result{}, err
	}

	// The preflight runs before anything is built, so a goblin that would
	// start without a credential it needs costs no pane and no worktree.
	// Dispatching anyway is what let a stale DATABASE_URL reach a goblin, so
	// a red blocking service stops here; --yolo is the existing override.
	preflight, err := s.preflightCredentials(ctx, project)
	if err != nil {
		return Result{}, err
	}
	if preflight.Refusal != "" && !req.Yolo {
		return Result{}, fmt.Errorf("spawn: %s", preflight.Refusal)
	}

	if err := herdrClient.EnsureServer(ctx); err != nil {
		return Result{}, fmt.Errorf("spawn: ensure Herdr server: %w", err)
	}
	if err := herdrClient.Preflight(ctx); err != nil {
		return Result{}, fmt.Errorf("spawn: Herdr compatibility preflight: %w", err)
	}
	kinds, err := herdrClient.AgentKinds(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("spawn: list Herdr agent kinds: %w", err)
	}
	if !kinds[string(req.Harness)] {
		return Result{}, fmt.Errorf("spawn: installed Herdr does not support harness kind %q", req.Harness)
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

	wt, err := s.Worktrees.Acquire(ctx, project, "gb-"+req.ID)
	if err != nil {
		return Result{}, fmt.Errorf("spawn: acquire task worktree: %w", err)
	}
	result = partialResult(req, project, taskTmp, endpoint, wt.Path)

	// Publish metadata as soon as the pane and worktree exist, before the
	// harness can start: a task whose launch later fails is then addressable
	// and cleanable through `cfo peek`/`cfo cleanup` instead of an unnameable
	// orphan whose pane the CFO has to hunt down by hand.
	result.Meta.SpawnGen = fmt.Sprintf("s%d", time.Now().UTC().UnixNano())
	if err := state.WriteTaskMeta(s.StateDir, result.Meta); err != nil {
		return Result{}, errors.Join(
			fmt.Errorf("spawn: publish task metadata: %w", err),
			s.teardownLaunch(ctx, &herdrClient, endpoint, project, wt.Path, result.Meta.ID),
		)
	}

	// fail records the exact cause and tears the half-built task down cleanly
	// (close the tab, return the worktree, retire the metadata). It is the one
	// failure path: nothing here can leave an unaddressable pane behind.
	fail := func(result Result, cause error) (Result, error) {
		line := "failed: " + bounded(state.NormalizeStatusDetail(cause.Error()), 1000)
		if err := state.AppendStatus(s.StateDir, result.Meta.ID, line); err != nil {
			cause = errors.Join(cause, fmt.Errorf("spawn: record launch failure: %w", err))
		}
		if err := s.teardownLaunch(ctx, &herdrClient, endpoint, project, wt.Path, result.Meta.ID); err != nil {
			cause = errors.Join(cause, err)
		}
		return result, cause
	}
	if err := validateLineValues("worktree", wt.Path); err != nil {
		return fail(result, err)
	}
	git, err := s.worktreeGit()
	if err != nil {
		return fail(result, err)
	}
	if err := worktree.Validate(ctx, git, project, wt.Path); err != nil {
		return fail(result, fmt.Errorf("spawn: validate task worktree: %w", err))
	}

	if err := adapter.Validate(ctx, herdrClient.Commands); err != nil {
		return fail(result, fmt.Errorf("spawn: validate harness %s: %w", req.Harness, err))
	}
	if err := os.MkdirAll(filepath.Join(taskTmp, "gotmp"), 0o755); err != nil {
		return fail(result, fmt.Errorf("spawn: create task temporary directory: %w", err))
	}
	// The worktree starts as tracked files only; provisioning is what makes it
	// runnable as if it were the project - shared config, dependencies
	// installed against the shared package cache, and the token-authenticated
	// subset of the project's MCP servers.
	provision, err := s.Worktrees.Provision(ctx, project, wt.Path, taskTmp)
	if err != nil {
		return fail(result, fmt.Errorf("spawn: provision worktree environment: %w", err))
	}
	launch, err := adapter.Build(harness.LaunchSpec{
		BriefPath: req.BriefPath,
		TaskTmp:   taskTmp,
		Model:     req.Model,
		Effort:    req.Effort,
		MCPConfig: provision.MCPConfig,
	})
	if err != nil {
		return fail(result, fmt.Errorf("spawn: build harness launch: %w", err))
	}
	launch.Dir = wt.Path
	mergeProvisionEnv(launch.Env, provision.Env)
	// Every goblin is told to report its outcome through cfo notify, so the
	// CFO is woken with the actual PR URL, question, or failure reason instead
	// of the watcher guessing from pane text.
	launch.Instruction = spawnInstruction(req.BriefPath, req.ID)
	if err := s.injectProjectCredentials(preflight, taskTmp, &launch); err != nil {
		return fail(result, err)
	}
	if _, err := s.startHarness(ctx, &herdrClient, endpoint.Target, launchPlan{
		AgentName: "gb-" + req.ID,
		Harness:   req.Harness,
		Launch:    launch,
	}); err != nil {
		return fail(result, err)
	}

	result.Output = successOutput(result.Meta)
	if provision.Installed != "" {
		result.Output += "\ndependencies: " + provision.Installed
	}
	if len(provision.LinkSkipped) > 0 {
		result.Output += "\nlink: " + strings.Join(provision.LinkSkipped, ", ") + " already present in the worktree (the project's own checked-out file), so the default share was skipped"
	}
	if provision.InstallFailed != "" {
		// Reported, not fatal: the goblin can run the installer itself, and
		// repairing the lockfile may be the task it was dispatched for.
		result.Output += "\ndependencies: strategy install failed at " + provision.InstallFailed + "; the goblin was dispatched without them: " + provision.InstallOutput
	}
	if len(provision.MCPDropped) > 0 {
		result.Output += "\nmcp: withheld OAuth-only servers from the goblin: " + strings.Join(provision.MCPDropped, ", ") + " (declare a token-authenticated form in the project .mcp.json to reach goblins)"
	}
	if provision.MCPWorktreeOccupied {
		result.Output += "\nmcp: the worktree already held a .mcp.json this spawn did not write, so it was left alone; a harness that reads its working directory sees that file, not the filtered configuration"
	}
	if provision.MCPProjectTracked && len(provision.MCPDropped) > 0 {
		// Only worth saying when something was actually withheld: if nothing
		// was dropped, a working-directory-reading harness sees exactly the
		// servers the filtered config would have given it.
		result.Output += "\nmcp: the project tracks .mcp.json, so the worktree keeps that file exactly as committed; a harness that reads its working directory sees every server declared there, including the ones the filtered config withholds"
	}
	if preflight.Warning != "" {
		result.Output += "\n" + preflight.Warning
	}
	if preflight.Refusal != "" {
		// Reached only under --yolo: the Overlord's override is recorded in
		// the output rather than silently swallowing what it overrode.
		result.Output += "\nauth: dispatched with --yolo over " + oneLine(preflight.Refusal)
	}
	return result, nil
}

// goblinMCPConfig returns the goblin MCP configuration provisioning
// materialized under the task's temporary directory, so a switch relaunch
// hands the new harness exactly what the original spawn did. It deliberately
// never looks inside the worktree: what sits at <worktree>/.mcp.json can be
// the project's own unfiltered file or one the goblin wrote, and neither may
// be promoted into --mcp-config.
func goblinMCPConfig(taskTmp string) string {
	path := filepath.Join(taskTmp, "mcp.json")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return path
}

// reservedLaunchEnv names the environment the launch contract owns. It is
// explicit rather than read off the launch map at merge time because the
// contract is written in stages: the adapter stamps GOTMPDIR and CFO_ROLE at
// build, startHarness adds CFO_STATE_OVERRIDE just before the pane line is
// rendered, and a manifest or credential merged in between must not be able
// to claim a name the launch has not written yet.
var reservedLaunchEnv = []string{"GOTMPDIR", "CFO_STATE_OVERRIDE", harness.RoleVariable}

// reservedLaunchName reports whether name belongs to the launch contract:
// one of the names the contract owns, or one the adapter already set on the
// launch. The comparison is case-insensitive because the pane is PowerShell
// on Windows, where $env:gotmpdir and $env:GOTMPDIR are the same variable,
// so a differently cased name is a collision, not a sibling.
func reservedLaunchName(env map[string]string, name string) bool {
	for _, reserved := range reservedLaunchEnv {
		if strings.EqualFold(name, reserved) {
			return true
		}
	}
	for existing := range env {
		if strings.EqualFold(name, existing) {
			return true
		}
	}
	return false
}

// mergeProvisionEnv folds provisioning's environment redirects into the
// launch. The harness environment is the launch contract, so a redirect that
// collides with a reserved name loses rather than redirecting it.
func mergeProvisionEnv(env map[string]string, redirects map[string]string) {
	for name, value := range redirects {
		if reservedLaunchName(env, name) {
			continue
		}
		env[name] = value
	}
}

// preflightCredentials resolves the project's credentials once, before the
// pane and worktree exist, so both the refusal decision and the injected
// environment come from the same probe run rather than two.
func (s Service) preflightCredentials(ctx context.Context, project string) (auth.Result, error) {
	if s.Auth == nil {
		return auth.Result{}, nil
	}
	result, err := s.Auth.Preflight(ctx, project)
	if err != nil {
		return auth.Result{}, fmt.Errorf("spawn: project auth preflight: %w", err)
	}
	return result, nil
}

// oneLine flattens a multi-line refusal for the single-line spawn output.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// injectProjectCredentials puts every usable credential from the preflight
// into the pane shell the harness inherits. The values go through a
// restricted file the shell dot-sources, never the typed line: a credential
// typed into the pane would sit in its scrollback and in every `cfo peek`.
func (s Service) injectProjectCredentials(preflight auth.Result, taskTmp string, launch *harness.Launch) error {
	if len(preflight.Env) == 0 {
		return nil
	}
	env := make(map[string]string, len(preflight.Env))
	for name, value := range preflight.Env {
		if reservedLaunchName(launch.Env, name) {
			// The harness environment is the launch contract; a project
			// manifest must not be able to redirect GOTMPDIR.
			continue
		}
		env[name] = value
	}
	if len(env) == 0 {
		return nil
	}
	script, err := harness.RenderEnvScript(env)
	if err != nil {
		return fmt.Errorf("spawn: render project credentials: %w", err)
	}
	path := filepath.Join(taskTmp, "auth.ps1")
	if err := auth.WriteSecretFile(path, script); err != nil {
		return fmt.Errorf("spawn: write project credentials: %w", err)
	}
	launch.SecretsFile = path
	return nil
}

// launchPlan is one harness start into an already-prepared Herdr pane. It is
// shared by spawn and by an in-place switch, which differ only in how the
// pane got there and what instruction the harness receives.
type launchPlan struct {
	AgentName string
	Harness   harness.Kind
	Launch    harness.Launch
}

// startHarness prepares the pane shell and starts the harness, then delivers
// the plan's instruction once it is ready. The returned submitted flag is now
// ignored by both callers: spawn tears the whole launch down through
// teardownLaunch on any error, and switch recovers the empty pane itself.
func (s Service) startHarness(ctx context.Context, client *herdr.Client, target herdr.Target, plan launchPlan) (submitted bool, err error) {
	launch := plan.Launch
	if s.StateDir != "" {
		if launch.Env == nil {
			launch.Env = map[string]string{}
		}
		launch.Env["CFO_STATE_OVERRIDE"] = s.StateDir
	}
	if launch.TypedLaunch {
		line, err := launch.PowerShellTypedLine()
		if err != nil {
			return false, fmt.Errorf("spawn: render typed harness launch: %w", err)
		}
		if err := client.SendLiteral(ctx, target, line); err != nil {
			return false, fmt.Errorf("spawn: send typed harness launch: %w", err)
		}
		if err := s.sleep(ctx, launchSettle); err != nil {
			return false, fmt.Errorf("spawn: wait before typed launch submit: %w", err)
		}
		if err := client.SendKey(ctx, target, "Enter"); err != nil {
			return false, fmt.Errorf("spawn: submit typed harness launch: %w", err)
		}
		if err := s.confirmHarnessDialogs(ctx, client, target, launch); err != nil {
			return true, err
		}
		if err := s.confirmLaunch(ctx, client, target); err != nil {
			return true, err
		}
		return true, nil
	}

	prefix, err := launch.PowerShellPrefix()
	if err != nil {
		return false, fmt.Errorf("spawn: render Windows launch prefix: %w", err)
	}
	if err := client.SendLiteral(ctx, target, prefix); err != nil {
		return false, fmt.Errorf("spawn: send launch prefix: %w", err)
	}
	if err := s.sleep(ctx, launchSettle); err != nil {
		return false, fmt.Errorf("spawn: wait before launch prefix submit: %w", err)
	}
	if err := client.SendKey(ctx, target, "Enter"); err != nil {
		return false, fmt.Errorf("spawn: submit launch prefix: %w", err)
	}
	if err := s.sleep(ctx, launchSettle); err != nil {
		return false, fmt.Errorf("spawn: wait before agent start: %w", err)
	}
	// The harness starts through Herdr's native agent facility under the gb-
	// task name, so it is a named, registered agent from birth rather than a
	// shell process Herdr happens to detect.
	if err := client.AgentStart(ctx, target, plan.AgentName, string(plan.Harness), launch.Args); err != nil {
		return false, fmt.Errorf("spawn: start native harness agent: %w", err)
	}
	if err := s.confirmHarnessDialogs(ctx, client, target, launch); err != nil {
		return true, err
	}
	if err := s.sleep(ctx, launchSettle); err != nil {
		return true, fmt.Errorf("spawn: wait before brief prompt: %w", err)
	}
	// The first instruction after launch is the least protected moment: a
	// too-early or too-fast first keystroke can eat leading characters and
	// turn the brief instruction into a bogus slash command. Type it, read it
	// back, and submit only once the composer shows it intact.
	if err := s.deliverVerifiedInstruction(ctx, client, target, plan.Harness, launch.PromptInstruction()); err != nil {
		return true, err
	}
	if err := s.confirmLaunch(ctx, client, target); err != nil {
		return true, err
	}
	return true, nil
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

func (s Service) worktreeGit() (worktree.Git, error) {
	if s.Worktrees.Git != nil {
		return s.Worktrees.Git, nil
	}
	if s.Worktrees.Commands == nil {
		return nil, errors.New("spawn: worktree Git dependency is required")
	}
	return worktree.RunnerGit{Commands: s.Worktrees.Commands, Sleep: s.Worktrees.Sleep}, nil
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
		Brief:            req.BriefPath,
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

// confirmHarnessDialogs clears any harness-declared blocking startup dialog
// (the workspace trust prompt the adapter declares) by sending the adapter's
// confirm keys while the marker text stays on screen. Herdr reports the
// dialog differently per harness (claude blocked, kimi idle), so marker
// absence in two consecutive captures is the readiness proof for every
// harness.
func (s Service) confirmHarnessDialogs(ctx context.Context, client *herdr.Client, target herdr.Target, launch harness.Launch) error {
	if len(launch.ConfirmMarkers) == 0 {
		return nil
	}
	clean := 0
	for attempt := 0; attempt < launchConfirmTries; attempt++ {
		if attempt > 0 {
			if err := s.sleep(ctx, launchConfirmPoll); err != nil {
				return fmt.Errorf("spawn: wait while confirming harness dialogs: %w", err)
			}
		}
		capture, err := client.Capture(ctx, target, 60, false)
		if err != nil {
			if herdr.WaitError(ctx, err) {
				return fmt.Errorf("spawn: confirm harness startup dialog: %w", err)
			}
			clean = 0
			continue
		}
		if containsMarker(capture, launch.ConfirmMarkers) {
			clean = 0
			for _, key := range launch.ConfirmKeys {
				if err := client.SendKey(ctx, target, key); err != nil {
					return fmt.Errorf("spawn: confirm harness startup dialog: %w", err)
				}
				if err := s.sleep(ctx, launchSettle); err != nil {
					return fmt.Errorf("spawn: wait between harness dialog keys: %w", err)
				}
			}
			continue
		}
		clean++
		if clean >= 2 {
			return nil
		}
	}
	return fmt.Errorf("spawn: harness startup dialog did not clear within %ds", int(launchConfirmPoll.Seconds()*launchConfirmTries))
}

// confirmLaunch waits for the launched harness to report working after its
// brief has been delivered through the native prompt channel or the typed
// launch line. On timeout it re-probes agent liveness: a pane that herdr does
// not report as empty - alive, or simply unreadable - is adopted rather than
// declared failed, so a false timeout cannot orphan a live goblin. An idle or
// blocked agent is a healthy Claude waiting at its prompt.
func (s Service) confirmLaunch(ctx context.Context, client *herdr.Client, target herdr.Target) error {
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
		if state == herdr.SubmitWorking {
			return nil
		}
	}
	// The full working budget elapsed without a working report. Re-probe once:
	// only a pane herdr can prove is empty is declared failed, so the spawn
	// reports success instead of writing a failed status beside a healthy pane.
	if s.paneProvablyDead(ctx, client, target) {
		return fmt.Errorf("spawn: harness launch did not report working within %ds", int(launchConfirmPoll.Seconds()*launchConfirmTries))
	}
	return nil
}

// paneProvablyDead reports whether herdr gave a trustworthy answer that the
// target pane holds no agent. It gates the destructive half of a readiness
// timeout - failing the launch runs teardownLaunch - so an unreadable probe
// counts as not-dead: herdr admitting it cannot answer is no reason to close
// the tab and return the worktree of a goblin that may be running.
func (s Service) paneProvablyDead(ctx context.Context, client *herdr.Client, target herdr.Target) bool {
	status, err := client.AgentStatus(ctx, target)
	return err == nil && (status == herdr.AgentDead || status == herdr.AgentMissing)
}

// deliverVerifiedInstruction types one instruction into the pane composer,
// reads it back, and submits it only once the read-back shows it intact. A
// mismatch (leading characters eaten by a too-early first keystroke) clears
// the composer and retries on the launch poll interval. This protects the
// first instruction after launch, which is the moment most exposed to a
// harness that is not yet focused, and the retry budget is sized for harness
// boot rather than for a focus glitch: a harness that needs a minute to come
// up must not read as a delivery failure.
func (s Service) deliverVerifiedInstruction(ctx context.Context, client *herdr.Client, target herdr.Target, kind harness.Kind, instruction string) error {
	var lastCaptureErr, lastWriteErr error
	readBack := false
	for attempt := 0; attempt < instructionTries; attempt++ {
		if attempt > 0 {
			// A mismatch almost always means the harness composer was not
			// accepting keystrokes yet: a harness takes seconds to boot,
			// far longer than launchSettle. Waiting a full poll interval
			// before retyping turns this loop into the readiness gate the
			// first instruction never had.
			if err := s.sleep(ctx, launchConfirmPoll); err != nil {
				return fmt.Errorf("spawn: wait before retyping instruction: %w", err)
			}
			if err := client.SendKey(ctx, target, "Ctrl+U"); err != nil {
				if herdr.WaitError(ctx, err) {
					return fmt.Errorf("spawn: clear composer before retyping instruction: %w", err)
				}
				lastWriteErr = err
				continue
			}
			if err := s.sleep(ctx, launchSettle); err != nil {
				return fmt.Errorf("spawn: wait after clearing composer: %w", err)
			}
		}
		if err := client.SendLiteral(ctx, target, instruction); err != nil {
			if herdr.WaitError(ctx, err) {
				return fmt.Errorf("spawn: type harness instruction: %w", err)
			}
			lastWriteErr = err
			continue
		}
		if err := s.sleep(ctx, launchSettle); err != nil {
			return fmt.Errorf("spawn: wait before instruction read-back: %w", err)
		}
		captured, err := client.Capture(ctx, target, 0, false)
		if err != nil {
			// A pane that is momentarily unreadable is part of booting, not
			// a delivery failure: spending an attempt keeps the boot budget
			// intact where returning would tear down a live launch.
			if herdr.WaitError(ctx, err) {
				return fmt.Errorf("spawn: read back harness instruction: %w", err)
			}
			lastCaptureErr = err
			continue
		}
		readBack = true
		if instructionIntact(captured, instruction) {
			if err := client.SendKey(ctx, target, submitKey(kind)); err != nil {
				return fmt.Errorf("spawn: submit harness instruction: %w", err)
			}
			return nil
		}
	}
	// Claiming a bare mismatch when herdr refused attempts buries the real
	// cause - the herdr stderr - in a durable `failed:` status, so the
	// retained transient error is reported whenever one exists.
	budget := int(launchConfirmPoll.Seconds() * instructionTries)
	if !readBack {
		if lastCaptureErr != nil {
			return fmt.Errorf("spawn: could not read the pane to verify the instruction within %ds: %w", budget, lastCaptureErr)
		}
		if lastWriteErr != nil {
			return fmt.Errorf("spawn: could not type the instruction into the pane within %ds: %w", budget, lastWriteErr)
		}
		return fmt.Errorf("spawn: instruction read-back did not match within %ds", budget)
	}
	if lastCaptureErr != nil {
		return fmt.Errorf("spawn: instruction read-back did not match within %ds; later pane reads were refused: %w", budget, lastCaptureErr)
	}
	if lastWriteErr != nil {
		return fmt.Errorf("spawn: instruction read-back did not match within %ds; later pane writes were refused: %w", budget, lastWriteErr)
	}
	return fmt.Errorf("spawn: instruction read-back did not match within %ds", budget)
}

// instructionIntact reports whether instruction survived typing intact in the
// captured pane tail. Whitespace is stripped from both sides so composer line
// wrapping never reads as corruption.
func instructionIntact(captured, instruction string) bool {
	return strings.Contains(stripWhitespace(captured), stripWhitespace(instruction))
}

// submitKey is the harness-specific key that submits a parked composer: kimi
// needs ctrl+s while every other harness submits with Enter.
func submitKey(kind harness.Kind) string {
	if kind == harness.Kimi {
		return "ctrl+s"
	}
	return "Enter"
}

// teardownLaunch closes the task tab, returns the worktree, and retires the
// task metadata. It is the clean-failure path: every step is attempted and
// their failures joined, so one stuck teardown step never leaves the rest
// undone.
func (s Service) teardownLaunch(ctx context.Context, client *herdr.Client, endpoint herdr.Endpoint, project, worktree, id string) error {
	var errs error
	if err := client.CloseTab(ctx, endpoint.Target.Session, endpoint.TabID); err != nil {
		errs = errors.Join(errs, fmt.Errorf("spawn: close task tab: %w", err))
	}
	if err := s.Worktrees.Return(ctx, project, worktree); err != nil {
		errs = errors.Join(errs, fmt.Errorf("spawn: return task worktree: %w", err))
	}
	if err := os.Remove(filepath.Join(s.StateDir, id+".meta")); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = errors.Join(errs, fmt.Errorf("spawn: retire task metadata: %w", err))
	}
	return errs
}

// ensureProjectSeeded makes an unborn or empty primary project workable before
// the first worktree acquisition, so a freshly created empty GitHub repo is
// seeded with an initial commit instead of having no remote branch to base a
// worktree on.
func (s Service) ensureProjectSeeded(ctx context.Context, project string) error {
	git, err := s.worktreeGit()
	if err != nil {
		return err
	}
	if _, err := git.EnsureSeeded(ctx, project); err != nil {
		return fmt.Errorf("spawn: prepare project for worktree acquisition: %w", err)
	}
	return nil
}

// spawnInstruction is the full first instruction a goblin receives: read the
// brief, then report outcomes through cfo notify so the CFO is woken with the
// real payload rather than a pane-derived guess.
func spawnInstruction(briefPath, id string) string {
	return harness.BriefInstruction(briefPath) + notifyInstruction(id)
}

// notifyInstruction tells a goblin how to report its outcome through cfo
// notify, so the CFO is woken with the actual payload instead of the watcher
// guessing from pane text.
func notifyInstruction(id string) string {
	exe, err := os.Executable()
	if err != nil {
		exe = "cfo"
	}
	return " Report outcomes to the CFO: on completion with a PR run: " + exe + " notify " + id + " --done --pr <url>. When blocked on a decision run: " + exe + " notify " + id + " --blocked \"<question>\". On failure run: " + exe + " notify " + id + " --failed \"<reason>\"."
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
	live := map[string]bool{}
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".meta") {
			live[strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))] = true
		}
	}
	for _, entry := range entries {
		extension := filepath.Ext(entry.Name())
		if entry.IsDir() || (!strings.EqualFold(extension, ".meta") && !strings.EqualFold(extension, ".status")) {
			continue
		}
		existingID := strings.TrimSuffix(entry.Name(), extension)
		// A status log whose task has no metadata is the history of a task
		// that finished and was cleaned up. It is kept for the record, not as
		// a claim on the id: refusing the id for it is what forced a cleaned-up
		// task to be respawned under an invented suffix.
		if strings.EqualFold(extension, ".status") && !live[existingID] {
			continue
		}
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
