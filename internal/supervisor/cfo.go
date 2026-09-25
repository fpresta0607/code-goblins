package supervisor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type primaryRegistration struct {
	Target    herdr.Target `json:"target"`
	Workspace string       `json:"workspace"`
	Tab       string       `json:"tab"`
	Agent     string       `json:"agent"`
	Terminal  string       `json:"terminal"`
	Process   lock.Info    `json:"process"`
}

// registrationProblem is every way primary.json stops naming a reachable CFO.
// They share one fix, so every message carries it: the board shows it as one
// state instead of a generic delivery failure.
type registrationProblem string

func (p registrationProblem) Error() string {
	return string(p) + "; run cfo register in the CFO session"
}

const errNotRegistered = registrationProblem("The CFO is not registered")

// CFOConnection uses the primary.json registration Register writes.
// Reading it grants no permission to guess a different pane or register one.
type CFOConnection struct {
	State string
	Herdr *herdr.Client
}

func decodePrimary(reader io.Reader) (primaryRegistration, string, error) {
	var primary primaryRegistration
	data, err := io.ReadAll(io.LimitReader(reader, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return primary, "", registrationProblem("The CFO registration is unreadable or exceeds its limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&primary); err != nil {
		return primary, "", registrationProblem("The CFO registration is invalid")
	}
	if decoder.Decode(new(json.RawMessage)) != io.EOF || primary.Target.Session == "" || primary.Target.Pane == "" || primary.Workspace == "" || primary.Tab == "" || primary.Terminal == "" || primary.Agent == "" || primary.Process.PID <= 0 || primary.Process.Start.IsZero() {
		return primary, "", registrationProblem("The CFO registration has incomplete identity")
	}
	for _, value := range []string{primary.Target.Session, primary.Target.Pane, primary.Workspace, primary.Tab, primary.Terminal, primary.Agent} {
		if len(value) > 256 || strings.ContainsAny(value, "\x00\r\n") {
			return primary, "", registrationProblem("The CFO registration has an invalid identity")
		}
	}
	sum := sha256.Sum256(data)
	return primary, hex.EncodeToString(sum[:]), nil
}

// Register writes primary.json for the harness this process runs under: the
// foreground process of the Herdr pane named by HERDR_PANE_ID, which must be
// one of this process's own ancestors, so an inherited variable cannot
// register someone else's pane. That harness must also hold the home's
// session lock, taking it when nobody live does, because the primary CFO is
// whoever holds the home. harness names the agent when Herdr has not
// detected one in the pane yet.
func Register(ctx context.Context, stateDir string, client *herdr.Client, harness, session string) (string, error) {
	pane := os.Getenv("HERDR_PANE_ID")
	if pane == "" {
		return "", errors.New("this session is not running in a Herdr pane, so the board cannot reach it")
	}
	// HERDR_SESSION names the session the way spawn and the watcher read it.
	scoped := *client
	if scoped.Session == "" {
		scoped.Session = os.Getenv("HERDR_SESSION")
	}
	target := herdr.Target{Session: scoped.EffectiveSession(), Pane: pane}
	ancestry, harnessAt, err := paneHarness(ctx, &scoped, target)
	if err != nil {
		return "", err
	}
	snapshot, err := scoped.Snapshot(ctx)
	if err != nil {
		return "", fmt.Errorf("Herdr snapshot: %w", err)
	}
	primary := primaryRegistration{Target: target, Agent: harness}
	for _, p := range snapshot.Panes {
		if p.ID == pane {
			primary.Workspace, primary.Tab, primary.Terminal = p.WorkspaceID, p.TabID, p.TerminalID
		}
	}
	if primary.Terminal == "" {
		return "", fmt.Errorf("Herdr session %s does not list pane %s", target.Session, pane)
	}
	for _, agent := range snapshot.Agents {
		if agent.PaneID == pane && agent.Agent != "" {
			if harness != "" && agent.Agent != harness {
				return "", fmt.Errorf("Herdr detects %s in pane %s, not %s", agent.Agent, pane, harness)
			}
			primary.Agent = agent.Agent
		}
	}
	if primary.Agent == "" {
		return "", fmt.Errorf("Herdr has not detected the agent in pane %s yet; retry once it has", pane)
	}
	// Custody is taken last, so a refused registration changes nothing.
	if !slices.ContainsFunc(ancestry[:harnessAt+1], func(entry proc.Entry) bool { return lock.HeldBy(stateDir, entry.PID) }) {
		if _, err := lock.AcquireOwner(stateDir, ancestry[harnessAt].PID, session); err != nil {
			return "", fmt.Errorf("another live session holds this home, so this one is not the primary CFO: %w", err)
		}
	}
	hostname, err := os.Hostname()
	if err != nil {
		return "", err
	}
	process := ancestry[harnessAt]
	described := fmt.Sprintf("%s pid %d in Herdr pane %s:%s", primary.Agent, process.PID, target.Session, pane)
	// primary.json's hash is the identity questions, reviews and answers are
	// bound to, so the same process in the same pane keeps its bytes.
	if file, err := openPrimary(filepath.Join(stateDir, "primary.json")); err == nil {
		current, _, err := decodePrimary(file)
		_ = file.Close()
		drift := current.Process.Start.Sub(process.Start)
		sameProcess := current.Process.PID == process.PID && current.Process.Hostname == hostname && drift > -time.Second && drift < time.Second
		current.Process = lock.Info{}
		if err == nil && sameProcess && current == primary {
			return described, nil
		}
	}
	primary.Process = lock.Info{PID: process.PID, OwnerPID: process.PID, Session: session, Start: process.Start, Hostname: hostname, Acquired: time.Now().UTC()}
	data, err := json.Marshal(primary)
	if err != nil {
		return "", err
	}
	// Every reader decodes with these rules, so nothing is written that a
	// reader would refuse.
	if _, _, err := decodePrimary(bytes.NewReader(data)); err != nil {
		return "", err
	}
	if err := fsx.AtomicWriteFile(filepath.Join(stateDir, "primary.json"), data); err != nil {
		return "", err
	}
	return described, nil
}

// paneHarness proves the calling process runs under the harness Herdr shows
// in the foreground of target, so a pane variable a process merely inherited
// never passes. It returns this process's ancestry and the harness's place in
// it.
func paneHarness(ctx context.Context, client *herdr.Client, target herdr.Target) ([]proc.Entry, int, error) {
	info, err := client.PaneProcessInfo(ctx, target)
	if err != nil {
		return nil, 0, fmt.Errorf("Herdr cannot describe pane %s: %w", target.Pane, err)
	}
	if info.ForegroundProcessGroupID == info.ShellPID {
		return nil, 0, fmt.Errorf("pane %s has no harness in its foreground", target.Pane)
	}
	ancestry, err := proc.Ancestry(os.Getpid(), 32)
	if err != nil {
		return nil, 0, err
	}
	at := slices.IndexFunc(ancestry, func(entry proc.Entry) bool { return entry.PID == info.ForegroundProcessGroupID })
	if at < 0 {
		return nil, 0, fmt.Errorf("pane %s runs pid %d in its foreground, and this command does not run under it", target.Pane, info.ForegroundProcessGroupID)
	}
	return ancestry, at, nil
}

func (c *CFOConnection) verify(ctx context.Context, primary primaryRegistration) error {
	if c.Herdr == nil {
		return errors.New("Native CFO transport is unavailable")
	}
	if !primary.Process.VerifiedAlive() {
		return registrationProblem(fmt.Sprintf("The registered CFO process is unavailable: pid %d, started %s, is no longer running", primary.Process.PID, primary.Process.Start.UTC().Format("2006-01-02 15:04 UTC")))
	}
	client := *c.Herdr
	client.Session = primary.Target.Session
	snapshot, err := client.Snapshot(ctx)
	if err != nil {
		return errors.New("Herdr cannot verify the registered CFO")
	}
	found := false
	for _, agent := range snapshot.Agents {
		if agent.PaneID == primary.Target.Pane && agent.TabID == primary.Tab && agent.WorkspaceID == primary.Workspace && agent.Agent == primary.Agent {
			found = true
		}
	}
	if !found {
		return registrationProblem("The registered CFO agent changed or is missing in Herdr pane " + primary.Target.Pane)
	}
	found = false
	for _, pane := range snapshot.Panes {
		if pane.ID == primary.Target.Pane && pane.TabID == primary.Tab && pane.WorkspaceID == primary.Workspace && pane.TerminalID == primary.Terminal {
			found = true
		}
	}
	if !found {
		return registrationProblem("The registered CFO terminal changed or is missing in Herdr pane " + primary.Target.Pane)
	}
	process, err := client.PaneProcessInfo(ctx, primary.Target)
	if err != nil {
		return errors.New("Herdr cannot verify the registered CFO")
	}
	if process.ForegroundProcessGroupID != primary.Process.PID || process.ForegroundProcessGroupID == process.ShellPID {
		return registrationProblem("The registered CFO process no longer owns its pane " + primary.Target.Pane)
	}
	return nil
}

// check reports why the board cannot reach the registered CFO right now, or
// nil when it can.
func (c *CFOConnection) check(ctx context.Context) error {
	file, err := openPrimary(filepath.Join(c.State, "primary.json"))
	if err != nil {
		return errNotRegistered
	}
	defer file.Close()
	primary, _, err := decodePrimary(file)
	if err != nil {
		return err
	}
	return c.verify(ctx, primary)
}

// LiveCFO returns the registered CFO's Herdr address when primary.json names
// a process that is still running. It asks Herdr nothing, so neither a board
// that has not checked the registration yet nor a Herdr that cannot answer
// changes whether the launcher starts a CFO: only the registration does.
func LiveCFO(stateDir string) (herdr.Endpoint, bool) {
	file, err := openPrimary(filepath.Join(stateDir, "primary.json"))
	if err != nil {
		return herdr.Endpoint{}, false
	}
	defer file.Close()
	primary, _, err := decodePrimary(file)
	if err != nil || !primary.Process.VerifiedAlive() {
		return herdr.Endpoint{}, false
	}
	return herdr.Endpoint{Target: primary.Target, WorkspaceID: primary.Workspace, TabID: primary.Tab, PaneID: primary.Target.Pane}, true
}

type primaryResolver struct {
	connection *CFOConnection
	primary    primaryRegistration
}

func (r primaryResolver) Resolve(ctx context.Context, _ string) (herdr.Target, state.TaskMeta, error) {
	if err := r.connection.verify(ctx, r.primary); err != nil {
		return herdr.Target{}, state.TaskMeta{}, fmt.Errorf("%w: %v", ErrRejected, err)
	}
	// Nonempty metadata makes registration mandatory in fleet.Sender. It can
	// never switch to the explicit-target raw shell typing fallback.
	return r.primary.Target, state.TaskMeta{HerdrPaneID: r.primary.Target.Pane}, nil
}

// oneLine keeps a board delivery on one line: Herdr's agent prompt drops
// line breaks, which would run the words on either side together.
func oneLine(text string) string {
	return strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(text)
}

func (c *CFOConnection) Send(ctx context.Context, identity, text string) (Evaluation, error) {
	file, err := openPrimary(filepath.Join(c.State, "primary.json"))
	if err != nil {
		return Evaluation{}, fmt.Errorf("%w: %v", ErrRejected, errNotRegistered)
	}
	defer file.Close()
	primary, current, err := decodePrimary(file)
	if err != nil || current != identity {
		return Evaluation{}, fmt.Errorf("%w: the primary CFO changed; refresh before sending", ErrRejected)
	}
	guard := func(ctx context.Context, target herdr.Target, agent herdr.AgentDetail) error {
		if target != primary.Target || agent.Agent != primary.Agent {
			return errors.New("primary CFO agent identity changed")
		}
		return c.verify(ctx, primary)
	}
	sender := fleet.Sender{Terminal: c.Herdr, Resolve: primaryResolver{c, primary}, Guard: guard}
	if err := sender.Text(ctx, "primary-cfo", oneLine("Overlord: "+text)); err != nil {
		return Evaluation{}, err
	}
	return Evaluation{Reason: "Accepted by the registered CFO through Herdr."}, nil
}
