package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

const (
	defaultSession = "default"
	serverPolls    = 20
	serverPoll     = 500 * time.Millisecond
	captureFloor   = 200
)

// Client executes every Herdr operation through an injected subprocess runner.
type Client struct {
	Commands execx.Runner
	Session  string
	Sleep    func(context.Context, time.Duration) error
}

type requestError struct {
	message string
}

func (e *requestError) Error() string {
	return e.message
}

type runnerError struct {
	err error
}

func (e *runnerError) Error() string {
	return e.err.Error()
}

func (e *runnerError) Unwrap() error {
	return e.err
}

// EnsureServer starts the selected Herdr server when absent, then confirms its
// JSON-reported running state within the fixed ten-second startup budget.
func (c *Client) EnsureServer(ctx context.Context) error {
	session := c.session()
	running, err := c.serverRunning(ctx, session)
	if err != nil {
		return err
	}
	if running {
		return nil
	}
	if err := c.start(ctx, session, "server"); err != nil {
		return fmt.Errorf("herdr: start server for session %q: %w", session, err)
	}
	for range serverPolls {
		if err := c.sleep(ctx, serverPoll); err != nil {
			return fmt.Errorf("herdr: wait for server %q: %w", session, err)
		}
		running, err := c.serverRunning(ctx, session)
		if err != nil {
			return err
		}
		if running {
			return nil
		}
	}
	return fmt.Errorf("herdr: server for session %q did not report running within 10s", session)
}

// fleetSpaceLabel is the one Herdr workspace every goblin tab lives in: the
// CFO space. One space holds the CFO session plus all running goblins; there
// is no separate fleet workspace.
const fleetSpaceLabel = "cfo"

// EnsureContainer returns the one flat cfo workspace: it adopts an existing
// cfo-labeled workspace, adopts the factory-default workspace ("~" holding one
// tab "1") by renaming both to cfo, or creates the workspace when neither
// shape is present. Only the create path retains the exact default tab from
// its response as the seeded default.
func (c *Client) EnsureContainer(ctx context.Context, cwd string) (Container, error) {
	session := c.session()
	workspaces, err := c.workspaces(ctx, session)
	if err != nil {
		return Container{}, err
	}

	var matches []workspaceRecord
	for _, workspace := range workspaces {
		if workspace.Label == fleetSpaceLabel {
			if workspace.ID == "" {
				return Container{}, errors.New("herdr: matching cfo workspace has no workspace_id")
			}
			matches = append(matches, workspace)
		}
	}
	switch len(matches) {
	case 1:
		return Container{Session: session, WorkspaceID: matches[0].ID}, nil
	case 0:
		// Adopt the factory state, or create below.
	default:
		return Container{}, fmt.Errorf("herdr: %d workspaces in session %q are labeled %s", len(matches), session, fleetSpaceLabel)
	}

	if container, adopted, err := c.adoptFactoryWorkspace(ctx, session, workspaces); err != nil || adopted {
		return container, err
	}

	result, err := c.required(ctx, session, Target{}, "workspace create", "workspace", "create", "--cwd", cwd, "--label", fleetSpaceLabel, "--no-focus")
	if err != nil {
		return Container{}, err
	}
	var create struct {
		Workspace struct {
			ID string `json:"workspace_id"`
		} `json:"workspace"`
		Tab struct {
			ID string `json:"tab_id"`
		} `json:"tab"`
	}
	if err := decodeResult(result.Stdout, &create); err != nil {
		return Container{}, fmt.Errorf("herdr: decode workspace create response: %w", err)
	}
	if create.Workspace.ID == "" || create.Tab.ID == "" {
		return Container{}, errors.New("herdr: workspace create response is missing workspace_id or seeded tab_id")
	}
	return Container{Session: session, WorkspaceID: create.Workspace.ID, SeededDefaultTab: create.Tab.ID}, nil
}

