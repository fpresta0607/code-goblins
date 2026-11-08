// Package sessionstream is the fleet's one conversation record: an
// append-only event stream per session, which every harness's own transcript
// reader feeds and which the board, the family tree, a goblin's panel and the
// resume path all read.
//
// A session is one harness conversation of one goblin generation, or of the
// CEO. Its stream lives beside the identity record the fleet already kept for
// it, under state/conversations, so the fleet's records stay in one place and
// no daemon, database or dependency is added.
package sessionstream

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// The kinds of an event. A turn is the words and calls between one prompt and
// the end of the work they asked for, and every event of it names it.
const (
	// KindSession says the session began, and which harness and conversation
	// it is.
	KindSession = "session"
	// KindPrompt is the user's turn: the words typed into the session.
	KindPrompt = "prompt"
	// KindAssistant is the assistant's output, one text block as the harness
	// wrote it.
	KindAssistant = "text"
	// KindThinking is reasoning the harness recorded the words of.
	KindThinking = "thinking"
	// KindToolCall is a call the assistant made.
	KindToolCall = "tool_call"
	// KindToolResult is what came back from a call, or that it failed, was
	// denied or was interrupted.
	KindToolResult = "tool_result"
	// KindNotice is the fleet's own word to a session, never the user's.
	KindNotice = "notice"
	// KindCompaction is a compaction the harness recorded.
	KindCompaction = "compaction"
	// KindInterrupt is where the user stopped the session.
	KindInterrupt = "interrupt"
	// KindTurnState says the session's own report of the turn it names: that it
	// runs, while the harness has said nothing of its end.
	KindTurnState = "turn_state"
	// KindTurnEnd says the turn it names ended, and what ended it.
	KindTurnEnd = "turn_end"
	// KindSessionEnd says the harness session ended.
	KindSessionEnd = "session_end"
	// KindMissing says what the harness cannot provide for its session, so the
	// board shows that rather than inventing content.
	KindMissing = "missing"
	// KindRestated says the harness rewrote its own record behind the reader,
	// as pi's branch reset does, and carries the conversation it holds now, so
	// an append-only stream never shows a message the harness has dropped.
	KindRestated = "restated"
)

// The states a call ends in beside done, and what ends a turn.
const (
	CallRunning = "running"

	TurnSettled     = "settled"
	TurnInterrupted = "interrupted"
	TurnEnded       = "ended"
)

// Picture is an image a prompt or a call's result holds.
type Picture struct {
	ID        string `json:"id"`
	MediaType string `json:"media_type"`
	Bytes     int    `json:"bytes"`
}

// Edit is what a call that edited a file changed.
type Edit struct {
	Added   int      `json:"added"`
	Removed int      `json:"removed"`
	Diff    []string `json:"diff,omitempty"`
}

// Agent is the sub-agent a call started: its node in its goblin's tree and
// its type.
type Agent struct {
	Node string `json:"node"`
	Type string `json:"type"`
}

// PlanStep is one step of a plan a call wrote.
type PlanStep struct {
	Text   string `json:"text"`
	Status string `json:"status"`
}

// Event is one thing that happened in a session, in the order it happened.
//
// ID is stable for its life: a reader that derives the same events again from
// the same harness record derives the same IDs, so an append is idempotent
// and a replay writes nothing. Seq is the event's place in the stream. Place
// is the place in the session of the message it belongs to, Block its place
// in that message's list of blocks, and Turn the turn it belongs to.
type Event struct {
	ID    string    `json:"id"`
	Seq   int       `json:"seq"`
	At    time.Time `json:"at"`
	Kind  string    `json:"kind"`
	Turn  string    `json:"turn,omitempty"`
	Place int       `json:"place,omitempty"`
	// Message is the message the event belongs to, and Block its place in
	// that message: a reply is many events, one per block.
	Message string `json:"message,omitempty"`
	Block   int    `json:"block,omitempty"`

	Text    string `json:"text,omitempty"`
	Source  string `json:"source,omitempty"`
	Queued  bool   `json:"queued,omitempty"`
	Trigger string `json:"trigger,omitempty"`
	Output  string `json:"output,omitempty"`
	Errors  string `json:"errors,omitempty"`
	Model   string `json:"model,omitempty"`

	CallID   string     `json:"call_id,omitempty"`
	Tool     string     `json:"tool,omitempty"`
	Subject  string     `json:"subject,omitempty"`
	State    string     `json:"state,omitempty"`
	Result   string     `json:"result,omitempty"`
	Spilled  int64      `json:"spilled,omitempty"`
	Pictures []Picture  `json:"pictures,omitempty"`
	Edit     *Edit      `json:"edit,omitempty"`
	Agent    *Agent     `json:"agent,omitempty"`
	Plan     []PlanStep `json:"plan,omitempty"`

	// Missing names what the harness could not provide for this event, which
	// the board shows instead of inventing it, and Restated the conversation
	// the harness's record holds now, in the shape the adapter projects.
	Missing  string `json:"missing,omitempty"`
	Restated string `json:"restated,omitempty"`
}

