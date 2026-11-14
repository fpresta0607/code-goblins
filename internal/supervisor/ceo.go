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

	"github.com/fpresta0607/code-goblins/internal/awake"
	"github.com/fpresta0607/code-goblins/internal/custody"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/terminal"
)

type primaryRegistration struct {
	Target    herdr.Target `json:"target"`
	Workspace string       `json:"workspace"`
	Tab       string       `json:"tab"`
	Agent     string       `json:"agent"`
	Terminal  string       `json:"terminal"`
	// Host names the native terminal a CEO runs in, which leaves the Herdr
	// fields above empty.
	Host    string    `json:"host,omitempty"`
	Process lock.Info `json:"process"`
}

// registrationProblem is every way primary.json stops naming a reachable CEO.
// They share one fix, so every message carries it: the board shows it as one
// state instead of a generic delivery failure.
type registrationProblem string

func (p registrationProblem) Error() string {
	return string(p) + "; run goblins register in the CEO session"
}

const errNotRegistered = registrationProblem("The CEO is not registered")

// CEOConnection uses the primary.json registration Register writes.
// Reading it grants no permission to guess a different pane or register one.
type CEOConnection struct {
	State string
	// Terminals opens Herdr for the board's views of a terminal an older
	// build started there. Nothing is delivered or proven through it.
	Terminals terminal.Opener
	// ReadScreen reads a native terminal's console for a typed wake; nil
	// reads it through its host. Deliver submits a typed wake; nil delivers
	// it as goblins send does.
	ReadScreen func(host.Record) ([]string, error)
	Deliver    func(ctx context.Context, terminal state.TaskMeta, text string) error
	// typing lets one writer at a time read and type into the CEO's native
	// terminal, so a typed wake and a board send never share its composer.
	typing sync.Mutex
	// skipped is the newest untyped wake and why, as last recorded in
	// ceoWakeSkipsFile, so each is recorded once.
	skipped string
	// steadyWhy is the reason the CEO, not in a turn, was last not told, and
	// steadySince since when every look found that same reason.
	steadyWhy   string
	steadySince time.Time
}