// adoptFactoryWorkspace renames the factory-default Herdr workspace ("~"
// holding one tab "1") to the fleet label and adopts it, so a brand-new
// install opens into the cfo space instead of growing a parallel one. The
// adopted tab is deliberately not retained as the seeded default: on a live
// install that tab may carry the operator's own CFO session, and the seeded
// default is the one pane CreateTask is allowed to prune.
func (c *Client) adoptFactoryWorkspace(ctx context.Context, session string, workspaces []workspaceRecord) (Container, bool, error) {
	if len(workspaces) != 1 || workspaces[0].Label != "~" || workspaces[0].ID == "" {
		return Container{}, false, nil
	}
	tabs, err := c.tabs(ctx, session, workspaces[0].ID)
	if err != nil {
		return Container{}, false, err
	}
	if len(tabs) != 1 || tabs[0].ID == "" || (tabs[0].Label != "1" && tabs[0].Label != fleetSpaceLabel) {
		return Container{}, false, nil
	}
	if tabs[0].Label == "1" {
		if _, err := c.required(ctx, session, Target{}, "tab rename", "tab", "rename", tabs[0].ID, fleetSpaceLabel); err != nil {
			return Container{}, false, err
		}
	}
	if _, err := c.required(ctx, session, Target{}, "workspace rename", "workspace", "rename", workspaces[0].ID, fleetSpaceLabel); err != nil {
		return Container{}, false, err
	}
	return Container{Session: session, WorkspaceID: workspaces[0].ID}, true, nil
}

// CreateTask creates one labeled tab in container. A duplicate is replaceable
// only when its pane is structurally missing or has no registered agent.
func (c *Client) CreateTask(ctx context.Context, container Container, label, cwd string) (Endpoint, error) {
	if container.Session == "" || container.WorkspaceID == "" {
		return Endpoint{}, errors.New("herdr: task container requires session and workspace_id")
	}
	if !strings.HasPrefix(label, "gb-") || len(label) == len("gb-") {
		return Endpoint{}, fmt.Errorf("herdr: task tab label %q must be gb-<id>", label)
	}

	tabs, err := c.tabs(ctx, container.Session, container.WorkspaceID)
	if err != nil {
		return Endpoint{}, err
	}
	husks := make([]string, 0)
	for _, tab := range tabs {
		if tab.Label != label {
			continue
		}
		if tab.ID == "" {
			return Endpoint{}, fmt.Errorf("herdr: existing tab %q has no tab_id", label)
		}
		husk, err := c.isHusk(ctx, container.Session, container.WorkspaceID, tab.ID)
		if err != nil {
			return Endpoint{}, err
		}
		if !husk {
			return Endpoint{}, fmt.Errorf("herdr: tab %q already exists in workspace %s (session %s)", label, container.WorkspaceID, container.Session)
		}
		husks = append(husks, tab.ID)
	}
	for _, husk := range husks {
		if err := c.close(ctx, container.Session, "tab", husk); err != nil {
			return Endpoint{}, err
		}
	}
	if len(husks) > 0 {
		if err := c.verifyHusksRemoved(ctx, container.Session, container.WorkspaceID, label); err != nil {
			return Endpoint{}, err
		}
	}

	result, err := c.required(ctx, container.Session, Target{}, "tab create", "tab", "create", "--workspace", container.WorkspaceID, "--cwd", cwd, "--label", label, "--no-focus")
	if err != nil {
		return Endpoint{}, err
	}
	var create struct {
		Tab struct {
			ID string `json:"tab_id"`
		} `json:"tab"`
		RootPane struct {
			ID string `json:"pane_id"`
		} `json:"root_pane"`
	}
	if err := decodeResult(result.Stdout, &create); err != nil {
		return Endpoint{}, fmt.Errorf("herdr: decode tab create response: %w", err)
	}
	if create.Tab.ID == "" || create.RootPane.ID == "" {
		return Endpoint{}, errors.New("herdr: tab create response is missing tab_id or root pane_id")
	}

	c.pruneSeededDefault(ctx, container)

	target := Target{Session: container.Session, Pane: create.RootPane.ID}
	return Endpoint{Target: target, WorkspaceID: container.WorkspaceID, TabID: create.Tab.ID, PaneID: create.RootPane.ID}, nil
}

// SendLiteral types unsubmitted literal text into the target pane.
func (c *Client) SendLiteral(ctx context.Context, target Target, text string) error {
	_, err := c.required(ctx, target.Session, target, "pane send-text", "pane", "send-text", target.Pane, text)
	return err
}