// Identity is the session a stream belongs to: one goblin generation, or the
// CEO's session, in one harness conversation.
type Identity struct {
	// Task is the goblin's task, empty for the CEO.
	Task string
	// Generation is the goblin's spawn generation, empty for the CEO, whose
	// conversation is its own generation.
	Generation string
	Harness    string
	// Session is the harness's own conversation id.
	Session string
}

// streamPart is the shape an identity's parts take in a file name: an
// identity that does not match it could reach outside the conversations
// folder once substituted into a path.
var streamPart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

var syncHarness = map[string]string{"claude": "claude", "codex": "codex", "pi": "pi"}

// Check reports whether the identity names a session whose stream can be
// kept, and why not when it cannot.
func (id Identity) Check() error {
	if id.Task != "" {
		if err := validTask(id.Task); err != nil {
			return err
		}
	}
	if id.Generation != "" && !streamPart.MatchString(id.Generation) {
		return fmt.Errorf("sessionstream: generation %q names no session", id.Generation)
	}
	if _, ok := syncHarness[strings.ToLower(id.Harness)]; !ok {
		return fmt.Errorf("sessionstream: harness %q keeps no conversation the fleet reads", id.Harness)
	}
	if !streamPart.MatchString(id.Session) {
		return fmt.Errorf("sessionstream: session %q names no conversation", id.Session)
	}
	return nil
}

// Owner is the folder the identity's streams are kept in: the task's, or the
// CEO's.
func (id Identity) Owner() string {
	if id.Task == "" {
		return "cfo"
	}
	return id.Task
}

// Path is where the identity's stream lives under a home's state folder. A
// goblin's is kept under its generation, so a relaunch of the same generation
// continues one stream and a new generation starts another; the CEO's is kept
// by the conversation it is.
func (id Identity) Path(stateDir string) (string, error) {
	if err := id.Check(); err != nil {
		return "", err
	}
	owner := filepath.Join(stateDir, "conversations", id.Owner())
	if id.Generation != "" {
		return filepath.Join(owner, id.Generation, id.Session+".jsonl"), nil
	}
	return filepath.Join(owner, id.Session+".jsonl"), nil
}

// validTask is the state package's rule for a task id, kept here so this
// package reads no other fleet package and an identity is checked before any
// path is built from it.
func validTask(id string) error {
	if id == "" || len(id) > 64 || id[0] == '.' || id[len(id)-1] == '.' {
		return fmt.Errorf("sessionstream: task %q names no task", id)
	}
	for _, ch := range id {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '.' || ch == '_' || ch == '-' {
			continue
		}
		return fmt.Errorf("sessionstream: task %q has invalid character %q", id, ch)
	}
	return nil
}

// ErrNoStream is returned for a session no stream was kept for yet, which a
// board renders from the harness's own record as it did before streams
// existed.
var ErrNoStream = errors.New("sessionstream: no stream is kept for this session")

// ErrUnreadable is returned for a stream that is kept and cannot be read, so
// a reader reports it rather than showing an empty conversation.
var ErrUnreadable = errors.New("sessionstream: this session's stream cannot be read")

