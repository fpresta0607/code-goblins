// Package spawn creates one local task in a native terminal of its own and
// publishes its durable identity as soon as its worktree exists, before the
// harness is confirmed working, so a failed launch stays addressable and
// cleanable.
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
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

const (
	spawnLockName = ".spawn.lock"
	launchSettle  = 300 * time.Millisecond
	// submitRetries is how many more times a typed brief still in its
	// composer is submitted after the first Enter.
	submitRetries = 3
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
	Class     string
	// Title is the task's short title, given with --title or taken from its
	// backlog row, kept on the task so the board names it once the row leaves
	// the queue.
	Title string
	// Parent, for a helper goblin, is the task that asked for it: the helper
	// works on a branch cut from the parent's last commit and reports to it.
	Parent string
	// Capsule, when set, writes the task capsule into the task temporary
	// directory and returns the brief the goblin reads instead of BriefPath.
	// It runs only once the id is proven free, because the alias check
	// refuses any existing directory of the id, this spawn's own included.
	Capsule func(taskTmp string) (briefPath string, err error)
}

// Result contains the exact published task identity and user-facing outcome.
type Result struct {
	Meta   state.TaskMeta
	Output string
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

// Service owns one local spawn. Its collaborators are injected through
// their established package seams so operation ordering remains deterministic.
type Service struct {
	Worktrees worktree.Service
	Harness   harness.Registry
	Auth      AuthPreflight
	Commands  execx.Runner
	StateDir  string
	// ScratchRoot is the home's scratch folder. A task's scratch folder,
	// which its pane's TEMP, TMP and GOTMPDIR name, is <ScratchRoot>\<id>.
	ScratchRoot string
	Project     string
	Sleep       func(context.Context, time.Duration) error
	ReleaseLock func(string, string) error
	PolicyPath  string
	Admit       func() error
	// UserEnvironment is the environment a native task starts from: the
	// variables Windows gives a new process of this user, never this
	// process's own. Nil reads them from the user's and the machine's
	// configuration.
	UserEnvironment func() ([]string, error)
	// HostCommand runs a native terminal's host: cfo.exe and "host" in
	// production.
	HostCommand []string
	// ReadScreen reads a native terminal's screen. Nil reads it through its
	// host.
	ReadScreen func(host.Record) ([]string, error)
	// PromptSince reports whether a native task's harness, in a spawn
	// generation, reported through its native hooks taking a prompt at or
	// after a time: supervisor.NativePromptSince in production. Nil proves a
	// delivery by the screen alone.
	PromptSince func(taskID, generation string, since time.Time) (bool, error)
	// Took reports whether a native task's harness handed text to its model
	// at or after a time, as the harness's own record of its conversation
	// shows: a monitor.HostProgress reading it in production. It is the one
	// proof that a harness in a turn has text typed into it, since a harness
	// queues such text and hands it over at its next tool call. Nil proves
	// nothing.
	Took func(ctx context.Context, meta state.TaskMeta, text string, since time.Time) bool
}

// Spawn creates and launches exactly one local ship or scout task.
func (s Service) Spawn(ctx context.Context, req Request) (result Result, err error) {
	if err := state.ValidTaskID(req.ID); err != nil {
		return Result{}, err
	}
	if err := validateRequestLineValues(req); err != nil {
		return Result{}, err
	}
	if req.Model == "" {
		req.Model = harness.DefaultModel(req.Harness)
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
	if err := validateHelperRequest(req); err != nil {
		return Result{}, err
	}
	var selection *pipeline.Selection
	if req.Class == "" {
		req.Class = "ordinary"
	}
	if !pipeline.ValidClass(req.Class) {
		return Result{}, errors.New("spawn: class must be ordinary, high-risk, or mechanical")
	}
	if req.Mode == "no-mistakes" && s.PolicyPath != "" {
		policy, err := pipeline.Load(s.PolicyPath)
		if err != nil {
			return Result{}, err
		}
		chosen, err := policy.Select(req.Class)
		if err != nil {
			return Result{}, err
		}
		selection = &chosen
	}
	adapter, err := s.Harness.Get(req.Harness)
	if err != nil {
		return Result{}, err
	}
	// A spawn never types into a screen it cannot read.
	if _, ok := harness.NativeScreens(req.Harness); !ok {
		return Result{}, fmt.Errorf("spawn: %s cannot run in a native terminal yet", req.Harness)
	}
	taskTmp := filepath.Join(s.StateDir, "tasktmp", req.ID)
	if err := validateLineValues("project", project, "tasktmp", taskTmp); err != nil {
		return Result{}, err
	}

	if err := os.MkdirAll(s.StateDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("spawn: create state directory: %w", err)
	}
	// The spawn lock covers the whole dispatch, not just worktree acquisition:
	// task id alias rejection, metadata publication and the harness launch all
	// mutate shared fleet state under it. Dependency provisioning (about 5s pnpm, about 22s
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
	if s.Admit != nil {
		if err := s.Admit(); err != nil {
			return Result{}, err
		}
	}
	var helper helperBase
	if req.Parent != "" {
		if helper, err = s.helperStart(ctx, req, project); err != nil {
			return Result{}, err
		}
	}
	if req.Capsule != nil {
		// A spawn that fails before the task is published has no teardown, so
		// the capsule goes with it rather than claiming the id for a retry.
		defer func() {
			if err == nil {
				return
			}
			if removeErr := os.RemoveAll(taskTmp); removeErr != nil {
				err = errors.Join(err, fmt.Errorf("spawn: remove task temporary directory: %w", removeErr))
			}
		}()
		if req.BriefPath, err = req.Capsule(taskTmp); err != nil {
			return Result{}, err
		}
	}
	if err := s.ensureProjectSeeded(ctx, project); err != nil {
		return Result{}, err
	}

	// The preflight runs before anything is built, so a goblin that would
	// start without a credential it needs costs no terminal and no worktree.
	// Dispatching anyway is what let a stale DATABASE_URL reach a goblin, so
	// a red blocking service stops here; --yolo is the existing override.
	preflight, err := s.preflightCredentials(ctx, project)
	if err != nil {
		return Result{}, err
	}
	if preflight.Refusal != "" && !req.Yolo {
		return Result{}, fmt.Errorf("spawn: %s", preflight.Refusal)
	}
	// A goblin starts from the user's environment, never this process's own.
	userEnv, err := s.userEnvironment()
	if err != nil {
		return Result{}, fmt.Errorf("spawn: read the user's environment for a native task: %w", err)
	}

	scratch, err := s.scratch(req.ID)
	if err != nil {
		return Result{}, err
	}
	wt, err := s.Worktrees.Acquire(ctx, project, req.ID, helper.head)
	if err != nil {
		return Result{}, fmt.Errorf("spawn: acquire task worktree: %w", err)
	}
	result = partialResult(req, project, taskTmp, wt.Path, scratch)

	// Publish metadata as soon as the worktree exists, before the harness can
	// start: a task whose launch later fails is then addressable and cleanable
	// through `cfo peek`/`cfo cleanup` instead of an unnameable orphan the CFO
	// has to hunt down by hand.
	result.Meta.SpawnGen = fmt.Sprintf("s%d", time.Now().UTC().UnixNano())
	if selection != nil {
		result.Meta.PipelineClass = selection.Class
		result.Meta.PipelineHash = selection.Hash
	}
	// nativeHost is the host this spawn launched, the only native terminal its
	// teardown may close.
	var nativeHost host.Record
	if err := state.WriteTaskMeta(s.StateDir, result.Meta); err != nil {
		return Result{}, errors.Join(
			fmt.Errorf("spawn: publish task metadata: %w", err),
			s.teardownLaunch(ctx, nativeHost, project, wt.Path, scratch, result.Meta.ID),
		)
	}

	// fail records the exact cause and tears the half-built task down cleanly
	// (close its terminal, return the worktree, retire the metadata). It is the
	// one failure path: nothing here can leave an unaddressable terminal behind.
	fail := func(result Result, cause error) (Result, error) {
		line := "failed: " + bounded(state.NormalizeStatusDetail(cause.Error()), 1000)
		if err := state.AppendStatus(s.StateDir, result.Meta.ID, line); err != nil {
			cause = errors.Join(cause, fmt.Errorf("spawn: record launch failure: %w", err))
		}
		if err := s.teardownLaunch(ctx, nativeHost, project, wt.Path, scratch, result.Meta.ID); err != nil {
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
	if helper.branch != "" {
		switched, err := s.commands().Run(ctx, execx.Request{Dir: wt.Path, Name: "git", Args: []string{"switch", "--quiet", "--create", helper.branch}})
		if err == nil && switched.ExitCode != 0 {
			err = errors.New(strings.TrimSpace(string(switched.Stderr)))
		}
		if err != nil {
			return fail(result, fmt.Errorf("spawn: cut the helper's branch %s from %s: %w", helper.branch, helper.parentBranch, err))
		}
	}

	if err := adapter.Validate(ctx, s.commands()); err != nil {
		return fail(result, fmt.Errorf("spawn: validate harness %s: %w", req.Harness, err))
	}
	if err := os.MkdirAll(taskTmp, 0o755); err != nil {
		return fail(result, fmt.Errorf("spawn: create task temporary directory: %w", err))
	}
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return fail(result, fmt.Errorf("spawn: create the task's scratch folder: %w", err))
	}
	if selection != nil {
		if err := selection.Save(filepath.Join(taskTmp, "pipeline.json")); err != nil {
			return fail(result, err)
		}
	}
	// The worktree starts as tracked files only; provisioning is what makes it
	// runnable as if it were the project - shared config, dependencies
	// installed against the shared package cache, and the token-authenticated
	// subset of the project's MCP servers. A server that authenticates by
	// bearerTokenEnvVar reaches the goblin only when the environment its
	// terminal's host is built with sets that variable. No harness billing key
	// ever reaches a goblin, whatever its source.
	hasVariable := func(name string) bool {
		if auth.IsHarnessBillingKey(name) {
			return false
		}
		return hasNativeVariable(s.nativeHostEnvironment(userEnv, harness.Launch{}, preflight.Env), name)
	}
	provision, err := s.Worktrees.Provision(ctx, project, wt.Path, taskTmp, preflight.Caches, hasVariable)
	if err != nil {
		return fail(result, fmt.Errorf("spawn: provision worktree environment: %w", err))
	}
	codexServers, err := codexMCPServers(req.Harness)
	if err != nil {
		return fail(result, fmt.Errorf("spawn: %w", err))
	}
	launch, err := adapter.Build(harness.LaunchSpec{
		BriefPath:       req.BriefPath,
		TaskTmp:         taskTmp,
		Scratch:         scratch,
		Model:           req.Model,
		Effort:          req.Effort,
		MCPConfig:       provision.MCPConfig,
		CodexMCPServers: codexServers,
	})
	if err != nil {
		return fail(result, fmt.Errorf("spawn: build harness launch: %w", err))
	}
	launch.Dir = wt.Path
	mergeProvisionEnv(launch.Env, provision.Env)
	// The shared package caches fill in behind the project's own redirects,
	// so a project that names a cache location keeps it and every other one
	// builds against the fleet's single store. They are not secrets, so they
	// ride the launch environment rather than the restricted credentials file.
	mergeProvisionEnv(launch.Env, preflight.Caches)
	nativeEnvironment(launch.Env, result.Meta)
	// Every goblin is told to report its outcome through cfo notify, so the
	// CFO is woken with the actual PR URL, question, or failure reason instead
	// of the watcher guessing from its screen.
	launch.Instruction = spawnInstruction(req.BriefPath, result.Meta)
	if selection != nil {
		launch.Instruction += selection.Instruction(req.ID, filepath.Join(taskTmp, "pipeline.json"))
	}
	if nativeHost, err = s.startNativeHarness(ctx, req.ID, req.Harness, launch, userEnv, preflight.Env); err != nil {
		return fail(result, err)
	}

	result.Output = successOutput(result.Meta)
	if notice := containedNotice(nativeHost); notice != "" {
		result.Output += "\n" + notice
	}
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
	if len(provision.MCPTokenUnset) > 0 {
		result.Output += "\nmcp: withheld servers whose token variable is not set for the goblin: " + strings.Join(provision.MCPTokenUnset, ", ") + " (declare the variable as a project credential to reach goblins)"
	}
	if provision.MCPWorktreeOccupied {
		result.Output += "\nmcp: the worktree already held a .mcp.json this spawn did not write, so it was left alone; a harness that reads its working directory sees that file, not the filtered configuration"
	}
	if provision.MCPProjectTracked && len(provision.MCPDropped)+len(provision.MCPTokenUnset) > 0 {
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

// codexMCPServers names the operator's Codex MCP servers a Codex goblin turns
// off, and refuses one it cannot turn off; any other harness has none to turn
// off.
func codexMCPServers(kind harness.Kind) ([]string, error) {
	if kind != harness.Codex {
		return nil, nil
	}
	servers, err := harness.CodexMCPServers()
	if err != nil {
		return nil, err
	}
	return servers, harness.CheckCodexMCPServers(servers)
}

// reservedLaunchEnv names the environment the launch contract owns. It is
// explicit rather than read off the launch map at merge time because the
// contract is written in stages: the adapter stamps GOTMPDIR and CFO_ROLE at
// build, nativeHostEnvironment adds CFO_STATE_OVERRIDE when it builds the
// host's environment, and a manifest or credential merged in between must not
// be able to claim a name the launch has not written yet.
//
// The cache root belongs to the contract for the same reason: the task's Go
// temporary directory is derived from os.UserCacheDir, which reads
// LOCALAPPDATA on Windows, XDG_CACHE_HOME on Linux and HOME whenever that is
// unset, and HOME alone on darwin. All three are reserved because a manifest
// that redirected any of them would leave any cfo command run from that terminal
// computing a different directory than the process that created it.
var reservedLaunchEnv = []string{"GOTMPDIR", "TEMP", "TMP", "CFO_STATE_OVERRIDE", "LOCALAPPDATA", "XDG_CACHE_HOME", "HOME", harness.RoleVariable}

// reservedLaunchName reports whether name belongs to the launch contract:
// one of the names the contract owns, or one the adapter already set on the
// launch. The comparison is case-insensitive because Windows compares
// environment names without case, so gotmpdir and GOTMPDIR are the same
// variable and a differently cased name is a collision, not a sibling.
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
// terminal and worktree exist, so both the refusal decision and the injected
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

// validateHelperRequest refuses a helper that could push or open a pull
// request: its parent does both, so a helper is a ship task in local-only
// mode.
func validateHelperRequest(req Request) error {
	if req.Parent == "" {
		return nil
	}
	if err := state.ValidTaskID(req.Parent); err != nil {
		return fmt.Errorf("spawn: parent: %w", err)
	}
	if req.Kind != "ship" || req.Mode != "local-only" {
		return errors.New("spawn: a helper is a ship task in local-only mode: its parent pushes and opens the pull request")
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
	data, err := fsx.ReadFile(req.BriefPath)
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

func validateRequestLineValues(req Request) error {
	return validateLineValues(
		"request project", req.Project,
		"request brief", req.BriefPath,
		"request kind", req.Kind,
		"request mode", req.Mode,
		"request harness", string(req.Harness),
		"request model", req.Model,
		"request effort", req.Effort,
		"request title", req.Title,
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

// partialResult is the task's identity before its harness starts: its
// terminal is the native host named by its id.
func partialResult(req Request, project, taskTmp, worktree, scratch string) Result {
	meta := state.TaskMeta{
		Scratch:        scratch,
		ID:             req.ID,
		Window:         "native",
		EndpointTaskID: req.ID,
		Worktree:       worktree,
		Project:        project,
		Harness:        string(req.Harness),
		Kind:           req.Kind,
		TaskTmp:        taskTmp,
		Brief:          req.BriefPath,
		Model:          valueOrDefault(req.Model),
		Effort:         valueOrDefault(req.Effort),
		Backend:        "native",
		Title:          req.Title,
		Parent:         req.Parent,
	}
	if req.Kind == "ship" {
		meta.Mode = req.Mode
		meta.Yolo = yoloString(req.Yolo)
	}
	return Result{Meta: meta}
}

// teardownLaunch closes the task's terminal, returns the worktree, removes the
// task's scratch folder and its task temporary directory, and retires the
// task metadata. It is the clean-failure path: every step after the close is
// attempted and their failures joined, so one stuck teardown step never leaves
// the rest undone. It closes only nativeHost, the host its spawn launched, and
// a terminal that does not close stops the teardown: the task stays
// addressable, and nothing is removed from under a harness that may still run.
func (s Service) teardownLaunch(ctx context.Context, nativeHost host.Record, project, worktree, scratch, id string) error {
	if err := host.Close(s.StateDir, nativeHost, nativeCloseWait); err != nil {
		return fmt.Errorf("spawn: close native terminal: %w; its worktree, temporary directories and task record are left in place", err)
	}
	var errs error
	if err := s.Worktrees.Return(ctx, project, worktree); err != nil {
		errs = errors.Join(errs, fmt.Errorf("spawn: return task worktree: %w", err))
	}
	// Removing the scratch folder belongs to this teardown rather than to a
	// later cleanup: cleanup reads <id>.meta to find a task at all, so once
	// the metadata is retired only the janitor's sweep of folders no task
	// owns would find it.
	if err := os.RemoveAll(scratch); err != nil {
		errs = errors.Join(errs, fmt.Errorf("spawn: remove the task's scratch folder: %w", err))
	}
	// The task temporary directory goes for the same reason as the Go one, and
	// with the same urgency: cleanup finds a task through <id>.meta, so once
	// the metadata is retired nothing can remove this directory again. Left
	// behind, it refuses the retry of the very spawn that just failed with
	// "conflicts case-insensitively with retained task temporary directory".
	// It also holds the rendered credential script, so removing it here is the
	// credential scrub for a launch that never became a task.
	if err := os.RemoveAll(filepath.Join(s.StateDir, "tasktmp", id)); err != nil {
		errs = errors.Join(errs, fmt.Errorf("spawn: remove task temporary directory: %w", err))
	}
	if err := os.Remove(filepath.Join(s.StateDir, id+".meta")); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = errors.Join(errs, fmt.Errorf("spawn: retire task metadata: %w", err))
	}
	return errs
}

// scratch is the task's scratch folder under the home, refused before anything
// is built when the home names none.
func (s Service) scratch(id string) (string, error) {
	if strings.TrimSpace(s.ScratchRoot) == "" || !filepath.IsAbs(s.ScratchRoot) {
		return "", fmt.Errorf("spawn: the home's scratch folder %q is not an absolute path", s.ScratchRoot)
	}
	return filepath.Join(s.ScratchRoot, id), nil
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
// real payload rather than a guess from its screen.
func spawnInstruction(briefPath string, meta state.TaskMeta) string {
	return harness.BriefInstruction(briefPath) + notifyInstruction(meta)
}

// notifyInstruction tells a goblin how to report its outcome through cfo
// notify, so the CFO is woken with the actual payload instead of the watcher
// guessing from its screen. A helper reports to its parent instead, and a
// ship goblin is told it may ask for a helper.
func notifyInstruction(meta state.TaskMeta) string {
	exe, err := os.Executable()
	if err != nil {
		exe = "cfo"
	}
	if meta.Parent != "" {
		return helperInstruction(exe, meta)
	}
	id := meta.ID
	offer := ""
	if meta.Kind == "ship" {
		offer = helperOffer(exe, id)
	}
	return " Report outcomes to the CFO: on completion with a PR run: " + exe + " notify " + id + " --done --pr <url>. When blocked on a decision run: " + exe + " notify " + id + " --blocked \"<question>\"; the CFO reads it as body text, so lead with one short sentence that is the actual question, put the details on lines of their own that start with \"- \" (a real line break, such as `n in PowerShell), and mark with **two asterisks** only the verdict or the blocking item, never the whole question; when the question has a fixed set of choices, name them after one literal options: marker separated by |, as in \"<question> options: Fix it next (Recommended) | Keep 300 s\", ending the choice you recommend with (Recommended); each choice is the answer itself as a short phrase, never a bare letter or number like a, b or 2, which notify refuses, and details stay in the \"- \" lines. cfo drain renders those as the decision's options; the CFO answers it, and his answer arrives here as a message. Never wait on the CFO for a choice you can undo: take the better option, say which with --working, and keep going; a choice you cannot undo or make yourself is a question for --blocked, never one asked in your reply. On failure run: " + exe + " notify " + id + " --failed \"<reason>\". To say you are back at work or what you are doing run: " + exe + " notify " + id + " --working \"<what>\"; when you wait on another task, CI, a deploy or the Overlord personally (his sign-in, his click, his page) instead of asking a question run: " + exe + " notify " + id + " --waiting-on <task-id|overlord|ci|deploy|memory> \"<why>\"; a choice the CFO can make, such as whether to start something now or later, is a question, not a wait on the Overlord: ask it with --blocked and options." +
		" When the Overlord must answer on a Scrawl page (his review page; call it Scrawl when you name it to him), open it with lavish-axi <html-file> --no-open, then run: " + exe + " notify " + id + " --waiting-on overlord \"<why>\" --lavish <html-file>, and never run lavish-axi poll yourself: the supervisor polls the page, and his answer reaches you through the CFO." +
		" When the page asks him to pick, declare its choices in it with a <script type=\"application/json\" data-lavish-choices> block as the lavish skill shows, each option the answer itself as a short phrase: the page draws them as a radio list and his pick reaches you as the option's exact text; a page without it shows him no choices." +
		" When the Overlord must run a command himself, such as a sign-in, never paste it into your words: write it to a .ps1 file and run: " + exe + " notify " + id + " --waiting-on overlord \"<why>\" --run <command.ps1>; his card shows the exact command and runs it with one click in a window he can use, and you are told how it ended." +
		" For a successful browser walkthrough or a Scrawl presentation that needs no answer, use lavish-axi --no-open and report its safe URL with cfo present --id <stable-id> --task " + id + " --generation <CFO_SPAWN_GEN> --kind browser|review --url <safe-url>. Refresh only while live and report --state ended when finished. A viewing choice never pauses your work. See docs/native-board.md; do not publish secrets, query parameters or browser history." + offer
}

func (s Service) releaseTaskLock(dir, name string) error {
	if s.ReleaseLock != nil {
		return s.ReleaseLock(dir, name)
	}
	return lock.ReleaseExclusiveNamed(dir, name)
}

// rejectTaskIDAlias prevents Windows task-state collisions before a spawn can
// create any terminal or worktree resources. Task IDs remain case-preserving in
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