// SendKey sends one normalized named key to the target pane.
func (c *Client) SendKey(ctx context.Context, target Target, key string) error {
	_, err := c.required(ctx, target.Session, target, "pane send-keys", "pane", "send-keys", target.Pane, normalizeKey(key))
	return err
}

// agentStartTimeoutMs gives a harness two minutes to boot on a loaded host;
// Herdr's 30s default is too tight for a cold pi or kimi start while test
// suites are running.
const agentStartTimeoutMs = "120000"

// AgentStart natively starts a supported interactive agent in the pane under
// the fleet's explicit name, so the agent is registered with Herdr from birth
// rather than detected after the fact. Herdr waits for interactive readiness
// and fails the command when the agent does not come up; harness args ride
// after `--`. The pane must be at its interactive shell prompt.
func (c *Client) AgentStart(ctx context.Context, target Target, name, kind string, args []string) error {
	if err := validateTarget(target); err != nil {
		return err
	}
	if name == "" {
		return &requestError{message: "herdr: agent name is required"}
	}
	if kind == "" {
		return &requestError{message: "herdr: agent kind is required"}
	}
	argv := []string{"agent", "start", name, "--kind", kind, "--pane", target.Pane, "--timeout", agentStartTimeoutMs}
	if len(args) > 0 {
		argv = append(argv, "--")
		argv = append(argv, args...)
	}
	_, err := c.required(ctx, target.Session, target, "agent start", argv...)
	return err
}

// AgentPrompt submits text to the registered agent through Herdr's native
// channel rather than a typed shell line, so the text never crosses
// PowerShell quoting.
func (c *Client) AgentPrompt(ctx context.Context, target Target, text string) error {
	if err := validateTarget(target); err != nil {
		return err
	}
	if text == "" {
		return &requestError{message: "herdr: agent prompt text is required"}
	}
	_, err := c.required(ctx, target.Session, target, "agent prompt", "agent", "prompt", target.Pane, text)
	return err
}

// ReportSource identifies CFO as the authority behind a reported agent, so a
// pane CFO registered is distinguishable from one Herdr detected itself.
const ReportSource = "cfo"

// ReportAgent tells Herdr which harness a pane is running, what that harness
// is doing, and which goblin session it belongs to.
//
// Herdr otherwise infers all three by matching pane output against a
// per-harness detection manifest. A manifest that has not kept up with a
// harness release matches nothing, and the pane then holds no agent at all:
// no row in the agent list, no harness or status in the fleet view, and
// nothing for the monitor to read. CFO started the harness, so it knows the
// kind, the session, and the state it just drove the pane into without
// reading the screen, and reports them rather than hoping a manifest agrees.
func (c *Client) ReportAgent(ctx context.Context, target Target, agent, state, sessionID, sessionPath string) error {
	if err := validateTarget(target); err != nil {
		return err
	}
	if agent == "" {
		return &requestError{message: "herdr: reported agent kind is required"}
	}
	if !reportableAgentState(state) {
		return &requestError{message: fmt.Sprintf("herdr: reported agent state %q is not one of blocked, idle, unknown, working", state)}
	}
	argv := []string{"pane", "report-agent", target.Pane, "--source", ReportSource, "--agent", agent, "--state", state}
	if sessionID != "" {
		argv = append(argv, "--agent-session-id", sessionID)
	}
	if sessionPath != "" {
		argv = append(argv, "--agent-session-path", sessionPath)
	}
	_, err := c.required(ctx, target.Session, target, "pane report-agent", argv...)
	return err
}

// reportableAgentState is the lifecycle vocabulary Herdr accepts from a
// reporting source. Herdr rejects anything else, so the check keeps a typo in
// a caller from reaching the socket as a failed spawn.
func reportableAgentState(state string) bool {
	switch state {
	case "blocked", "idle", "unknown", "working":
		return true
	default:
		return false
	}
}