// MaxEvents bounds how many events one stream keeps, so a stream that runs on
// for weeks is compacted rather than grown without end.
const MaxEvents = 20000

// Read returns the events kept for id, in order. A session with no stream
// returns ErrNoStream. A stream that cannot be read returns ErrUnreadable,
// wrapping the cause, since a board must say so and never show it as an empty
// conversation. An entry a writer is still appending, which does not end in a
// newline, is passed over: it is read once its line ends.
func Read(stateDir string, id Identity) ([]Event, error) {
	path, err := id.Path(stateDir)
	if err != nil {
		return nil, err
	}
	data, err := fsx.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoStream
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreadable, err)
	}
	events, err := decode(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreadable, err)
	}
	if len(events) == 0 {
		return nil, ErrNoStream
	}
	return events, nil
}

// decode reads a stream's lines into events. A line that is not an event, and
// everything after it, is refused: a stream is one projection of one thing,
// and reading on past a damaged entry would invent a conversation it never
// held.
func decode(data []byte) ([]Event, error) {
	lines := bytes.Split(data, []byte("\n"))
	if last := len(lines) - 1; len(lines[last]) > 0 {
		// The newest entry is still being written.
		lines = lines[:last]
	}
	events := make([]Event, 0, len(lines))
	for at, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, fmt.Errorf("entry %d: %v", at+1, err)
		}
		if event.Kind == "" || event.ID == "" {
			return nil, fmt.Errorf("entry %d names no event", at+1)
		}
		events = append(events, event)
	}
	return events, nil
}

// Encode writes each event as one line, as a stream keeps them.
func Encode(events []Event) []byte {
	var buffer bytes.Buffer
	for _, event := range events {
		line, err := json.Marshal(event)
		if err != nil {
			continue
		}
		buffer.Write(line)
		buffer.WriteByte('\n')
	}
	return buffer.Bytes()
}

// EventID is the identity of one event: the same harness record read again
// derives the same id, so appending it twice writes one entry, and content
// that changed derives another, which a reader folds over the one before.
func EventID(id Identity, kind, message, callID string, block int, content ...string) string {
	sum := sha256.New()
	write := func(part string) {
		sum.Write([]byte(part))
		sum.Write([]byte{0})
	}
	write(id.Owner())
	write(id.Generation)
	write(strings.ToLower(id.Harness))
	write(id.Session)
	write(kind)
	write(message)
	write(callID)
	write(fmt.Sprint(block))
	for _, part := range content {
		write(part)
	}
	return hex.EncodeToString(sum.Sum(nil))[:32]
}

// key is what a later event of the same key replaces, or "" for an event that
// is never replaced: a turn's end and a session's own lines stand as written.
func (event Event) key() string {
	switch event.Kind {
	case KindTurnState, KindTurnEnd, KindSession, KindSessionEnd, KindMissing, KindRestated:
		return ""
	case KindToolCall, KindToolResult:
		return event.Message + "\x00call\x00" + event.CallID
	default:
		return event.Message + "\x00" + fmt.Sprint(event.Block)
	}
}

// Appender appends to one session's stream. It holds what it has already
// written, read from the stream the first time it appends, so a reader that
// derives a session's events again and again writes only what is new.
type Appender struct {
	mu     sync.Mutex
	id     Identity
	path   string
	seen   map[string]bool
	seq    int
	loaded bool
}

// Open returns the appender for id's stream under stateDir.
func Open(stateDir string, id Identity) (*Appender, error) {
	path, err := id.Path(stateDir)
	if err != nil {
		return nil, err
	}
	return &Appender{id: id, path: path, seen: map[string]bool{}}, nil
}

// load reads what the stream already holds, once: what it holds after that is
// what this appender wrote, which a compaction reads again itself.
func (a *Appender) load() error {
	if a.loaded {
		return nil
	}
	data, err := fsx.ReadFile(a.path)
	if errors.Is(err, os.ErrNotExist) {
		a.loaded = true
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnreadable, err)
	}
	events, err := decode(data)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnreadable, err)
	}
	for _, event := range events {
		a.seen[event.ID] = true
		if event.Seq >= a.seq {
			a.seq = event.Seq + 1
		}
	}
	a.loaded = true
	return nil
}

