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
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/terminal"
)

type primaryRegistration struct {
	Target    herdr.Target `json:"target"`
	Workspace string       `json:"workspace"`
	Tab       string       `json:"tab"`
	Agent     string       `json:"agent"`
	Terminal  string       `json:"terminal"`
	// Host names the native terminal a CFO runs in, which leaves the Herdr
	// fields above empty.
	Host    string    `json:"host,omitempty"`
	Process lock.Info `json:"process"`
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
	// Terminals opens the terminal backend the CFO and its goblins run in.
	Terminals terminal.Opener
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
	herdrFields := []string{primary.Target.Session, primary.Target.Pane, primary.Workspace, primary.Tab, primary.Terminal}
	complete := !slices.Contains(herdrFields, "")
	if primary.Host != "" {
		complete = state.ValidTaskID(primary.Host) == nil && strings.Join(herdrFields, "") == ""
	}
	if decoder.Decode(new(json.RawMessage)) != io.EOF || !complete || primary.Agent == "" || primary.Process.PID <= 0 || primary.Process.Start.IsZero() {
		return primary, "", registrationProblem("The CFO registration has incomplete identity")
	}
	for _, value := range append(herdrFields, primary.Agent) {
		if len(value) > 256 || strings.ContainsAny(value, "\x00\r\n") {
			return primary, "", registrationProblem("The CFO registration has an invalid identity")
		}
	}
	sum := sha256.Sum256(data)
	return primary, hex.EncodeToString(sum[:]), nil
}