// HarnessRunning reports whether the pane is running something other than the
// shell CFO prepared it with.
//
// This is the liveness evidence that does not depend on Herdr recognizing the
// harness: Herdr reads the pane's foreground process group from the operating
// system, so a pane still sitting at its own shell has the shell itself in the
// foreground, and a pane running any harness does not. It deliberately does
// not match executable names. A harness installed as an npm shim runs as the
// interpreter rather than under its own name, and every such table needs
// another entry for every harness, which is the coupling that left pi
// invisible in the first place.
func (c *Client) HarnessRunning(ctx context.Context, target Target) (bool, error) {
	if err := validateTarget(target); err != nil {
		return false, err
	}
	result, err := c.required(ctx, target.Session, target, "pane process-info", "pane", "process-info", "--pane", target.Pane)
	if err != nil {
		return false, err
	}
	var response struct {
		ProcessInfo struct {
			ForegroundProcessGroupID int `json:"foreground_process_group_id"`
			ShellPID                 int `json:"shell_pid"`
		} `json:"process_info"`
	}
	if err := decodeResult(result.Stdout, &response); err != nil {
		return false, fmt.Errorf("herdr: decode pane process info for %s: %w", target, err)
	}
	info := response.ProcessInfo
	if info.ShellPID == 0 || info.ForegroundProcessGroupID == 0 {
		return false, fmt.Errorf("herdr: pane process info for %s reported no foreground process group", target)
	}
	return info.ForegroundProcessGroupID != info.ShellPID, nil
}

// AgentKinds reports the agent kinds the running Herdr server can natively
// start, read from its active detection manifests.
func (c *Client) AgentKinds(ctx context.Context) (map[string]bool, error) {
	result, err := c.required(ctx, c.session(), Target{}, "agent manifests", "server", "agent-manifests", "--json")
	if err != nil {
		return nil, err
	}
	var response struct {
		Manifests []struct {
			Agent string `json:"agent"`
		} `json:"manifests"`
	}
	if err := decodeResult(result.Stdout, &response); err != nil {
		return nil, fmt.Errorf("herdr: decode agent manifests response: %w", err)
	}
	kinds := make(map[string]bool, len(response.Manifests))
	for _, manifest := range response.Manifests {
		if manifest.Agent != "" {
			kinds[manifest.Agent] = true
		}
	}
	if len(kinds) == 0 {
		return nil, errors.New("herdr: agent manifests response lists no agents")
	}
	return kinds, nil
}

// Capture reads at least 200 lines to work around Herdr's viewport bug, then
// returns only the caller's requested tail.
func (c *Client) Capture(ctx context.Context, target Target, lines int, ansi bool) (string, error) {
	if lines <= 0 {
		lines = captureFloor
	}
	fetch := max(lines, captureFloor)
	args := []string{"pane", "read", target.Pane, "--source", "recent", "--lines", fmt.Sprint(fetch)}
	if ansi {
		args = append(args, "--format", "ansi")
	}
	result, err := c.required(ctx, target.Session, target, "pane read", args...)
	if err != nil {
		return "", err
	}
	return tail(string(result.Stdout), lines), nil
}

// AgentStatus distinguishes structural pane absence, agent absence, live
// registration, and unreadable Herdr responses without relying on exit codes.
func (c *Client) AgentStatus(ctx context.Context, target Target) (AgentStatus, error) {
	paneResult, err := c.raw(ctx, target.Session, "pane", "get", target.Pane)
	if err != nil {
		return AgentUnreadable, err
	}
	paneRaw, paneCode, err := responseEnvelope(paneResult)
	if err != nil {
		return AgentUnreadable, fmt.Errorf("herdr: decode pane presence for %s: %w", target, err)
	}
	if paneCode != "" {
		if paneCode == "pane_not_found" {
			return AgentMissing, nil
		}
		return AgentUnreadable, fmt.Errorf("herdr: pane presence for %s returned %s", target, paneCode)
	}
	var pane struct {
		Pane struct {
			ID string `json:"pane_id"`
		} `json:"pane"`
	}
	if err := decodeRaw(paneRaw, &pane); err != nil || pane.Pane.ID != target.Pane {
		if err != nil {
			return AgentUnreadable, fmt.Errorf("herdr: decode pane presence for %s: %w", target, err)
		}
		return AgentUnreadable, fmt.Errorf("herdr: pane presence for %s did not round-trip pane_id", target)
	}

	agentResult, err := c.raw(ctx, target.Session, "agent", "get", target.Pane)
	if err != nil {
		return AgentUnreadable, err
	}
	agentRaw, agentCode, err := responseEnvelope(agentResult)
	if err != nil {
		return AgentUnreadable, fmt.Errorf("herdr: decode agent status for %s: %w", target, err)
	}
	if agentCode != "" {
		if agentCode == "agent_not_found" {
			return AgentDead, nil
		}
		return AgentUnreadable, fmt.Errorf("herdr: agent status for %s returned %s", target, agentCode)
	}
	status, err := agentStatus(agentRaw)
	if err != nil {
		return AgentUnreadable, fmt.Errorf("herdr: decode agent status for %s: %w", target, err)
	}
	if knownAgentStatus(status) {
		return AgentAlive, nil
	}
	return AgentUnreadable, fmt.Errorf("herdr: agent status for %s is unreadable: %q", target, status)
}