// Write appends the events of the stream that are not written yet, in the
// order given, and returns the events it wrote, each with its place in the
// stream, and whether the stream was compacted on the way.
//
// A stream past its bound is compacted first, keeping the newest event of each
// thing the session did, so it never grows without end and never loses what
// the session is now; a caller that reads the stream again after a compaction
// reads the whole of what it holds.
func (a *Appender) Write(events []Event) (written []Event, isCompacted bool, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.load(); err != nil {
		return nil, false, err
	}
	fresh := make([]Event, 0, len(events))
	for _, event := range events {
		if event.ID == "" || a.seen[event.ID] {
			continue
		}
		fresh = append(fresh, event)
	}
	if len(fresh) == 0 {
		return nil, false, nil
	}
	if a.seq+len(fresh) > MaxEvents {
		if err := a.compact(fresh); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	}
	for at := range fresh {
		fresh[at].Seq = a.seq + at
	}
	if err := a.append(fresh); err != nil {
		// A write cut short leaves entries whose place this appender no longer
		// knows, so the next write reads the stream again instead of trusting
		// what it last saw.
		a.loaded = false
		return nil, false, err
	}
	for _, event := range fresh {
		a.seen[event.ID] = true
	}
	a.seq += len(fresh)
	return fresh, false, nil
}

// append adds the events to the end of the stream, making its folder on the
// way, and after the stream's last whole entry: an entry a write cut short,
// or one another writer left behind, is trimmed first, so no append buries it
// mid-stream where every reader after would refuse the stream.
func (a *Appender) append(events []Event) error {
	if err := os.MkdirAll(filepath.Dir(a.path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(a.path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := trimTail(a.path, file); err != nil {
		return err
	}
	_, err = file.Write(Encode(events))
	return err
}

// trimTail cuts a stream back to its last ended line, so an append always
// begins at a whole entry.
func trimTail(path string, file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	if size == 0 {
		return nil
	}
	last := make([]byte, 1)
	if _, err := file.ReadAt(last, size-1); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil
	}
	buffer := make([]byte, 64<<10)
	for end := size - 1; end > 0; {
		start := max(0, end-int64(len(buffer)))
		count, err := file.ReadAt(buffer[:end-start], start)
		if at := bytes.LastIndexByte(buffer[:count], '\n'); at >= 0 {
			return os.Truncate(path, start+int64(at)+1)
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		end = start
	}
	return os.Truncate(path, 0)
}

// compact rewrites the stream with the newest event of each thing the session
// did, then the events of this read that are not among them, so a stream that
// reached its bound keeps what the session is now in the room of what it was.
// A stream still at its bound then keeps its newest half, from the first
// message it still holds whole, so a session that runs on for weeks costs a
// bounded file and its older history is read from the harness's own record.
func (a *Appender) compact(fresh []Event) error {
	data, err := fsx.ReadFile(a.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	events, err := decode(data)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnreadable, err)
	}
	latest := map[string]int{}
	for at, event := range events {
		if key := event.key(); key != "" {
			latest[key] = at
		}
	}
	kept := make([]Event, 0, len(events))
	for at, event := range events {
		if key := event.key(); key != "" && latest[key] != at {
			continue
		}
		kept = append(kept, event)
	}
	kept = append(kept, fresh...)
	if len(kept) > MaxEvents {
		kept = wholeFrom(kept, MaxEvents/2)
	}
	for at := range kept {
		kept[at].Seq = at
	}
	if err := fsx.AtomicWriteFile(a.path, Encode(kept)); err != nil {
		return err
	}
	a.seen = map[string]bool{}
	for _, event := range kept {
		a.seen[event.ID] = true
	}
	a.seq = len(kept)
	a.loaded = true
	return nil
}

// wholeFrom is the newest keep events of events, beginning at the first
// message the events still hold whole, so a stream never begins part way
// through a reply.
func wholeFrom(events []Event, keep int) []Event {
	start := max(0, len(events)-keep)
	for start > 0 && start < len(events) && events[start].Message == events[start-1].Message {
		start++
	}
	return events[start:]
}