// Register writes primary.json for the harness this process runs under: the
// program in the native terminal named by host.IDVariable, or else the
// foreground process of the Herdr pane named by HERDR_PANE_ID. Either must be
// one of this process's own ancestors, so an inherited variable cannot
// register someone else's terminal. That harness must also hold the home's
// session lock, taking it when nobody live does, because the primary CFO is
// whoever holds the home. harness names the agent when neither Herdr nor the
// program's own name says which it is. terminals opens the backend in its own
// session, which the caller reads from HERDR_SESSION the way spawn and the
// watcher do.
func Register(ctx context.Context, stateDir string, terminals terminal.Opener, harness, session string) (string, error) {
	var (
		primary   primaryRegistration
		ancestry  []proc.Entry
		harnessAt int
		err       error
	)
	if id := os.Getenv(host.IDVariable); id != "" && os.Getenv("HERDR_PANE_ID") == "" {
		primary, ancestry, harnessAt, err = nativeHarness(stateDir, id, harness)
	} else {
		primary, ancestry, harnessAt, err = herdrHarness(ctx, terminals, harness)
	}
	if err != nil {
		return "", err
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
	where := fmt.Sprintf("Herdr pane %s:%s", primary.Target.Session, primary.Target.Pane)
	if primary.Host != "" {
		where = "native terminal " + primary.Host
	}
	described := fmt.Sprintf("%s pid %d in %s", primary.Agent, process.PID, where)
	// primary.json's hash is the identity questions, reviews and answers are
	// bound to, so the same process in the same terminal keeps its bytes.
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

// nativeHarness proves this process runs under the program in native
// terminal id, whose host must answer, and names that program's harness:
// harness when the caller names it, or else the program's own name.
func nativeHarness(stateDir, id, harness string) (primaryRegistration, []proc.Entry, int, error) {
	record, err := host.ReadRecord(stateDir, id)
	if err != nil {
		return primaryRegistration{}, nil, 0, fmt.Errorf("native terminal %s has no host record: %w", id, err)
	}
	ancestry, err := proc.Ancestry(os.Getpid(), 32)
	if err != nil {
		return primaryRegistration{}, nil, 0, err
	}
	at := slices.IndexFunc(ancestry, func(entry proc.Entry) bool { return entry.PID == record.ChildPID })
	if at < 0 {
		return primaryRegistration{}, nil, 0, fmt.Errorf("native terminal %s runs pid %d, and this command does not run under it", id, record.ChildPID)
	}
	// A host that was killed leaves its record behind, so only an answer on
	// its pipe proves a host still serves the terminal.
	client, err := host.Dial(record)
	if err != nil {
		return primaryRegistration{}, nil, 0, fmt.Errorf("the host of native terminal %s does not answer: %w", id, err)
	}
	_ = client.Close()
	if harness == "" {
		harness = strings.TrimSuffix(strings.ToLower(ancestry[at].ExeBase), ".exe")
		if harness != "claude" && harness != "codex" && harness != "pi" {
			return primaryRegistration{}, nil, 0, fmt.Errorf("native terminal %s runs %s, which is not a harness the board delivers to", id, ancestry[at].ExeBase)
		}
	}
	return primaryRegistration{Host: id, Agent: harness}, ancestry, at, nil
}

// herdrHarness proves this process runs under the foreground process of the
// Herdr pane named by HERDR_PANE_ID and names the agent Herdr detects there.
func herdrHarness(ctx context.Context, terminals terminal.Opener, harness string) (primaryRegistration, []proc.Entry, int, error) {
	pane := os.Getenv("HERDR_PANE_ID")
	if pane == "" {
		return primaryRegistration{}, nil, 0, errors.New("this session runs in neither a Herdr pane nor a native terminal, so the board cannot reach it")
	}
	backend := terminals("")
	target := herdr.Target{Session: backend.EffectiveSession(), Pane: pane}
	ancestry, harnessAt, err := paneHarness(ctx, backend, target)
	if err != nil {
		return primaryRegistration{}, nil, 0, err
	}
	snapshot, err := backend.Snapshot(ctx)
	if err != nil {
		return primaryRegistration{}, nil, 0, fmt.Errorf("Herdr snapshot: %w", err)
	}
	primary := primaryRegistration{Target: target, Agent: harness}
	for _, p := range snapshot.Panes {
		if p.ID == pane {
			primary.Workspace, primary.Tab, primary.Terminal = p.WorkspaceID, p.TabID, p.TerminalID
		}
	}
	if primary.Terminal == "" {
		return primaryRegistration{}, nil, 0, fmt.Errorf("Herdr session %s does not list pane %s", target.Session, pane)
	}
	for _, agent := range snapshot.Agents {
		if agent.PaneID == pane && agent.Agent != "" {
			if harness != "" && agent.Agent != harness {
				return primaryRegistration{}, nil, 0, fmt.Errorf("Herdr detects %s in pane %s, not %s", agent.Agent, pane, harness)
			}
			primary.Agent = agent.Agent
		}
	}
	if primary.Agent == "" {
		return primaryRegistration{}, nil, 0, fmt.Errorf("Herdr has not detected the agent in pane %s yet; retry once it has", pane)
	}
	return primary, ancestry, harnessAt, nil
}

// paneHarness proves the calling process runs under the harness Herdr shows
// in the foreground of target, so a pane variable a process merely inherited
// never passes. It returns this process's ancestry and the harness's place in
// it.
func paneHarness(ctx context.Context, client terminal.Backend, target herdr.Target) ([]proc.Entry, int, error) {
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
	if primary.Host == "" && c.Terminals == nil {
		return errors.New("Native CFO transport is unavailable")
	}
	if !primary.Process.VerifiedAlive() {
		return registrationProblem(fmt.Sprintf("The registered CFO process is unavailable: pid %d, started %s, is no longer running", primary.Process.PID, primary.Process.Start.UTC().Format("2006-01-02 15:04 UTC")))
	}
	if primary.Host != "" {
		// The host's job ends its terminal's program with the host, so while
		// the program the record names runs, its host serves it.
		if record, err := host.ReadRecord(c.State, primary.Host); err != nil || record.ChildPID != primary.Process.PID {
			return registrationProblem("The registered CFO's native terminal " + primary.Host + " ended or runs another program")
		}
		return nil
	}
	client := c.Terminals(primary.Target.Session)
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
// a process in Herdr that is still running. It asks Herdr nothing, so neither
// a board that has not checked the registration yet nor a Herdr that cannot
// answer changes whether the launcher starts a CFO: only the registration
// does.
func LiveCFO(stateDir string) (herdr.Endpoint, bool) {
	primary, live := livePrimary(stateDir)
	if !live || primary.Host != "" {
		return herdr.Endpoint{}, false
	}
	return herdr.Endpoint{Target: primary.Target, WorkspaceID: primary.Workspace, TabID: primary.Tab, PaneID: primary.Target.Pane}, true
}

// NativeCFO returns the native terminal the registered CFO runs in, when
// primary.json names one and a process that is still running.
func NativeCFO(stateDir string) (string, bool) {
	primary, live := livePrimary(stateDir)
	return primary.Host, live && primary.Host != ""
}

func livePrimary(stateDir string) (primaryRegistration, bool) {
	file, err := openPrimary(filepath.Join(stateDir, "primary.json"))
	if err != nil {
		return primaryRegistration{}, false
	}
	defer file.Close()
	primary, _, err := decodePrimary(file)
	return primary, err == nil && primary.Process.VerifiedAlive()
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
	if primary.Host != "" {
		return c.sendNative(ctx, primary, text)
	}
	guard := func(ctx context.Context, target herdr.Target, agent herdr.AgentDetail) error {
		if target != primary.Target || agent.Agent != primary.Agent {
			return errors.New("primary CFO agent identity changed")
		}
		return c.verify(ctx, primary)
	}
	sender := fleet.Sender{Terminal: c.Terminals(""), Resolve: primaryResolver{c, primary}, Guard: guard}
	if err := sender.Text(ctx, "primary-cfo", oneLine("Overlord: "+text)); err != nil {
		if errors.Is(err, fleet.ErrQueuedBehindTurn) {
			return Evaluation{Reason: "Submitted to the registered CFO through Herdr while it was working; it takes the message when its current turn ends."}, nil
		}
		return Evaluation{}, err
	}
	return Evaluation{Reason: "Accepted by the registered CFO through Herdr."}, nil
}

// nativeSubmitSettle lets the CFO's harness take typed text before Enter
// submits it, as the Herdr sender waits.
const nativeSubmitSettle = 300 * time.Millisecond

// sendNative types text into the registered CFO's native terminal once and
// submits it. Nothing reports that the CFO took it until native hooks report
// its prompts, so a message that was typed is returned unconfirmed, and it is
// never typed again.
func (c *CFOConnection) sendNative(ctx context.Context, primary primaryRegistration, text string) (Evaluation, error) {
	if err := c.verify(ctx, primary); err != nil {
		return Evaluation{}, fmt.Errorf("%w: %v", ErrRejected, err)
	}
	record, err := host.ReadRecord(c.State, primary.Host)
	if err != nil {
		return Evaluation{}, fmt.Errorf("%w: %v", ErrRejected, err)
	}
	client, err := host.Dial(record)
	if err != nil {
		return Evaluation{}, fmt.Errorf("%w: the CFO's native terminal does not answer; nothing was sent", ErrRejected)
	}
	defer client.Close()
	if err := client.Input([]byte(oneLine("Overlord: " + text))); err != nil {
		return Evaluation{}, fmt.Errorf("the message may have reached the CFO's native terminal only in part: %w", err)
	}
	select {
	case <-time.After(nativeSubmitSettle):
	case <-ctx.Done():
		return Evaluation{}, fmt.Errorf("the message was typed into the CFO's native terminal but not submitted: %w", ctx.Err())
	}
	if err := client.Input([]byte("\r")); err != nil {
		return Evaluation{}, fmt.Errorf("the message was typed into the CFO's native terminal, and whether Enter reached it is unknown: %w", err)
	}
	return Evaluation{}, errors.New("the message was typed into the CFO's native terminal and submitted once; nothing confirms the CFO took it until native hooks report its prompts")
}