// BusyState returns the native watcher-facing activity signal. A blocked agent
// is deliberately idle because it needs human attention rather than CPU time.
func (c *Client) BusyState(ctx context.Context, target Target) (BusyState, error) {
	status, err := c.readAgentStatus(ctx, target)
	if err != nil {
		return BusyUnknown, err
	}
	switch status {
	case "working":
		return BusyWorking, nil
	case "idle", "done", "blocked":
		return BusyIdle, nil
	case "unknown":
		return BusyUnknown, nil
	default:
		return BusyUnknown, fmt.Errorf("herdr: unrecognized agent status %q for %s", status, target)
	}
}

// WaitForWorking observes agent state across the requested budget. It returns
// working or blocked immediately, idle only after every read is idle, and
// unknown only when no read supplied a recognized status.
func (c *Client) WaitForWorking(ctx context.Context, target Target, budget time.Duration, polls int) (SubmitState, error) {
	if polls < 1 {
		return SubmitUnknown, errors.New("herdr: WaitForWorking requires at least one poll")
	}
	if err := ctx.Err(); err != nil {
		return SubmitUnknown, err
	}
	if err := validateTarget(target); err != nil {
		return SubmitUnknown, err
	}
	if budget < 0 {
		budget = 0
	}
	interval := time.Duration(0)
	if polls > 1 {
		interval = budget / time.Duration(polls-1)
	}

	readable := 0
	idle := 0
	for poll := 0; poll < polls; poll++ {
		if poll > 0 {
			if err := c.sleep(ctx, interval); err != nil {
				return SubmitUnknown, fmt.Errorf("herdr: wait for agent %s: %w", target, err)
			}
		}
		status, err := c.readAgentStatus(ctx, target)
		if err != nil {
			if WaitError(ctx, err) {
				return SubmitUnknown, err
			}
			continue
		}
		switch status {
		case "working":
			return SubmitWorking, nil
		case "blocked":
			return SubmitBlocked, nil
		case "idle", "done":
			readable++
			idle++
		case "unknown":
			readable++
		}
	}
	if idle == polls {
		return SubmitIdle, nil
	}
	if readable == 0 {
		return SubmitUnknown, nil
	}
	return SubmitPending, nil
}

func validateTarget(target Target) error {
	if target.Session == "" {
		return &requestError{message: "herdr: target session is required"}
	}
	if target.Pane == "" {
		return &requestError{message: "herdr: target pane is required"}
	}
	return nil
}

// WaitError reports whether err is a terminal poll failure (a cancelled
// context, a missing runner, or a runner process error) rather than a
// transient unreadable Herdr response. Wait loops fail fast on terminal
// errors and keep polling on any other error.
func WaitError(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return true
	}
	var request *requestError
	if errors.As(err, &request) {
		return true
	}
	var runner *runnerError
	return errors.As(err, &runner)
}

type workspaceRecord struct {
	ID    string `json:"workspace_id"`
	Label string `json:"label"`
}

type tabRecord struct {
	ID    string `json:"tab_id"`
	Label string `json:"label"`
}

type paneRecord struct {
	ID    string `json:"pane_id"`
	TabID string `json:"tab_id"`
}

func (c *Client) serverRunning(ctx context.Context, session string) (bool, error) {
	result, err := c.required(ctx, session, Target{}, "status", "status", "--json")
	if err != nil {
		return false, err
	}
	var status struct {
		Server struct {
			Running *bool `json:"running"`
		} `json:"server"`
	}
	if err := json.Unmarshal(result.Stdout, &status); err != nil {
		return false, fmt.Errorf("herdr: decode status response: %w", err)
	}
	if status.Server.Running == nil {
		return false, errors.New("herdr: status response is missing server.running")
	}
	return *status.Server.Running, nil
}

