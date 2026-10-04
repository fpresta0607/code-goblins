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
	"sync"
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
	// ReadScreen reads a native terminal's console for a typed wake; nil
	// reads it through its host. Deliver submits a typed wake; nil delivers
	// it as cfo send does.
	ReadScreen func(host.Record) ([]string, error)
	Deliver    func(ctx context.Context, terminal state.TaskMeta, text string) error
	// typing lets one writer at a time read and type into the CFO's native
	// terminal, so a typed wake and a board send never share its composer.
	typing sync.Mutex
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
	// Plain cfo register names no session. Codex gives every command it runs
	// its thread, which is the conversation a closed Codex CFO comes back on.
	if session == "" && primary.Agent == "codex" {
		session = os.Getenv("CODEX_THREAD_ID")
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
		recorded := current
		current.Process = lock.Info{}
		if err == nil && sameProcess && current == primary {
			return described, recordCFOConversation(stateDir, recorded, session)
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
	return described, recordCFOConversation(stateDir, primary, session)
}

// nativeHarness proves this process runs under the program in native
// terminal id, whose host must answer, and names that program's harness:
// harness when the caller names it, or else the name the program runs.
func nativeHarness(stateDir, id, harness string) (primaryRegistration, []proc.Entry, int, error) {
	ancestry, at, err := nativeProgram(stateDir, id)
	if err != nil {
		return primaryRegistration{}, nil, 0, err
	}
	if harness == "" {
		program := ancestry[at].ExeBase
		harness = strings.TrimSuffix(strings.ToLower(program), ".exe")
		// Codex and pi install as npm script shims, which a native terminal
		// runs as cmd /c <name> (spawn.NativeProgram), so cmd's own command
		// line names the harness.
		if harness == "cmd" {
			if identity, err := proc.Identify(ancestry[at].PID, ancestry[at].Start); err == nil && len(identity.Arguments) >= 3 && strings.EqualFold(identity.Arguments[1], "/c") {
				program += " /c " + identity.Arguments[2]
				harness = strings.ToLower(identity.Arguments[2])
			}
		}
		if harness != "claude" && harness != "codex" && harness != "pi" {
			return primaryRegistration{}, nil, 0, fmt.Errorf("native terminal %s runs %s, which is not a harness the board delivers to", id, program)
		}
	}
	return primaryRegistration{Host: id, Agent: harness}, ancestry, at, nil
}

// nativeProgram proves this process runs under the program in native
// terminal id, whose host must answer, and returns this process's ancestry
// with the program's place in it.
func nativeProgram(stateDir, id string) ([]proc.Entry, int, error) {
	record, err := host.ReadRecord(stateDir, id)
	if err != nil {
		return nil, 0, fmt.Errorf("native terminal %s has no host record: %w", id, err)
	}
	ancestry, err := proc.Ancestry(os.Getpid(), 32)
	if err != nil {
		return nil, 0, err
	}
	at := slices.IndexFunc(ancestry, func(entry proc.Entry) bool { return entry.PID == record.ChildPID })
	if at < 0 {
		program, err := terminalProgram(record, os.Environ())
		if err != nil || len(ancestry) == 0 {
			return nil, 0, fmt.Errorf("native terminal %s runs pid %d, and this command does not run under it", id, record.ChildPID)
		}
		ancestry, at = []proc.Entry{ancestry[0], program}, 1
	}
	// A host that was killed leaves its record behind, so only an answer on
	// its pipe proves a host still serves the terminal.
	client, err := host.Dial(record)
	if err != nil {
		return nil, 0, fmt.Errorf("the host of native terminal %s does not answer: %w", id, err)
	}
	_ = client.Close()
	return ancestry, at, nil
}

// terminalProgram proves that a process whose environment is env runs in
// native terminal record, by the proof value its host put there, and returns
// the terminal's program. It is how a process whose chain of parents stops
// short of the program is proven, as a Cygwin or MSYS exec leaves one: Git
// Bash runs timeout by replacing its own Windows process, so `timeout 60 cfo
// notify ...` has a parent that already exited, and the MSYS runtime breaks
// away from the terminal's job as well. Every descendant of the terminal
// inherits the value, so a process in a Herdr pane proves nothing by it: a
// Herdr server started from the terminal hands it to every pane it opens.
func terminalProgram(record host.Record, env []string) (proc.Entry, error) {
	if environmentValue(env, "HERDR_PANE_ID") != "" {
		return proc.Entry{}, errors.New("it runs in a Herdr pane, not in the terminal")
	}
	if environmentValue(env, host.IDVariable) != record.ID || !record.Proves(environmentValue(env, host.ProofVariable)) {
		return proc.Entry{}, errors.New("it carries no proof of the terminal")
	}
	program, err := proc.Ancestry(record.ChildPID, 1)
	if err != nil || len(program) == 0 || !program[0].Start.Equal(record.ChildStart) {
		return proc.Entry{}, fmt.Errorf("the terminal's program pid %d is not running", record.ChildPID)
	}
	return program[0], nil
}

// environmentValue is name's value in env, a process's environment.
func environmentValue(env []string, name string) string {
	for _, entry := range env {
		if key, value, ok := strings.Cut(entry, "="); ok && strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
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
		return processGone(primary)
	}
	if primary.Host != "" {
		return terminalLeft(c.State, primary)
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

// processGone is the problem of a registration whose process no longer runs.
func processGone(primary primaryRegistration) error {
	return registrationProblem(fmt.Sprintf("The registered CFO process is unavailable: pid %d, started %s, is no longer running", primary.Process.PID, primary.Process.Start.UTC().Format("2006-01-02 15:04 UTC")))
}

// terminalLeft is the problem of a native registration whose terminal no
// longer runs the registered process, or nil while it does. The host's job
// ends its terminal's program with the host, so while the program the record
// names runs, its host serves it.
func terminalLeft(stateDir string, primary primaryRegistration) error {
	if record, err := host.ReadRecord(stateDir, primary.Host); err != nil || record.ChildPID != primary.Process.PID {
		return registrationProblem("The registered CFO's native terminal " + primary.Host + " ended or runs another program")
	}
	return nil
}

// check reports why the board cannot reach the registered CFO right now, or
// nil when it can.
func (c *CFOConnection) check(ctx context.Context) error {
	_, err := c.examine(ctx)
	return err
}

// examine is check, and names the registration it examined: what it found
// stands for that registration alone, never for one written since.
func (c *CFOConnection) examine(ctx context.Context) (string, error) {
	file, err := openPrimary(filepath.Join(c.State, "primary.json"))
	if err != nil {
		return "", errNotRegistered
	}
	defer file.Close()
	primary, identity, err := decodePrimary(file)
	if err != nil {
		return "", err
	}
	return identity, c.verify(ctx, primary)
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

// NativeCFOTerminal is the native terminal goblins --native and the board's
// first-run page start the CFO in.
const NativeCFOTerminal = "cfo"

// NativeTerminalRuns reports whether native terminal id's host answers.
func NativeTerminalRuns(stateDir, id string) bool {
	record, err := host.ReadRecord(stateDir, id)
	if err != nil {
		return false
	}
	client, err := host.Dial(record)
	if err != nil {
		return false
	}
	_ = client.Close()
	return true
}

// CFORuns reports whether a CFO is registered and running, or native
// terminal cfo is up for one that has not registered yet.
func CFORuns(stateDir string) bool {
	cfo := readCFOState(stateDir)
	return cfo.registered || cfo.starting
}

// cfoState is the CFO as the board sees it, from one read of its
// registration and at most one dial of native terminal cfo.
type cfoState struct {
	// registered says a CFO is registered and running.
	registered bool
	// starting says native terminal cfo is up for a CFO that has not
	// registered yet: Claude Code registers through its SessionStart hook
	// after its onboarding and sign-in, in that terminal, and a Codex or pi
	// CFO when its first prompt runs cfo register.
	starting bool
	// terminal is the native terminal the board shows the CFO in: the one the
	// registered CFO names or, while it is starting, native terminal cfo,
	// where the Overlord may first have to answer it. It is empty while the
	// CFO runs in Herdr or not at all.
	terminal string
	// harness is the harness the registered CFO runs, as it registered.
	harness string
	// identity is the fingerprint of the registration this read found, and
	// empty when it found none it could read.
	identity string
	// closed says the home's CFO registered and its process has since ended,
	// with no terminal up for a new one: he closed it, it crashed, or the
	// machine restarted. That is no problem to report, since nothing about
	// the registration is wrong: the board says the CFO is closed and offers
	// to reopen it, as goblins brings it back.
	closed bool
	// problem says why the board cannot reach the CFO this read found, with
	// the fix. It is empty while the board can, while the CFO is starting and
	// has not registered yet, and while the CFO is closed.
	problem string
}

func readCFOState(stateDir string) cfoState {
	primary, identity, err := readPrimary(stateDir)
	if err == nil && primary.Process.VerifiedAlive() {
		cfo := cfoState{registered: true, terminal: primary.Host, harness: primary.Agent, identity: identity}
		if primary.Host != "" {
			if err := terminalLeft(stateDir, primary); err != nil {
				cfo.problem = err.Error()
			}
		}
		return cfo
	}
	if NativeTerminalRuns(stateDir, NativeCFOTerminal) {
		return cfoState{starting: true, terminal: NativeCFOTerminal}
	}
	if err == nil {
		return cfoState{closed: true, identity: identity}
	}
	return cfoState{identity: identity, problem: err.Error()}
}

// NativeCFO returns the native terminal the registered CFO runs in, when
// primary.json names one and a process that is still running.
func NativeCFO(stateDir string) (string, bool) {
	primary, live := livePrimary(stateDir)
	return primary.Host, live && primary.Host != ""
}

func livePrimary(stateDir string) (primaryRegistration, bool) {
	primary, _, err := readPrimary(stateDir)
	return primary, err == nil && primary.Process.VerifiedAlive()
}

// readPrimary reads primary.json once: the registration and its identity, or
// why it names no CFO.
func readPrimary(stateDir string) (primaryRegistration, string, error) {
	file, err := openPrimary(filepath.Join(stateDir, "primary.json"))
	if err != nil {
		return primaryRegistration{}, "", errNotRegistered
	}
	defer file.Close()
	return decodePrimary(file)
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
		return Evaluation{}, err
	}
	return Evaluation{Reason: "Accepted by the registered CFO through Herdr."}, nil
}

// nativeSubmitSettle lets the CFO's harness take typed text before Enter
// submits it, as the Herdr sender waits.
const nativeSubmitSettle = 300 * time.Millisecond

// nativeConfirm bounds how long a delivery to the native CFO waits after
// Enter for the CFO's hook to report it taken before the delivery is left
// sent and awaiting that report; nativeConfirmPoll spaces the looks.
const (
	nativeConfirm     = 5 * time.Second
	nativeConfirmPoll = 250 * time.Millisecond
)

// sendNative types text into the registered CFO's native terminal once and
// submits it, each part confirmed written by the terminal's host. It is
// delivered once the CFO's own prompt hook, naming the terminal it runs in,
// reports taking it. A CFO inside a turn, or one slow to start its next,
// reports only later, so a delivery not yet reported is sent and awaits the
// report, which settleDeliveries hears; it is no error, and it is never typed
// again.
func (c *CFOConnection) sendNative(ctx context.Context, primary primaryRegistration, text string) (Evaluation, error) {
	if err := c.verify(ctx, primary); err != nil {
		return Evaluation{}, fmt.Errorf("%w: %v", ErrRejected, err)
	}
	record, err := host.ReadRecord(c.State, primary.Host)
	if err != nil {
		return Evaluation{}, fmt.Errorf("%w: %v", ErrRejected, err)
	}
	delivery, err := host.DialDelivery(record)
	if errors.Is(err, host.ErrNoDelivery) {
		return Evaluation{}, fmt.Errorf("%w: %v; start the CFO again so its terminal can confirm what the board sends", ErrRejected, err)
	}
	if err != nil {
		return Evaluation{}, fmt.Errorf("%w: the CFO's native terminal does not answer; nothing was sent", ErrRejected)
	}
	defer delivery.Close()
	c.typing.Lock()
	defer c.typing.Unlock()
	submitted := time.Now()
	if err := delivery.Write([]byte(oneLine("Overlord: " + text))); err != nil {
		return Evaluation{}, fmt.Errorf("the message may have reached the CFO's native terminal only in part: %w", err)
	}
	select {
	case <-time.After(nativeSubmitSettle):
	case <-ctx.Done():
		return Evaluation{}, fmt.Errorf("the message was typed into the CFO's native terminal but not submitted: %w", ctx.Err())
	}
	if err := delivery.Write([]byte("\r")); err != nil {
		return Evaluation{}, fmt.Errorf("the message was typed into the CFO's native terminal, and whether Enter reached it is unknown: %w", err)
	}
	for deadline := time.Now().Add(nativeConfirm); ; {
		if taken, err := NativeHostPromptSince(c.State, primary.Host, submitted); err == nil && taken {
			return Evaluation{Reason: "Taken by the CFO in its native terminal, as its hook reported."}, nil
		}
		if time.Now().After(deadline) {
			return Evaluation{Reason: sentToCFO, Awaiting: &Awaiting{Host: primary.Host, Harness: primary.Agent, Since: submitted}}, nil
		}
		select {
		case <-time.After(nativeConfirmPoll):
		case <-ctx.Done():
			return Evaluation{}, fmt.Errorf("the message was typed into the CFO's native terminal and submitted, and the wait to see it taken ended: %w", ctx.Err())
		}
	}
}
