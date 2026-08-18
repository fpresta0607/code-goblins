package monitor

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// HerdrProber is the read-only structural prober. One monitoring cycle takes
// one session snapshot, validates every task's recorded identity against that
// single topology, and adds at most one bounded terminal capture per valid
// task. It has no send, close, restart, return, or delete capability: its only
// dependency is the read-only Herdr client surface. Unreadable evidence always
// becomes an unknown sample, never invented liveness.
type HerdrProber struct {
	Client *herdr.Client

	mu            sync.Mutex
	schemaChecked bool
	loaded        bool
	snapshot      herdr.SessionSnapshot
	snapshotErr   error
	agentsLoaded  bool
	agents        []herdr.AgentRecord
	agentsErr     error
}

// NewHerdrProber binds the structural prober to one Herdr session through the
// given client.
func NewHerdrProber(client *herdr.Client) *HerdrProber {
	return &HerdrProber{Client: client}
}

var (
	_ Prober      = (*HerdrProber)(nil)
	_ CycleProber = (*HerdrProber)(nil)
)

// BeginScan marks one monitoring-cycle boundary: the previous cycle's
// structural evidence and agent list are dropped so the next Inspect loads
// exactly one fresh snapshot and one fresh agent list shared by every task in
// the cycle. The immutable schema check stays cached for the process lifetime
// once it has succeeded.
func (p *HerdrProber) BeginScan(context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loaded = false
	p.snapshot = herdr.SessionSnapshot{}
	p.snapshotErr = nil
	p.agentsLoaded = false
	p.agents = nil
	p.agentsErr = nil
}

// Inspect validates one task's recorded Herdr identity against the cycle's
// single session snapshot and, only for a structurally valid task, requests
// its one bounded terminal capture. The agent's native status and liveness
// counters come from the cycle's single `herdr agent list`.
func (p *HerdrProber) Inspect(ctx context.Context, meta state.TaskMeta) (EndpointSample, error) {
	snapshot, err := p.cycleSnapshot(ctx)
	if err != nil {
		return EndpointSample{Verdict: ProbeUnknown, Detail: err.Error()}, nil
	}
	agents, err := p.cycleAgents(ctx)
	if err != nil {
		return EndpointSample{Verdict: ProbeUnknown, Detail: err.Error()}, nil
	}
	return p.inspect(ctx, snapshot, agents, meta)
}

func (p *HerdrProber) cycleSnapshot(ctx context.Context) (herdr.SessionSnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.loaded {
		return p.snapshot, p.snapshotErr
	}
	p.loaded = true
	if p.Client == nil {
		p.snapshotErr = errors.New("monitor: Herdr prober client is required")
		return p.snapshot, p.snapshotErr
	}
	if !p.schemaChecked {
		if err := p.Client.CheckSchema(ctx); err != nil {
			p.snapshotErr = err
			return p.snapshot, p.snapshotErr
		}
		p.schemaChecked = true
	}
	snapshot, err := p.Client.Snapshot(ctx)
	if err != nil {
		p.snapshotErr = err
		return p.snapshot, p.snapshotErr
	}
	if snapshot.Protocol != herdr.SupportedProtocol {
		p.snapshotErr = fmt.Errorf("herdr: session snapshot protocol %d, want %d", snapshot.Protocol, herdr.SupportedProtocol)
		return p.snapshot, p.snapshotErr
	}
	p.snapshot = snapshot
	return p.snapshot, nil
}

func (p *HerdrProber) cycleAgents(ctx context.Context) ([]herdr.AgentRecord, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.agentsLoaded {
		return p.agents, p.agentsErr
	}
	p.agentsLoaded = true
	if p.Client == nil {
		p.agentsErr = errors.New("monitor: Herdr prober client is required")
		return nil, p.agentsErr
	}
	agents, err := p.Client.AgentList(ctx)
	if err != nil {
		p.agentsErr = err
		return nil, err
	}
	p.agents = agents
	return agents, nil
}