func (c *Client) workspaces(ctx context.Context, session string) ([]workspaceRecord, error) {
	result, err := c.required(ctx, session, Target{}, "workspace list", "workspace", "list")
	if err != nil {
		return nil, err
	}
	var response struct {
		Workspaces json.RawMessage `json:"workspaces"`
	}
	if err := decodeResult(result.Stdout, &response); err != nil {
		return nil, fmt.Errorf("herdr: decode workspace list response: %w", err)
	}
	var workspaces []workspaceRecord
	if err := decodeArray(response.Workspaces, &workspaces, "workspaces"); err != nil {
		return nil, fmt.Errorf("herdr: decode workspace list response: %w", err)
	}
	return workspaces, nil
}

func (c *Client) tabs(ctx context.Context, session, workspaceID string) ([]tabRecord, error) {
	result, err := c.required(ctx, session, Target{}, "tab list", "tab", "list", "--workspace", workspaceID)
	if err != nil {
		return nil, err
	}
	var response struct {
		Tabs json.RawMessage `json:"tabs"`
	}
	if err := decodeResult(result.Stdout, &response); err != nil {
		return nil, fmt.Errorf("herdr: decode tab list response: %w", err)
	}
	var tabs []tabRecord
	if err := decodeArray(response.Tabs, &tabs, "tabs"); err != nil {
		return nil, fmt.Errorf("herdr: decode tab list response: %w", err)
	}
	return tabs, nil
}

func (c *Client) panes(ctx context.Context, session, workspaceID string) ([]paneRecord, error) {
	result, err := c.required(ctx, session, Target{}, "pane list", "pane", "list", "--workspace", workspaceID)
	if err != nil {
		return nil, err
	}
	var response struct {
		Panes json.RawMessage `json:"panes"`
	}
	if err := decodeResult(result.Stdout, &response); err != nil {
		return nil, fmt.Errorf("herdr: decode pane list response: %w", err)
	}
	var panes []paneRecord
	if err := decodeArray(response.Panes, &panes, "panes"); err != nil {
		return nil, fmt.Errorf("herdr: decode pane list response: %w", err)
	}
	return panes, nil
}

func (c *Client) isHusk(ctx context.Context, session, workspaceID, tabID string) (bool, error) {
	panes, err := c.panes(ctx, session, workspaceID)
	if err != nil {
		return false, err
	}
	for _, pane := range panes {
		if pane.TabID != tabID {
			continue
		}
		if pane.ID == "" {
			return false, fmt.Errorf("herdr: pane for tab %s has no pane_id", tabID)
		}
		status, err := c.AgentStatus(ctx, Target{Session: session, Pane: pane.ID})
		if err != nil {
			return false, err
		}
		if status != AgentMissing && status != AgentDead {
			return false, nil
		}
	}
	return true, nil
}

func (c *Client) pruneSeededDefault(ctx context.Context, container Container) {
	if container.SeededDefaultTab == "" {
		return
	}
	tabs, err := c.tabs(ctx, container.Session, container.WorkspaceID)
	if err != nil || len(tabs) < 2 {
		return
	}
	for _, tab := range tabs {
		if tab.ID != container.SeededDefaultTab || tab.Label != "1" {
			continue
		}
		panes, err := c.panes(ctx, container.Session, container.WorkspaceID)
		if err != nil {
			return
		}
		for _, pane := range panes {
			if pane.TabID != tab.ID || pane.ID == "" {
				continue
			}
			status, err := c.readAgentStatus(ctx, Target{Session: container.Session, Pane: pane.ID})
			if err != nil || (status != "idle" && status != "done" && status != "blocked") {
				return
			}
			_ = c.close(ctx, container.Session, "pane", pane.ID)
			return
		}
		return
	}
}

// CloseTab closes one task tab. Cleanup calls it only after the endpoint is
// proven inactive; an already-absent tab is accepted, matching close's
// not-found tolerance.
func (c *Client) CloseTab(ctx context.Context, session, tabID string) error {
	return c.close(ctx, session, "tab", tabID)
}