func decodePrimary(reader io.Reader) (primaryRegistration, string, error) {
	var primary primaryRegistration
	data, err := io.ReadAll(io.LimitReader(reader, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return primary, "", registrationProblem("The CEO registration is unreadable or exceeds its limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&primary); err != nil {
		return primary, "", registrationProblem("The CEO registration is invalid")
	}
	herdrFields := []string{primary.Target.Session, primary.Target.Pane, primary.Workspace, primary.Tab, primary.Terminal}
	complete := !slices.Contains(herdrFields, "")
	if primary.Host != "" {
		complete = state.ValidTaskID(primary.Host) == nil && strings.Join(herdrFields, "") == ""
	}
	if decoder.Decode(new(json.RawMessage)) != io.EOF || !complete || primary.Agent == "" || primary.Process.PID <= 0 || primary.Process.Start.IsZero() {
		return primary, "", registrationProblem("The CEO registration has incomplete identity")
	}
	for _, value := range append(herdrFields, primary.Agent) {
		if len(value) > 256 || strings.ContainsAny(value, "\x00\r\n") {
			return primary, "", registrationProblem("The CEO registration has an invalid identity")
		}
	}
	sum := sha256.Sum256(data)
	return primary, hex.EncodeToString(sum[:]), nil
}

// Register writes primary.json for the harness of the CEO's own session,
// which this process must run in (OwnSession), and whose terminal's host
// must answer, since the board types into it. That harness holds the home's
// session lock from then on: it takes it, and takes it over, audited, from a
// live holder that is another process, because the session lock is the CEO's
// own session's alone. harness names the agent when the program's own name
// does not say which it is.
func Register(stateDir, harness, session string) (string, error) {
	primary, ancestry, harnessAt, err := nativeHarness(stateDir, harness)
	if err != nil {
		return "", err
	}
	// Plain goblins register names no session. Codex gives every command it runs
	// its thread, and pi 0.85.1 its session, which is the conversation a closed
	// CEO comes back on. Each reads only its own, since a CEO started inside
	// another harness's session inherits that one's.
	if session == "" {
		switch primary.Agent {
		case "codex":
			session = os.Getenv("CODEX_THREAD_ID")
		case "pi":
			session = os.Getenv("PI_SESSION_ID")
		}
	}
	// Custody is taken last, so a refused registration changes nothing.
	if _, _, err := custody.Take(stateDir, ancestry[harnessAt].PID, session, "goblins register"); err != nil {
		return "", fmt.Errorf("the home's session lock could not be taken, so this session is not registered: %w", err)
	}
	hostname, err := os.Hostname()
	if err != nil {
		return "", err
	}
	process := ancestry[harnessAt]
	described := fmt.Sprintf("%s pid %d in native terminal %s", primary.Agent, process.PID, primary.Host)
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
			return described, recordCEOConversation(stateDir, recorded, session)
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
	return described, recordCEOConversation(stateDir, primary, session)
}

// nativeHarness proves this process runs in the CEO's own session, whose
// terminal's host must answer, and names that session's harness: harness
// when the caller names it, or else the name the terminal's program runs.
func nativeHarness(stateDir, harness string) (primaryRegistration, []proc.Entry, int, error) {
	const id = NativeCEOTerminal
	ancestry, at, err := ownProgram(stateDir)
	if err == nil {
		err = terminalAnswers(stateDir, id)
	}
	if err != nil {
		return primaryRegistration{}, nil, 0, err
	}
	if harness == "" {
		program := ancestry[at].ExeBase
		harness = strings.TrimSuffix(strings.ToLower(program), ".exe")
		// Codex and pi install as npm script shims, which a native terminal
		// runs as cmd /c <path> (spawn.NativeProgram), so the base name of
		// the script cmd's own command line names is the harness.
		if harness == "cmd" {
			if identity, err := proc.Identify(ancestry[at].PID, ancestry[at].Start); err == nil && len(identity.Arguments) >= 3 && strings.EqualFold(identity.Arguments[1], "/c") {
				script := identity.Arguments[2]
				program += " /c " + script
				harness = strings.ToLower(strings.TrimSuffix(filepath.Base(script), filepath.Ext(script)))
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
	_, ancestry, at, err := terminalAncestry(stateDir, id)
	if err == nil {
		err = terminalAnswers(stateDir, id)
	}
	if err != nil {
		return nil, 0, err
	}
	return ancestry, at, nil
}

// terminalAnswers reports why the host of native terminal id does not serve
// it, or nil while it does. A host that was killed leaves its record behind,
// so only an answer on its pipe proves a host still serves the terminal.
func terminalAnswers(stateDir, id string) error {
	record, err := host.ReadRecord(stateDir, id)
	if err != nil {
		return fmt.Errorf("native terminal %s has no host record: %w", id, err)
	}
	client, err := host.Dial(record)
	if err != nil {
		return fmt.Errorf("the host of native terminal %s does not answer: %w", id, err)
	}
	_ = client.Close()
	return nil
}

// errHerdrRegistration is a registration an older build wrote for a CEO in a
// Herdr pane. Registering again from that pane is refused, so it names its
// own fix.
var errHerdrRegistration = errors.New("The CEO is registered in a Herdr pane, which this build cannot reach; start the CEO again in a native terminal")

// terminalProgram proves that a process whose environment is env runs in
// native terminal record, by the proof value its host put there, and returns
// the terminal's program. It is how a process whose chain of parents stops
// short of the program is proven, as a Cygwin or MSYS exec leaves one: Git
// Bash runs timeout by replacing its own Windows process, so `timeout 60 goblins
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

// verify reports why the registered CEO cannot be reached, or nil while its
// process runs as the program of the native terminal it registered in.
func (c *CEOConnection) verify(primary primaryRegistration) error {
	if primary.Host == "" {
		return errHerdrRegistration
	}
	if !primary.Process.VerifiedAlive() {
		return processGone(primary)
	}
	_, err := terminalLeft(c.State, primary)
	return err
}

// processGone is the problem of a registration whose process no longer runs.
func processGone(primary primaryRegistration) error {
	return registrationProblem(fmt.Sprintf("The registered CEO process is unavailable: pid %d, started %s, is no longer running", primary.Process.PID, primary.Process.Start.UTC().Format("2006-01-02 15:04 UTC")))
}

// terminalLeft is the problem of a native registration whose terminal no
// longer runs the registered process, or nil, with the record of the
// terminal's host, while it does. The host's job ends its terminal's program
// with the host, so while the program the record names runs, its host serves
// it.
func terminalLeft(stateDir string, primary primaryRegistration) (host.Record, error) {
	record, err := host.ReadRecord(stateDir, primary.Host)
	if err != nil || record.ChildPID != primary.Process.PID {
		return host.Record{}, registrationProblem("The registered CEO's native terminal " + primary.Host + " ended or runs another program")
	}
	return record, nil
}

// check reports why the board cannot reach the registered CEO right now, or
// nil when it can.
func (c *CEOConnection) check() error {
	_, err := c.examine()
	return err
}

// examine is check, and names the registration it examined: what it found
// stands for that registration alone, never for one written since.
func (c *CEOConnection) examine() (string, error) {
	file, err := openPrimary(filepath.Join(c.State, "primary.json"))
	if err != nil {
		return "", errNotRegistered
	}
	defer file.Close()
	primary, identity, err := decodePrimary(file)
	if err != nil {
		return "", err
	}
	return identity, c.verify(primary)
}

// LiveCEO returns the registered CEO's Herdr address when primary.json names
// a process in Herdr that is still running. It asks Herdr nothing, so neither
// a board that has not checked the registration yet nor a Herdr that cannot
// answer changes whether the launcher starts a CEO: only the registration
// does.
func LiveCEO(stateDir string) (herdr.Endpoint, bool) {
	primary, live := livePrimary(stateDir)
	if !live || primary.Host != "" {
		return herdr.Endpoint{}, false
	}
	return herdr.Endpoint{Target: primary.Target, WorkspaceID: primary.Workspace, TabID: primary.Tab, PaneID: primary.Target.Pane}, true
}

// NativeCEOTerminal is the native terminal goblins and the board's
// first-run page start the CEO in.
const NativeCEOTerminal = "ceo"

// CEOLaunchLock serializes starting the CEO, so two starts at once never
// start two CEOs, and an end a start meets while it holds the lock is that
// start's, never the automatic comeback's.
const CEOLaunchLock = ".ceo-launch.lock"

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

// CEORuns reports whether a CEO is registered and running, or native
// terminal ceo is up for one that has not registered yet.
func CEORuns(stateDir string) bool {
	ceo := readCEOState(stateDir)
	return ceo.registered || ceo.starting
}

// ceoState is the CEO as the board sees it, from one read of its
// registration and at most one dial of native terminal ceo.
type ceoState struct {
	// registered says a CEO is registered and running.
	registered bool
	// starting says native terminal ceo is up for a CEO that has not
	// registered yet: Claude Code registers through its SessionStart hook
	// after its onboarding and sign-in, in that terminal, and a Codex or pi
	// CEO when its first prompt runs goblins register.
	starting bool
	// terminal is the native terminal the board shows the CEO in: the one the
	// registered CEO names or, while it is starting, native terminal ceo,
	// where the Overlord may first have to answer it. It is empty while the
	// CEO runs in Herdr or not at all.
	terminal string
	// since is when the host of that terminal started, and zero when its
	// record cannot be read.
	since time.Time
	// harness is the harness the registered CEO runs, as it registered.
	harness string
	// session is the board's key of the registered CEO's native session,
	// harness/id, and empty when its registration names none.
	session string
	// identity is the fingerprint of the registration this read found, and
	// empty when it found none it could read.
	identity string
	// closed says the home's CEO registered and its process has since ended,
	// with no terminal up for a new one: he closed it, it crashed, or the
	// machine restarted. That is no problem to report, since nothing about
	// the registration is wrong: the board says the CEO is closed and offers
	// to reopen it, as goblins brings it back.
	closed bool
	// problem says why the board cannot reach the CEO this read found, with
	// the fix. It is empty while the board can, while the CEO is starting and
	// has not registered yet, and while the CEO is closed.
	problem string
}

func readCEOState(stateDir string) ceoState {
	primary, identity, err := readPrimary(stateDir)
	if err == nil && primary.Process.VerifiedAlive() {
		ceo := ceoState{registered: true, terminal: primary.Host, harness: primary.Agent, identity: identity}
		if session := ceoSession(stateDir, primary); session != "" {
			ceo.session = primary.Agent + "/" + session
		}
		if primary.Host != "" {
			record, err := terminalLeft(stateDir, primary)
			if err != nil {
				ceo.problem = err.Error()
			}
			ceo.since = record.Started
		}
		return ceo
	}
	if NativeTerminalRuns(stateDir, NativeCEOTerminal) {
		ceo := ceoState{starting: true, terminal: NativeCEOTerminal}
		if record, err := host.ReadRecord(stateDir, NativeCEOTerminal); err == nil {
			ceo.since = record.Started
		}
		return ceo
	}
	if err == nil {
		return ceoState{closed: true, identity: identity}
	}
	return ceoState{identity: identity, problem: err.Error()}
}

// NativeCEO returns the native terminal the registered CEO runs in, when
// primary.json names one and a process that is still running.
func NativeCEO(stateDir string) (string, bool) {
	primary, live := livePrimary(stateDir)
	return primary.Host, live && primary.Host != ""
}

func livePrimary(stateDir string) (primaryRegistration, bool) {
	primary, _, err := readPrimary(stateDir)
	return primary, err == nil && primary.Process.VerifiedAlive()
}

// RegisteredCEOSession is the session and harness of the registered CEO
// while it runs, which a goblin the supervisor starts, resumes or switches
// records as its parent, since no session of a CEO reaches that start. It is
// empty strings while no live CEO is registered.
func RegisteredCEOSession(stateDir string) (session, harness string) {
	primary, live := livePrimary(stateDir)
	if !live {
		return "", ""
	}
	return ceoSession(stateDir, primary), primary.Agent
}

// readPrimary reads primary.json once: the registration and its identity, or
// why it names no CEO.
func readPrimary(stateDir string) (primaryRegistration, string, error) {
	file, err := openPrimary(filepath.Join(stateDir, "primary.json"))
	if err != nil {
		return primaryRegistration{}, "", errNotRegistered
	}
	defer file.Close()
	return decodePrimary(file)
}

// oneLine keeps a board delivery on one line: it is typed into a terminal,
// where a line break would submit the words before it on their own.
func oneLine(text string) string {
	return strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(text)
}

// stampedForCEO is the line the board types to the primary CEO: it is the
// board speaking, behind the Overlord's stamp, which is what tells the fleet
// the line is his, unless it is a command for the harness itself, a line that
// starts with / or !, which the harness runs as it was written. The stamp is
// the words' own and every board delivery carries it; whether a screen the
// harness draws from the delivery is his to answer is the delivery's mark,
// which only his own message or answer leaves (the CEO, 2026-11-07).
func stampedForCEO(text string) string {
	line := oneLine(text)
	if strings.HasPrefix(line, "/") || strings.HasPrefix(line, "!") {
		return line
	}
	return "Overlord: " + line
}

// Send types text to the primary CEO as one of his own messages or answers,
// behind the Overlord's stamp, and leaves the terminal's mark of his input.
func (c *CEOConnection) Send(ctx context.Context, identity, text string) (Evaluation, error) {
	return c.sendNative(ctx, identity, stampedForCEO(text), false, true)
}

// SendNotice delivers a line the supervisor writes to the CEO, such as a
// review request or how a run item ended, behind the same Overlord's stamp
// the board speaks with. It is nobody's own input, so it clears the
// terminal's mark of his.
func (c *CEOConnection) SendNotice(ctx context.Context, identity, text string) (Evaluation, error) {
	return c.sendNative(ctx, identity, stampedForCEO(text), false, false)
}

// nativeSubmitSettle lets the CEO's harness take typed text before its submit
// key.
const nativeSubmitSettle = 300 * time.Millisecond

// claudeTypedUpTo is the longest line typed into a Claude Code CEO as keys.
// Claude Code 2.1.312 takes a longer burst of keys as a paste, and one it
// reads in parts as a paste of the first part and keys after it: on
// 2026-10-29 the live CEO, slow to read in a turn, read a board message of
// 1,512 bytes as a paste of its first 1,024 and keys after them, and took
// only those keys at its next tool call (#1740). A longer line goes as one
// bracketed paste, which it takes whole however it reads it. A shorter one
// stays keys, which it never takes as a paste: it typed 800 characters as
// keys, and a bracketed path alone becomes an image.
const claudeTypedUpTo = 512

// nativeConfirm bounds how long a delivery to an idle native CEO waits after
// submission to be shown taken before the delivery is left sent and awaiting
// that, in time the machine ran (awake), since a pause of the whole machine
// stops the CEO's harness too; nativeConfirmPoll spaces the looks.
const (
	nativeConfirm     = 5 * time.Second
	nativeConfirmPoll = 250 * time.Millisecond
)

// nativeShown bounds how long a delivery to the native CEO waits for the
// text it typed to show on the CEO's prompt line, in time the machine ran. A
// harness busy in a turn reads its input late: on 2026-11-10 the live CEO's
// Claude Code left typed input unread for more than three seconds again and
// again, and a delivery that gave up after five left its words on the prompt
// line unsubmitted, where they held back every message after them (#2306).
var nativeShown = 30 * time.Second

// ErrNotSent says a delivery typed its words into a terminal that never took
// them as submitted and took them back off its prompt line, so they reached
// nobody and none are left typed there: the message reads Not sent, with Send
// now and Cancel, rather than as one that may have arrived (#2306).
var ErrNotSent = errors.New("Not sent")

// sendNative types line, as it is, to the primary CEO identity names. It
// waits for an empty composer, types once and confirms the text before
// Enter. A prompt hook or a new working turn proves acceptance by an idle
// CEO once the text has left its prompt line, unless the screen shows a
// turn offering its queue, which holds the text as a turn under way does; a
// line submitted during a turn waits in the harness's own queue, as one
// typed into its terminal by hand does, and is taken at the CEO's next tool
// call, which only the CEO's own record of its conversation proves.
// With sendsNow, a line that queued into a turn gets the harness's own
// send-now key once, which interrupts the turn and has the CEO take it at
// once (the CEO, 2026-11-03, withdrawing the 2026-11-02 rule that
// interrupting stays the box's Stop). With his, the line is the Overlord's
// own board message, so the terminal keeps his mark for the screen the CEO's
// harness draws from it.
func (c *CEOConnection) sendNative(ctx context.Context, identity, line string, sendsNow, his bool) (Evaluation, error) {
	file, err := openPrimary(filepath.Join(c.State, "primary.json"))
	if err != nil {
		return Evaluation{}, fmt.Errorf("%w: %v", ErrRejected, errNotRegistered)
	}
	defer file.Close()
	primary, current, err := decodePrimary(file)
	if err != nil || current != identity {
		return Evaluation{}, fmt.Errorf("%w: the primary CEO changed; refresh before sending", ErrRejected)
	}
	if err := c.verify(primary); err != nil {
		return Evaluation{}, fmt.Errorf("%w: %v", ErrRejected, err)
	}
	record, err := host.ReadRecord(c.State, primary.Host)
	if err != nil {
		return Evaluation{}, fmt.Errorf("%w: %v", ErrRejected, err)
	}
	screens, readable := harness.NativeScreens(harness.Kind(primary.Agent))
	if !readable {
		return Evaluation{Reason: "The CEO's input cannot be read. Your answer is queued; nothing was sent."}, ErrDeferred
	}
	read := c.ReadScreen
	if read == nil {
		read = host.ReadScreen
	}
	c.typing.Lock()
	defer c.typing.Unlock()
	delivery, err := host.DialDelivery(record)
	if errors.Is(err, host.ErrNoDelivery) {
		return Evaluation{}, fmt.Errorf("%w: %v; start the CEO again so its terminal can confirm what the board sends", ErrRejected, err)
	}
	if err != nil {
		return Evaluation{}, fmt.Errorf("%w: the CEO's native terminal does not answer; nothing was sent", ErrRejected)
	}
	defer delivery.Close()
	if his {
		_ = delivery.MarkHis()
	}
	before, err := read(record)
	if err != nil {
		return Evaluation{Reason: "The CEO's input cannot be read. Your answer is queued; nothing was sent."}, ErrDeferred
	}
	if dialog, shown := screens.Dialog(before); shown {
		if dialog.EscapeHint == "" {
			return Evaluation{Reason: "The CEO has a dialog open. Your answer is queued; nothing was sent."}, ErrDeferred
		}
		if err := spawn.AnswerDialog(ctx, record, dialog, before); err != nil {
			return Evaluation{Reason: "The CEO's dialog has not closed. Your answer is queued; nothing was sent."}, ErrDeferred
		}
		before, err = read(record)
		if err != nil {
			return Evaluation{Reason: "The CEO's input cannot be read. Your answer is queued; nothing was sent."}, ErrDeferred
		}
	}
	// A live offer drawn above the composer takes a key of its own over an
	// empty one, so it is dismissed with its one Escape before anything is
	// typed, as a delivery to a goblin dismisses it. Rows that outlive the key
	// are history, which holds nothing back (#1556).
	_, dismissed, err := spawn.Service{ReadScreen: read}.DismissOffer(ctx, record, screens)
	if err != nil {
		return Evaluation{Reason: "The CEO's optional offer could not be dismissed. Your answer is queued; nothing was sent."}, ErrDeferred
	}
	if dismissed {
		// The terminal holds an Escape the next input follows within 100 ms
		// as the start of a longer key, so the answer waits a poll.
		select {
		case <-time.After(nativeConfirmPoll):
		case <-ctx.Done():
			return Evaluation{Reason: "The CEO's optional offer was dismissed. Your answer is queued; nothing was sent."}, ErrDeferred
		}
		if before, err = read(record); err != nil {
			return Evaluation{Reason: "The CEO's input cannot be read. Your answer is queued; nothing was sent."}, ErrDeferred
		}
	}
	busy := screens.IsWorking(before)
	if !screens.ComposerEmpty(before) || !busy && !screens.IsReady(before) {
		return Evaluation{Reason: "The CEO's input is not empty and ready. Your answer is queued; nothing was sent."}, ErrDeferred
	}
	instruction := oneLine(line)
	// A command waits on the board for the turn to end, as a goblin's does.
	// Codex and pi record none, and can drop one typed into a turn with
	// nothing to show it (#2193). Claude Code queues one, ahead of what is
	// typed after it, and its send-now key then takes that command alone, as
	// a turn of its own that leaves his message behind it queued: the CEO's
	// did with three ahead of his message on 2026-11-10 (#2332). A message
	// behind it can be sent now, which types it into the turn ahead of it:
	// holdCommand gives the command words of its own, and what is said here
	// is for the words queued behind it. A command the harness runs at once
	// beside a turn waits for nothing: he types it into a turn in the terminal
	// and it runs there, so the board types it into the turn too (the
	// Overlord, 2026-11-11: "cant type / commands into modern chat like
	// terminal", #2372).
	// Send now on a held command runs it at once with the harness's send-now
	// key, as a goblin's does (#1915): Claude Code's takes the command typed
	// into its turn's queue while nothing else waits there, and Codex's and
	// pi's Escape ends the turn first, so the command goes at the prompt line
	// that leaves.
	isHeldCommand := busy && isHarnessCommand(instruction) && !harness.RunsBesideTurn(harness.Kind(primary.Agent), instruction)
	readScreen := func(context.Context) ([]string, error) { return read(record) }
	press := func() error { return spawn.PressKeys(delivery.Write, screens.SendNow) }
	isSentNow := isHeldCommand && sendsNow && screens.SendNow != ""
	if isSentNow && screens.Queued == nil {
		if idle := spawn.InterruptTurn(ctx, readScreen, press, screens, before); idle != nil {
			before, busy, isHeldCommand = idle, false, false
		}
	}
	isRunNow := isSentNow && screens.Queued != nil && !screens.OffersQueue(before)
	if isHeldCommand && !isRunNow {
		sendNow := "Send now types it for the CEO to take at its next tool call."
		if screens.SendNow != "" {
			sendNow = "Send now has it take the message at once."
		}
		return Evaluation{Reason: "Queued until the CEO's turn ends. " + sendNow, CanSendNow: true, CommandSendsNow: screens.SendNow != ""}, ErrDeferred
	}
	typed := screens.Submittable(instruction)
	// A "?" typed at an empty prompt line opens Claude Code's and Codex's
	// shortcut help rather than writing itself, as the live CEO's Claude Code
	// did with the Overlord's lone "?" on 2026-11-02, so a line that starts with
	// one goes as a paste, which every harness writes as it is.
	isPasted := strings.HasPrefix(instruction, "?") || primary.Agent == "claude" && len(instruction) > claudeTypedUpTo && !isHarnessCommand(instruction)
	if isPasted {
		typed = herdr.CleanPaste("\x1b[200~" + typed + "\x1b[201~")
		instruction = strings.TrimSuffix(strings.TrimPrefix(typed, "\x1b[200~"), "\x1b[201~")
	}
	submitted := time.Now()
	if err := delivery.Write([]byte(typed)); err != nil {
		return Evaluation{}, fmt.Errorf("the message may have reached the CEO's native terminal only in part: %w", err)
	}
	// From here the CEO's prompt line holds the words, or will once its
	// harness reads them, so a delivery that ends with no submit key taken
	// takes them back off it: left there, they would join whatever is typed
	// next, or hold it back for good. It says they were not sent once it saw
	// them leave, and otherwise leaves them to be taken back when they show.
	// Words whose submit key went are watched for their take as well: a
	// harness that reads its keys late, as one on a machine short of memory
	// does, takes that key once the delivery gave up on it (#2541).
	waiting := &Awaiting{Host: primary.Host, Harness: primary.Agent, Since: submitted, Digest: monitor.TextDigest(instruction), Session: ceoSession(c.State, primary), Typed: instruction, Pasted: isPasted, IsHeld: busy}
	isSubmitted := false
	unsent := func(why string) (Evaluation, error) {
		if spawn.TakeBack(func() ([]string, error) { return read(record) }, delivery.Write, screens, instruction) {
			return Evaluation{}, fmt.Errorf("%w. %s It was taken back off the CEO's prompt line, so nothing of it is left typed there.", ErrNotSent, why)
		}
		if isSubmitted {
			return Evaluation{Awaiting: waiting, LeftTyped: waiting}, errors.New(why + " It is taken back off the CEO's prompt line if it shows there.")
		}
		return Evaluation{LeftTyped: waiting}, errors.New(why + " Nothing was submitted, and it is taken back off the CEO's prompt line if it shows there.")
	}
	shown, cancelShown := awake.WithTimeout(context.Background(), nativeShown)
	defer cancelShown()
	for {
		screen, err := read(record)
		if err != nil {
			return Evaluation{LeftTyped: waiting}, fmt.Errorf("the message was typed but the CEO's input cannot be read: %w", err)
		}
		if _, shown := screens.Dialog(screen); shown {
			return unsent("The answer was typed but a dialog opened before submission.")
		}
		if screens.Shows(screen, instruction) {
			break
		}
		if shown.Err() != nil {
			return unsent("The answer was typed but never showed in the CEO's input.")
		}
		select {
		case <-time.After(nativeConfirmPoll):
		case <-ctx.Done():
			return unsent("The answer was typed into the CEO's terminal, and the wait to see it there ended before it showed.")
		}
	}
	select {
	case <-time.After(nativeSubmitSettle):
	case <-ctx.Done():
		return unsent("The answer was typed into the CEO's terminal, and the delivery ended before its submit key.")
	}
	screen, err := read(record)
	if err != nil {
		return Evaluation{LeftTyped: waiting}, fmt.Errorf("the message was typed but the CEO's input cannot be read: %w", err)
	}
	// The submit key sends all the prompt line holds, so it goes only over
	// the board's words alone: on 2026-11-13 it sent a message of his with
	// words after it that the board never typed (#2541). Only a prompt line
	// the screen reads can show words beside them, so one it cannot read is
	// submitted on the words showing, as before.
	if _, shown := screens.Dialog(screen); shown || !screens.Shows(screen, instruction) || screens.ComposerHoldsText(screen) && !screens.ComposerHoldsPartOf(screen, instruction) {
		return Evaluation{LeftTyped: waiting}, errors.New("The CEO's input changed before submission. Inspect its terminal; nothing was submitted.")
	}
	submit := "\r"
	if screens.PasteTakesEnter {
		// End flushes Codex's paste burst without changing the text; otherwise
		// Enter can join that paste instead of submitting it. It is pressed
		// once: a Codex can take it late, its text still showing, and an Enter
		// pressed again would reach it after the answer was submitted.
		submit = "\x1b[F\r"
	}
	if err := delivery.Write([]byte(submit)); err != nil {
		return Evaluation{Awaiting: waiting}, fmt.Errorf("the message was typed into the CEO's native terminal, and whether its submit key reached it is unknown: %w", err)
	}
	isSubmitted = true
	// A Claude Code slow to read, as on a loaded machine, takes the submit key
	// as a new line and leaves the words on its prompt line unsent, in a turn
	// or not (the Overlord, 2026-11-13: "just queues the message and puts it
	// into the chat input, but doesn't actually fire the message and then
	// start the turn", #2541). The key is pressed again as often as its
	// harness's screens allow once two reads in a row show the words alone
	// over the row it added, as for a goblin: a screen is read a row at a
	// time, so one read can join the prompt row drawn before a redraw to the
	// rows under it drawn after, and show his words alone over that row though
	// words beside them came in the same redraw.
	atPress, presses, isOverNewRow := screen, 0, false
	pressAgain := func(screen []string) (bool, error) {
		wasOverNewRow := isOverNewRow
		isOverNewRow = screens.ComposerHoldsPartOf(screen, instruction) && screens.TookEnterAsNewLine(atPress, screen)
		if presses >= screens.Resubmits || !isOverNewRow || !wasOverNewRow {
			return false, nil
		}
		if err := delivery.Write([]byte(submit)); err != nil {
			return false, err
		}
		atPress, presses, isOverNewRow = screen, presses+1, false
		return true, nil
	}
	// A CEO in a turn takes the answer at its next tool call, and its prompt
	// hook runs as its harness queues the answer, before the CEO has it, so
	// only its own record of the conversation proves it taken: the answer is
	// sent once its submit key went, and settleDeliveries reads that record.
	if busy {
		// Only a command the CEO's harness runs at once beside a turn, or one
		// Send now runs at once with its send-now key, is typed into one, and
		// its prompt line is what shows it ran. One run beside the turn starts
		// no turn and can leave no record, and one the send-now key ran has
		// left the turn's queue once that line reads empty with no queue
		// offered (spawn.RanNow). One its harness queued after all, or still
		// holds queued, waits there as his words do, for the CEO's record to
		// show it taken.
		if isRunNow {
			ran, isPressed, err := spawn.RanNow(ctx, readScreen, press, screens, instruction)
			switch {
			case err != nil:
				return Evaluation{Awaiting: waiting}, fmt.Errorf("the CEO's send-now key could not be pressed: %w", err)
			case ran:
				return Evaluation{Reason: "Taken by the CEO in its native terminal, which ran your command at once."}, nil
			case isPressed:
				return Evaluation{Reason: sentNowInTurn, Awaiting: waiting}, nil
			}
			return Evaluation{Reason: heldByCEO, Awaiting: waiting}, nil
		}
		if isHarnessCommand(instruction) {
			if ran, _ := spawn.RanBesideTurn(ctx, readScreen, screens, instruction, screens.OffersQueue(before)); ran {
				return Evaluation{Reason: "Taken by the CEO in its native terminal, which ran your command beside its turn."}, nil
			}
			return Evaluation{Reason: heldByCEO, Awaiting: waiting}, nil
		}
		// Where a harness can take the submit key as a new line, as Claude
		// Code can, his words are in the turn's queue only once they leave its
		// prompt line, so words still alone on it once the wait for them to
		// show would have ended are taken back off it. Any other harness queued
		// them as the key went, as before: a Codex can take its key late with
		// them still showing.
		if screens.Resubmits > 0 {
			// A turn that reads the submit key only as its words are taken
			// back queues them before the keys that take them back reach it,
			// and Claude Code runs its prompt hook as it queues a line, so
			// words its hook reports by then are held in that turn (#2541).
			heldOrUnsent := func(why string) (Evaluation, error) {
				result, err := unsent(why)
				if isReported, hookErr := NativeHostPromptSince(c.State, primary.Host, submitted); hookErr == nil && isReported {
					return Evaluation{Reason: heldByCEO, Awaiting: waiting}, nil
				}
				return result, err
			}
			left, cancelLeft := awake.WithTimeout(context.Background(), nativeShown)
			defer cancelLeft()
			for {
				screen, err := read(record)
				if err != nil {
					return Evaluation{Awaiting: waiting}, fmt.Errorf("the message was submitted into the CEO's turn, and its input cannot be read: %w", err)
				}
				if !screens.ComposerHoldsPartOf(screen, instruction) {
					break
				}
				isPressed, err := pressAgain(screen)
				if err != nil {
					return Evaluation{Awaiting: waiting}, fmt.Errorf("the message was typed into the CEO's turn, and whether its submit key pressed again reached it is unknown: %w", err)
				}
				if !isPressed && left.Err() != nil {
					return heldOrUnsent("The message stayed on the CEO's prompt line after its submit key, so the CEO's turn never queued it.")
				}
				select {
				case <-time.After(nativeConfirmPoll):
				case <-ctx.Done():
					return heldOrUnsent("The message was typed into the CEO's turn, and the wait to see it leave the prompt line ended before it did.")
				}
			}
		}
		// A Send now he asked for interrupts the turn through the key the
		// CEO's own harness binds to that, as a goblin's does, once its
		// input line shows the answer gone into its queue and no screen of
		// its own is open over it, and never once the CEO's own record
		// proves it took the answer: a turn that ended as the answer was
		// typed and submitted leaves the busy read before typing stale, and
		// its harness starts a turn on the answer itself.
		if sendsNow && screens.SendNow != "" {
			for pressed := time.Now().Add(nativeConfirm); ; {
				if ceoConversationTook(ctx, c.State, *waiting) {
					return Evaluation{Reason: "Taken by the CEO in its native terminal, as its record showed."}, nil
				}
				screen, readErr := read(record)
				if readErr == nil && !screens.IsOpen(screen) && screens.ComposerEmpty(screen) {
					if err := spawn.PressKeys(delivery.Write, screens.SendNow); err != nil {
						return Evaluation{Awaiting: waiting}, fmt.Errorf("the CEO's send-now key could not be pressed: %w", err)
					}
					return Evaluation{Reason: sentNowInTurn, Awaiting: waiting}, nil
				}
				if time.Now().After(pressed) {
					break
				}
				select {
				case <-time.After(nativeConfirmPoll):
				case <-ctx.Done():
					return Evaluation{Awaiting: waiting}, fmt.Errorf("the CEO's send-now key was not pressed: %w", ctx.Err())
				}
			}
		}
		return Evaluation{Reason: heldByCEO, Awaiting: waiting}, nil
	}
	taken, cancelTaken := awake.WithTimeout(context.Background(), nativeConfirm)
	defer cancelTaken()
	// took is how an idle CEO's screen and prompt hook show it took the
	// words, and false while they show neither. A stop hook can open a turn
	// as the words reach it, which then only queues them, and Claude Code
	// runs its prompt hook as it queues a line, so a turn whose prompt line
	// offers its queue holds them, and the hook counts as their take only
	// once they have left the prompt line (#2541).
	took := func(screen []string) (Evaluation, bool) {
		if screens.IsWorking(screen) && screens.OffersQueue(screen) {
			waiting.IsHeld = true
			return Evaluation{Reason: heldByCEO, Awaiting: waiting}, true
		}
		if isReported, err := NativeHostPromptSince(c.State, primary.Host, submitted); err == nil && isReported && !screens.ComposerHoldsPartOf(screen, instruction) {
			return Evaluation{Reason: "Taken by the CEO in its native terminal, as its hook reported."}, true
		}
		if _, dialog := screens.Dialog(screen); !dialog && screens.IsWorking(screen) && screens.ComposerEmpty(screen) {
			return Evaluation{Reason: "Taken by the CEO in its native terminal, which started working on your answer."}, true
		}
		return Evaluation{}, false
	}
	for {
		screen, err := read(record)
		if err != nil {
			return Evaluation{Awaiting: waiting}, fmt.Errorf("the answer was submitted but the CEO's screen cannot be read: %w", err)
		}
		if result, isTaken := took(screen); isTaken {
			return result, nil
		}
		_, dialog := screens.Dialog(screen)
		if isHarnessCommand(instruction) && screens.TookCommand(screen, instruction) {
			return Evaluation{Reason: "Taken by the CEO in its native terminal, which ran your command."}, nil
		}
		isPressed, err := pressAgain(screen)
		if err != nil {
			return Evaluation{Awaiting: waiting}, fmt.Errorf("the message was typed into the CEO's native terminal, and whether its submit key pressed again reached it is unknown: %w", err)
		}
		if !isPressed && taken.Err() != nil {
			if dialog {
				return Evaluation{LeftTyped: waiting}, errors.New("The answer remains in the CEO's input without confirmation that it was submitted. Inspect its terminal before sending again.")
			}
			if !screens.IsWorking(screen) && !screens.ComposerEmpty(screen) {
				// Its submit key was not taken, so the words are taken back.
				// A harness that took the key late shows where they went or
				// reports the prompt by then, and has the answer after all;
				// a screen that cannot be read leaves that to its hook.
				left, failure := unsent("The answer remained in the CEO's input after its submit key.")
				screen, _ := read(record)
				if result, isTaken := took(screen); isTaken {
					return result, nil
				}
				return left, failure
			}
			return Evaluation{Reason: sentToCEO, Awaiting: waiting}, nil
		}
		select {
		case <-time.After(nativeConfirmPoll):
		case <-ctx.Done():
			return Evaluation{Awaiting: waiting}, fmt.Errorf("the message was typed into the CEO's native terminal and submitted, and the wait to see it taken ended: %w", ctx.Err())
		}
	}
}