func (p *HerdrProber) inspect(ctx context.Context, snapshot herdr.SessionSnapshot, agents []herdr.AgentRecord, meta state.TaskMeta) (EndpointSample, error) {
	unknown := func(detail string) EndpointSample {
		return EndpointSample{Verdict: ProbeUnknown, Detail: detail}
	}
	if meta.HerdrSession == "" || meta.HerdrWorkspaceID == "" || meta.HerdrTabID == "" || meta.HerdrPaneID == "" {
		return unknown("task metadata has incomplete Herdr identity"), nil
	}
	if meta.HerdrSession != p.Client.EffectiveSession() {
		return unknown(fmt.Sprintf("recorded session %q does not match prober session %q", meta.HerdrSession, p.Client.EffectiveSession())), nil
	}
	workspaces := 0
	for _, workspace := range snapshot.Workspaces {
		if workspace.ID == meta.HerdrWorkspaceID {
			workspaces++
		}
	}
	if workspaces != 1 {
		return unknown(fmt.Sprintf("session snapshot has %d copies of workspace %s", workspaces, meta.HerdrWorkspaceID)), nil
	}
	tabs := 0
	var tab herdr.SnapshotTab
	for _, candidate := range snapshot.Tabs {
		if candidate.ID == meta.HerdrTabID {
			tabs++
			tab = candidate
		}
	}
	if tabs != 1 {
		return unknown(fmt.Sprintf("session snapshot has %d copies of tab %s", tabs, meta.HerdrTabID)), nil
	}
	if tab.WorkspaceID != meta.HerdrWorkspaceID {
		return unknown(fmt.Sprintf("tab %s belongs to workspace %s, not %s", tab.ID, tab.WorkspaceID, meta.HerdrWorkspaceID)), nil
	}
	if tab.Label != "gb-"+meta.ID {
		return unknown(fmt.Sprintf("tab %s label %q is not gb-%s", tab.ID, tab.Label, meta.ID)), nil
	}
	panes := 0
	var pane herdr.SnapshotPane
	for _, candidate := range snapshot.Panes {
		if candidate.ID == meta.HerdrPaneID {
			panes++
			pane = candidate
		}
	}
	if panes == 0 {
		return EndpointSample{Verdict: ProbeMissing, Detail: fmt.Sprintf("recorded pane %s is absent from the session snapshot", meta.HerdrPaneID)}, nil
	}
	if panes > 1 {
		return unknown(fmt.Sprintf("session snapshot has %d copies of pane %s", panes, meta.HerdrPaneID)), nil
	}
	if pane.TabID != meta.HerdrTabID || pane.WorkspaceID != meta.HerdrWorkspaceID {
		return unknown(fmt.Sprintf("pane %s belongs to tab %s workspace %s, not tab %s workspace %s", pane.ID, pane.TabID, pane.WorkspaceID, meta.HerdrTabID, meta.HerdrWorkspaceID)), nil
	}

	sample := EndpointSample{
		Verdict: ProbePresent,
		Endpoint: herdr.Endpoint{
			Target:      herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID},
			WorkspaceID: meta.HerdrWorkspaceID,
			TabID:       meta.HerdrTabID,
			PaneID:      meta.HerdrPaneID,
		},
		TabLabel: tab.Label,
	}

	agentsOnPane := 0
	var agent herdr.AgentRecord
	for _, candidate := range agents {
		if candidate.PaneID == meta.HerdrPaneID {
			agentsOnPane++
			agent = candidate
		}
	}
	if agentsOnPane > 1 {
		return unknown(fmt.Sprintf("agent list has %d agents on pane %s", agentsOnPane, meta.HerdrPaneID)), nil
	}
	if agentsOnPane == 0 {
		sample.Agent = herdr.AgentDead
		sample.Busy = herdr.BusyUnknown
		sample.Detail = fmt.Sprintf("pane %s has no registered agent", meta.HerdrPaneID)
		return sample, nil
	}
	if agent.TabID != meta.HerdrTabID || agent.WorkspaceID != meta.HerdrWorkspaceID {
		return unknown(fmt.Sprintf("agent on pane %s belongs to tab %s workspace %s, not tab %s workspace %s", agent.PaneID, agent.TabID, agent.WorkspaceID, meta.HerdrTabID, meta.HerdrWorkspaceID)), nil
	}
	sample.Agent = herdr.AgentAlive
	sample.Status = agent.Status
	sample.InteractiveReady = agent.InteractiveReady
	sample.StateChangeSeq = agent.StateChangeSeq
	sample.Revision = agent.Revision

	capture, err := p.Client.CaptureEvidence(ctx, sample.Endpoint.Target)
	if err != nil {
		return unknown(fmt.Sprintf("pane capture is unreadable: %v", err)), nil
	}
	sample.Capture = capture
	switch agent.Status {
	case herdr.AgentWorking:
		sample.Busy = herdr.BusyWorking
	case herdr.AgentIdle, herdr.AgentDone, herdr.AgentBlocked:
		sample.Busy = herdr.BusyIdle
	default:
		sample.Busy = herdr.BusyUnknown
	}
	return sample, nil
}