func (c *Client) close(ctx context.Context, session, kind, id string) error {
	operation := kind + " close"
	result, err := c.raw(ctx, session, kind, "close", id)
	if err != nil {
		return &CommandError{Operation: operation, Target: Target{Session: session, Pane: id}, Err: err}
	}
	if result.ExitCode == 0 {
		return nil
	}
	_, code, decodeErr := responseEnvelope(result)
	if decodeErr == nil && code == kind+"_not_found" {
		return nil
	}
	return &CommandError{Operation: operation, Target: Target{Session: session, Pane: id}, Stderr: strings.TrimSpace(string(result.Stderr)), ExitCode: result.ExitCode}
}

func (c *Client) verifyHusksRemoved(ctx context.Context, session, workspaceID, label string) error {
	tabs, err := c.tabs(ctx, session, workspaceID)
	if err != nil {
		return err
	}
	for _, tab := range tabs {
		if tab.Label == label {
			return fmt.Errorf("herdr: tab %q still has duplicate %s after replacement", label, tab.ID)
		}
	}
	return nil
}

func (c *Client) readAgentStatus(ctx context.Context, target Target) (string, error) {
	if err := validateTarget(target); err != nil {
		return "", err
	}
	result, err := c.raw(ctx, target.Session, "agent", "get", target.Pane)
	if err != nil {
		return "", err
	}
	raw, code, err := envelope(result.Stdout)
	if err != nil {
		return "", err
	}
	if code != "" {
		return "", fmt.Errorf("herdr: agent get for %s returned %s", target, code)
	}
	return agentStatus(raw)
}

// AgentDetail reads the exact native agent identity and its current state.
func (c *Client) AgentDetail(ctx context.Context, target Target) (AgentDetail, error) {
	if err := validateTarget(target); err != nil {
		return AgentDetail{}, err
	}
	result, err := c.raw(ctx, target.Session, "agent", "get", target.Pane)
	if err != nil {
		return AgentDetail{}, err
	}
	raw, code, err := envelope(result.Stdout)
	if err != nil {
		return AgentDetail{}, err
	}
	if code != "" {
		return AgentDetail{}, fmt.Errorf("herdr: agent get for %s returned %s", target, code)
	}
	return agentDetail(raw)
}

func agentStatus(raw json.RawMessage) (string, error) {
	var response struct {
		Agent struct {
			Status string `json:"agent_status"`
		} `json:"agent"`
	}
	if err := decodeRaw(raw, &response); err != nil {
		return "", err
	}
	if response.Agent.Status == "" {
		return "", errors.New("missing result.agent.agent_status")
	}
	return response.Agent.Status, nil
}

func agentDetail(raw json.RawMessage) (AgentDetail, error) {
	var response struct {
		Agent struct {
			Agent  string `json:"agent"`
			Status string `json:"agent_status"`
		} `json:"agent"`
	}
	if err := decodeRaw(raw, &response); err != nil {
		return AgentDetail{}, err
	}
	if response.Agent.Agent == "" {
		return AgentDetail{}, errors.New("missing result.agent.agent")
	}
	if response.Agent.Status == "" {
		return AgentDetail{}, errors.New("missing result.agent.agent_status")
	}
	return AgentDetail{Agent: response.Agent.Agent, Status: response.Agent.Status}, nil
}

func knownAgentStatus(status string) bool {
	switch status {
	case "working", "idle", "done", "blocked", "unknown":
		return true
	default:
		return false
	}
}

func (c *Client) required(ctx context.Context, session string, target Target, operation string, args ...string) (execx.Result, error) {
	result, err := c.raw(ctx, session, args...)
	if err != nil {
		return execx.Result{}, &CommandError{Operation: operation, Target: target, Err: err}
	}
	if result.ExitCode != 0 {
		return execx.Result{}, &CommandError{Operation: operation, Target: target, Stderr: strings.TrimSpace(string(result.Stderr)), ExitCode: result.ExitCode}
	}
	return result, nil
}

