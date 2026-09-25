// Package terminaltest holds a terminal.Backend with no terminals behind it,
// so a test can drive a fleet command through the interface alone.
package terminaltest

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/terminal"
)

// Fake records every call as one line, in order, and answers from its fields.
// Each prompt it accepts advances the agent's counters, as an agent that took
// the prompt does. Fail makes the named method return that error instead.
type Fake struct {
	Session   string
	Kinds     map[string]bool
	Container herdr.Container
	// Endpoint is what CreateTask returns.
	Endpoint herdr.Endpoint
	Status   herdr.AgentStatus
	Detail   herdr.AgentDetail
	Working  herdr.SubmitState
	Busy     herdr.BusyState
	Screen   string
	// Structure is what Snapshot returns.
	Structure herdr.SessionSnapshot
	Process   herdr.PaneProcessInfo
	Running   bool
	Dead      bool
	Fail      map[string]error

	mu      sync.Mutex
	calls   []string
	prompts int64
}

var _ terminal.Backend = (*Fake)(nil)

// Calls returns every call so far, one line each.
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// Asked reports whether any call line equals line.
func (f *Fake) Asked(line string) bool {
	for _, call := range f.Calls() {
		if call == line {
			return true
		}
	}
	return false
}

// Missing returns the first of prefixes that no call starts with after the
// calls matched to the prefixes before it, or "" when the calls came in that
// order.
func (f *Fake) Missing(prefixes ...string) string {
	calls := f.Calls()
	at := 0
	for _, prefix := range prefixes {
		for at < len(calls) && !strings.HasPrefix(calls[at], prefix) {
			at++
		}
		if at == len(calls) {
			return prefix
		}
		at++
	}
	return ""
}

func (f *Fake) record(method string, fields ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.Join(append([]string{method}, fields...), " "))
	return f.Fail[method]
}

func (f *Fake) EffectiveSession() string {
	return f.Session
}

func (f *Fake) EnsureServer(context.Context) error {
	return f.record("EnsureServer")
}

func (f *Fake) Preflight(context.Context) error {
	return f.record("Preflight")
}

func (f *Fake) AgentKinds(context.Context) (map[string]bool, error) {
	return f.Kinds, f.record("AgentKinds")
}

func (f *Fake) EnsureContainer(_ context.Context, cwd string) (herdr.Container, error) {
	return f.Container, f.record("EnsureContainer", cwd)
}

func (f *Fake) CreateTask(_ context.Context, _ herdr.Container, label, cwd string) (herdr.Endpoint, error) {
	return f.Endpoint, f.record("CreateTask", label, cwd)
}

func (f *Fake) CloseTab(_ context.Context, session, tabID string) error {
	return f.record("CloseTab", session, tabID)
}

func (f *Fake) Snapshot(context.Context) (herdr.SessionSnapshot, error) {
	return f.Structure, f.record("Snapshot")
}

func (f *Fake) SendLiteral(_ context.Context, target herdr.Target, text string) error {
	return f.record("SendLiteral", target.String(), text)
}

func (f *Fake) SendKey(_ context.Context, target herdr.Target, key string) error {
	return f.record("SendKey", target.String(), key)
}

func (f *Fake) Capture(_ context.Context, target herdr.Target, lines int, _ bool) (string, error) {
	return f.Screen, f.record("Capture", target.String(), strconv.Itoa(lines))
}

func (f *Fake) AgentStart(_ context.Context, target herdr.Target, name, kind string, _ []string) error {
	return f.record("AgentStart", target.String(), name, kind)
}

func (f *Fake) AgentPrompt(_ context.Context, target herdr.Target, text string) error {
	if err := f.record("AgentPrompt", target.String(), text); err != nil {
		return err
	}
	f.mu.Lock()
	f.prompts++
	f.mu.Unlock()
	return nil
}

func (f *Fake) ReportAgent(_ context.Context, target herdr.Target, agent, state, _, _ string) error {
	return f.record("ReportAgent", target.String(), agent, state)
}

func (f *Fake) AgentStatus(_ context.Context, target herdr.Target) (herdr.AgentStatus, error) {
	return f.Status, f.record("AgentStatus", target.String())
}

func (f *Fake) AgentDetail(_ context.Context, target herdr.Target) (herdr.AgentDetail, error) {
	err := f.record("AgentDetail", target.String())
	f.mu.Lock()
	defer f.mu.Unlock()
	detail := f.Detail
	detail.StateChangeSeq += f.prompts
	detail.Revision += f.prompts
	return detail, err
}

func (f *Fake) WaitForWorking(_ context.Context, target herdr.Target, _ time.Duration, _ int) (herdr.SubmitState, error) {
	return f.Working, f.record("WaitForWorking", target.String())
}

func (f *Fake) BusyState(_ context.Context, target herdr.Target) (herdr.BusyState, error) {
	return f.Busy, f.record("BusyState", target.String())
}

func (f *Fake) HarnessRunning(_ context.Context, target herdr.Target) (bool, error) {
	return f.Running, f.record("HarnessRunning", target.String())
}

func (f *Fake) PaneProvablyDead(_ context.Context, target herdr.Target) bool {
	_ = f.record("PaneProvablyDead", target.String())
	return f.Dead
}

func (f *Fake) PaneProcessInfo(_ context.Context, target herdr.Target) (herdr.PaneProcessInfo, error) {
	return f.Process, f.record("PaneProcessInfo", target.String())
}