func (c *Client) raw(ctx context.Context, session string, args ...string) (execx.Result, error) {
	if c == nil || c.Commands == nil {
		return execx.Result{}, &requestError{message: "herdr: command runner is required"}
	}
	if session == "" {
		return execx.Result{}, &requestError{message: "herdr: session is required"}
	}
	// The session flag is a Herdr option, never an agent argument: when the
	// command carries a `--` separator (agent start with harness args), the
	// flag must precede it or Herdr forwards the flag to the agent.
	requestArgs := append([]string{}, args...)
	if separator := slices.Index(requestArgs, "--"); separator >= 0 {
		requestArgs = slices.Insert(requestArgs, separator, "--session", session)
	} else {
		requestArgs = append(requestArgs, "--session", session)
	}
	result, err := c.Commands.Run(ctx, execx.Request{Name: "herdr", Args: requestArgs})
	if err != nil {
		return execx.Result{}, &runnerError{err: err}
	}
	return result, nil
}

func (c *Client) start(ctx context.Context, session string, args ...string) error {
	if c == nil || c.Commands == nil {
		return errors.New("herdr: command runner is required")
	}
	starter, ok := c.Commands.(execx.Starter)
	if !ok {
		return errors.New("herdr: command runner does not support non-blocking process start")
	}
	requestArgs := append(append([]string{}, args...), "--session", session)
	return starter.Start(ctx, execx.Request{Name: "herdr", Args: requestArgs})
}

func (c *Client) session() string {
	if c != nil && c.Session != "" {
		return c.Session
	}
	return defaultSession
}

func (c *Client) sleep(ctx context.Context, duration time.Duration) error {
	if c != nil && c.Sleep != nil {
		return c.Sleep(ctx, duration)
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

func decodeResult(data []byte, destination any) error {
	raw, code, err := envelope(data)
	if err != nil {
		return err
	}
	if code != "" {
		return fmt.Errorf("response error %s", code)
	}
	return decodeRaw(raw, destination)
}

func envelope(data []byte) (json.RawMessage, string, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, "", err
	}
	if object == nil {
		return nil, "", errors.New("response is not an object")
	}
	if errorRaw, exists := object["error"]; exists {
		var responseError struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(errorRaw, &responseError); err != nil {
			return nil, "", fmt.Errorf("decode response error: %w", err)
		}
		if responseError.Code == "" {
			return nil, "", errors.New("response error is missing code")
		}
		return nil, responseError.Code, nil
	}
	result, exists := object["result"]
	if !exists || len(result) == 0 || string(result) == "null" {
		return nil, "", errors.New("response is missing result")
	}
	return result, "", nil
}

// responseEnvelope accepts a valid structured error emitted on either output
// stream. Herdr may put normal business errors on stderr with a non-zero exit;
// the JSON envelope, not that exit status, remains the liveness evidence.
func responseEnvelope(result execx.Result) (json.RawMessage, string, error) {
	raw, code, stdoutErr := envelope(result.Stdout)
	if stdoutErr == nil {
		return raw, code, nil
	}
	if len(result.Stderr) == 0 {
		return nil, "", stdoutErr
	}
	_, stderrCode, stderrErr := envelope(result.Stderr)
	if stderrErr == nil && stderrCode != "" {
		return nil, stderrCode, nil
	}
	return nil, "", stdoutErr
}

func decodeRaw(raw json.RawMessage, destination any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return errors.New("response is missing result")
	}
	return json.Unmarshal(raw, destination)
}

func decodeArray(raw json.RawMessage, destination any, field string) error {
	if len(raw) == 0 || string(raw) == "null" {
		return fmt.Errorf("missing result.%s", field)
	}
	return json.Unmarshal(raw, destination)
}

func normalizeKey(key string) string {
	switch key {
	case "Enter", "enter":
		return "enter"
	case "Escape", "escape", "Esc", "esc":
		return "escape"
	case "C-c", "c-c", "ctrl+c", "Ctrl+C":
		return "ctrl+c"
	case "C-u", "c-u", "ctrl+u", "Ctrl+U":
		return "ctrl+u"
	default:
		return key
	}
}

func tail(text string, lines int) string {
	if lines <= 0 {
		return ""
	}
	parts := strings.SplitAfter(text, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) <= lines {
		return text
	}
	return strings.Join(parts[len(parts)-lines:], "")
}
