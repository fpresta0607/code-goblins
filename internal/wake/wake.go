// Package wake owns the durable wake queue: sequenced records a watcher
// appends and a drain turn acknowledges, surviving restarts in between.
package wake

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/lock"
)

const queueFile = ".wake-queue"

// wakeLockName is the named lock every wake-state mutation (Append,
// AckThrough, PublishEpisode, AckEpisode) holds for its read-modify-write,
// serializing them across processes. Read-only paths (Pending, ReadEpisode)
// take no lock and create nothing, keeping INERT MEANS INERT intact.
// The lock is NOT reentrant. Calling a second wake mutator from inside the
// fn of one already in flight does not deadlock, because AcquireNamedOwner
// treats the same process re-acquiring as an idempotent self-match; instead
// the inner call's release drops the lock out from under the outer call,
// which then finishes its own read-modify-write believing it is still
// exclusive when it no longer is. A caller that needs several acks done
// together, such as cfo drain, must call them sequentially, one at a time,
// never nested.
const wakeLockName = ".wake-queue.lock"

// kinds is the whitelist Append enforces: upstream's four documented wake
// kinds, the `notify` kind cfo notify appends, and the `orphan` kind the
// reaper's sweep appends, and no others.
var kinds = map[string]bool{
	"signal":    true,
	"stale":     true,
	"check":     true,
	"heartbeat": true,
	"notify":    true,
	"orphan":    true,
}

// Record is one durable wake. Seq starts at 1 and is never reused; the ack
// floor only ever moves forward, matching upstream's --ack-through contract.
// Key identifies the subject of the wake (a goblin id, a window id, a
// heartbeat). It is NOT a dedup key and records are never folded: Render
// prints every unacknowledged record, omitting the `key: ` segment only when
// Key equals Kind, and Key is the identifying column in drain's blocked-notify
// refusal listing. Old lines without a key unmarshal with it empty.
type Record struct {
	Seq    int       `json:"seq"`
	Time   time.Time `json:"time"`
	Kind   string    `json:"kind"`
	Key    string    `json:"key"`
	Detail string    `json:"detail"`
	// Answered is the answer this blocking notify received outside the
	// queue, and AnsweredBy who gave it: AnsweredByOverlord on the board or
	// AnsweredByCFO with cfo answer. Pending attaches both from their own
	// marker; they are never written into the queue.
	Answered   string `json:"answered,omitempty"`
	AnsweredBy string `json:"answered_by,omitempty"`
}

// ackFile persists the highest acknowledged sequence so acked sequences stay
// retired even once the queue file empties.
const ackFile = ".wake-ack"

// withLock serializes a wake-state read-modify-write behind
// state/.wake-queue.lock. Contention (a live holder) is retried at 50ms up
// to 10 times (500ms total) before it is returned to the caller as an
// error rather than swallowed; a dead holder is stolen by the lock package
// itself, so a process killed inside fn cannot wedge the home.
func withLock(dir string, fn func() error) error {
	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		if _, err := lock.AcquireNamedOwner(dir, wakeLockName, os.Getpid(), "wake"); err != nil {
			if errors.Is(err, lock.ErrHeld) {
				lastErr = err
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return err
		}
		defer lock.ReleaseNamed(dir, wakeLockName)
		return fn()
	}
	return lastErr
}

func readAckFloor(dir string) (int, error) {
	data, err := os.ReadFile(filepath.Join(dir, ackFile))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var floor int
	if _, err := fmt.Sscanf(string(data), "%d", &floor); err != nil {
		return 0, fmt.Errorf("wake: unreadable ack floor: %w", err)
	}
	return floor, nil
}

func readAll(dir string) ([]Record, error) {
	lines, err := fsx.ReadLines(filepath.Join(dir, queueFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	records := make([]Record, 0, len(lines))
	for _, line := range lines {
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, fmt.Errorf("wake: corrupt queue line %q: %w", line, err)
		}
		records = append(records, rec)
	}
	return records, nil
}

func writeQueue(dir string, records []Record) error {
	var b []byte
	for _, r := range records {
		line, err := json.Marshal(r)
		if err != nil {
			return err
		}
		b = append(b, line...)
		b = append(b, '\n')
	}
	return fsx.AtomicWriteFile(filepath.Join(dir, queueFile), b)
}

// Append adds one record and returns it with its assigned sequence. kind
// must be one of signal, stale, check or heartbeat.
// Rewrite the entire queue atomically; O(n) is acceptable for small queue and
// single-writer, and gains AtomicWriteFile's bounded retry on Windows sharing locks.
func Append(dir, kind, key, detail string) (Record, error) {
	if !kinds[kind] {
		return Record{}, fmt.Errorf("wake: unknown kind %q, want one of signal, stale, check, heartbeat, notify, orphan", kind)
	}
	var rec Record
	err := withLock(dir, func() error {
		records, err := readAll(dir)
		if err != nil {
			return err
		}
		floor, err := readAckFloor(dir)
		if err != nil {
			return err
		}
		next := floor + 1
		if n := len(records); n > 0 {
			next = records[n-1].Seq + 1
		}
		rec = Record{Seq: next, Time: time.Now().UTC(), Kind: kind, Key: key, Detail: detail}
		records = append(records, rec)
		return writeQueue(dir, records)
	})
	return rec, err
}

// Pending returns every unacknowledged record in sequence order, each with
// the answer the Overlord gave it on the board, if any.
func Pending(dir string) ([]Record, error) {
	records, err := readAll(dir)
	if err != nil {
		return nil, err
	}
	return attachAnswers(dir, records)
}

// ackSequence returns the sequence a reader may safely acknowledge after
// being shown displayed, and reports false when displayed does not account
// for every record in records.
//
// The two arguments are the whole point: the ack sequence must come from what
// the reader was actually shown, never from the full set. Taking it from the
// full set while printing a narrowed one is exactly how this queue retired
// escalations nobody had read. Render passes the rows it wrote, so today the
// sets always match; the check is kept because the failure it prevents is
// silent, and a later change that narrows the listing again must break loudly
// here rather than quietly widen the ack.
func ackSequence(records, displayed []Record) (int, bool) {
	shown := make(map[int]struct{}, len(displayed))
	maxSeq := 0
	for _, rec := range displayed {
		shown[rec.Seq] = struct{}{}
		if rec.Seq > maxSeq {
			maxSeq = rec.Seq
		}
	}
	for _, rec := range records {
		if _, ok := shown[rec.Seq]; !ok {
			return 0, false
		}
	}
	return maxSeq, true
}

// AckThrough retires every record with Seq <= seq and advances the durable
// ack floor. Acking an already-empty or already-acked range is a no-op.
func AckThrough(dir string, seq int) error {
	return withLock(dir, func() error {
		records, err := readAll(dir)
		if err != nil {
			return err
		}
		kept := records[:0]
		for _, rec := range records {
			if rec.Seq > seq {
				kept = append(kept, rec)
			}
		}
		floor, err := readAckFloor(dir)
		if err != nil {
			return err
		}
		// Persist ack floor first; a crash between writes leaves acked records in queue
		// (harmless re-delivery) rather than an empty queue with a stale floor (sequence reuse).
		if seq > floor {
			floor = seq
			if err := fsx.AtomicWriteFile(filepath.Join(dir, ackFile), []byte(fmt.Sprintf("%d\n", floor))); err != nil {
				return err
			}
		}
		if err := writeQueue(dir, kept); err != nil {
			return err
		}
		pruneAnswers(dir, floor)
		return nil
	})
}

// Render writes the wake queue's presentation for records (RAW, unfolded)
// and ep to w: this is the shared renderer behind both `cfo drain` and the
// session-start digest's WAKE QUEUE section, so the two call sites can never
// drift in format. Both callers simply hand it whatever Pending/ReadEpisode
// returned.
//
// Every unacknowledged record is printed. Records are NOT folded: this
// renderer used to collapse them last-write-wins per (kind, key), which made
// a later notify from a task silently replace an earlier one - and because
// the ack sequence was taken from the raw maximum, the ack line it printed
// covered records it had just declined to show. Two escalations were retired
// unread that way, one of them a correction whose loss left the CFO ruling on
// superseded facts with nothing anywhere to flag it. The queue is an
// append-only log and this is its only display, so a record that is not
// printed is a record nobody will ever read.
//
// The printed rows and the ack sequence are therefore derived from ONE set:
// ackSequence takes the rows actually written out and refuses if they do not
// account for every record it was given. That asymmetry - printing one set
// and acking the maximum of another - is what let an ack line overreach what
// it displayed. If a future change filters the listing again, the tool
// withholds the ack line instead of printing one that overreaches.
//
// Apart from that withheld-ack line, which the loop below keeps unreachable,
// Render prints one of four output shapes: an empty queue with no pending
// episode (nothing further); an empty queue with a pending episode; a
// non-empty queue with a pending episode (the full listing plus a
// generation-qualified ack command); and a non-empty queue with NO pending
// episode (the listing plus a sequence-only ack command, with no
// --recovery-generation flag). The fourth shape is reachable by design, not
// only by hand-editing: a watcher appends a wake record before it calls
// PublishEpisode, so a watcher killed in that window (or a truncated
// .watcher-down marker, which ReadEpisode degrades to Pending: false) leaves
// queued records with no episode. That shape's ack line must never carry
// --recovery-generation 0: acking generation 0 against a home that never had
// an episode would fabricate one (see AckEpisode's guard).
func Render(w io.Writer, records []Record, ep Episode, now time.Time) error {
	if len(records) == 0 && !ep.Pending {
		_, err := fmt.Fprintln(w, "WAKE QUEUE: empty")
		return err
	}

	if _, err := fmt.Fprintf(w, "WAKE QUEUE: %d pending\n", len(records)); err != nil {
		return err
	}
	displayed := make([]Record, 0, len(records))
	for _, rec := range records {
		if verb, question, options, ok := decision(rec); ok {
			if err := renderDecision(w, rec, verb, question, options, now); err != nil {
				return err
			}
			displayed = append(displayed, rec)
			continue
		}
		line := fmt.Sprintf("  %d  %-6s  ", rec.Seq, rec.Kind)
		if rec.Key != rec.Kind {
			line += terminalText(rec.Key) + ": "
		}
		line += terminalText(rec.Detail)
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
		displayed = append(displayed, rec)
	}

	maxSeq, complete := ackSequence(records, displayed)
	// Unreachable today by construction: the loop above appends every row it
	// prints, so displayed always accounts for records. It is kept because the
	// failure it guards is silent - a filtered listing would print an ack line
	// covering records nobody read. TestAckSequenceRefusesToOutrunTheListing
	// asserts the premise by driving ackSequence with a narrowed listing.
	if !complete {
		_, err := fmt.Fprintln(w, "WAKE_ACK_WITHHELD: the listing above does not account for every unacknowledged record; acking now would retire records nobody has read")
		return err
	}

	if !ep.Pending {
		_, err := fmt.Fprintf(w, "WAKE_ACK_REQUIRED: cfo drain --ack-through %d\n", maxSeq)
		return err
	}

	if _, err := fmt.Fprintf(w, "RECOVERY EPISODE: pending, generation %d\n", ep.Gen); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "WAKE_ACK_REQUIRED: cfo drain --ack-through %d --recovery-generation %d\n", maxSeq, ep.Gen)
	return err
}

// BlockingNotify is one of the two arms of "a goblin is waiting on the CFO",
// and the only one the drain ack refusal consults: a goblin's own notify
// whose detail reports it blocked or failed, carrying a question only the CFO
// can answer. It returns the verb that parked it. An informational notify - a
// `done:` reporting a PR - matches neither arm and is not a decision.
//
// This is the single definition of that rule. Render prints exactly this set
// as a DECISION block, and `cfo drain` refuses to ack exactly this set unread;
// --ack-blocking's promise that the operator has read the question holds only
// while those two sets are the same one.
func BlockingNotify(rec Record) (string, bool) {
	if rec.Kind != "notify" {
		return "", false
	}
	for _, verb := range []string{"blocked", "failed"} {
		if strings.HasPrefix(rec.Detail, verb+":") {
			return verb, true
		}
	}
	return "", false
}

// InformationalNotify is the opposite of a question: a goblin's own notify
// reporting a terminal outcome nobody has read yet. It asks nothing, so a
// goblin holding one pending is finished rather than waiting, and the
// monitor suppresses its turn-ended stall while the record sits in the queue.
// A `failed:` notify is a question and belongs to BlockingNotify, not here.
//
// It reads the queue rather than the goblin's status file on purpose: a
// status line is the last thing the goblin ever wrote and stays `done:`
// forever, so a goblin steered back to work by `cfo send` would be silenced
// by a verb it wrote hours ago. Acking the notify is what the CFO does before
// steering it, so the pending record tracks the state the status line cannot.
func InformationalNotify(rec Record, id string) bool {
	return rec.Kind == "notify" && rec.Key == id && strings.HasPrefix(rec.Detail, "done:")
}

// DecisionSignal is the other arm: the watcher's own signal for a goblin,
// keyed by the status file that produced it. The watcher appends one only for
// a decision verb, so a needs-decision or checks-passed goblin - which never
// files a notify of its own - is waiting on the CFO through this record
// alone. The monitor's re-ask predicate consults it; the ack refusal does not,
// because the wake ack protocol retires questions the goblin itself asked.
func DecisionSignal(rec Record, id string) bool {
	return rec.Kind == "signal" && rec.Key == id+".status"
}

// stallAwaitingAnswer is the detail prefix the monitor writes for a goblin
// whose agent turn ended at its prompt. It is spelled out here for the same
// reason BlockingNotify spells out its verbs: the queue stores rendered text,
// and this package must read that text without importing the monitor.
const stallAwaitingAnswer = "awaiting_answer:"

// AwaitingAnswerStall is the third arm: the monitor's own stall record for a
// goblin whose turn ended waiting on input without filing a notify of its
// own. Such a goblin asked nothing formally, so the notify and signal arms
// both miss it - and an answer is owed all the same. That gap is how this
// class went quiet twice on 2026-09-18.
//
// Only the awaiting-answer stall counts. The monitor's own re-asks are stall
// records too, and counting them would make a goblin unanswered forever: the
// re-ask would be its own evidence, outliving the record it re-asked about.
func AwaitingAnswerStall(rec Record, id string) bool {
	return rec.Kind == "stale" && rec.Key == id && strings.HasPrefix(rec.Detail, stallAwaitingAnswer)
}

// Question is a blocked notify's question and the options it offered. A
// failed notify is still a decision for the CFO, but it is not a question.
func Question(rec Record) (string, []string, bool) {
	verb, question, options, ok := decision(rec)
	return question, options, ok && verb == "blocked"
}

// decision splits a blocking notify into the verb that parked it, the question
// it asked, and the options it offered.
//
// The options convention is one literal "options:" marker in the question,
// with the choices separated by "|", which is what
// `cfo notify <id> --blocked "<question> options: a | b"` produces. A question
// with no marker offered no options, and the rendering says so rather than
// inventing choices the goblin never named.
func decision(rec Record) (verb, question string, options []string, ok bool) {
	verb, ok = BlockingNotify(rec)
	if !ok {
		return "", "", nil, false
	}
	question, options = splitOptions(strings.TrimSpace(rec.Detail[len(verb)+1:]))
	return verb, question, options, true
}

func splitOptions(detail string) (string, []string) {
	// The marker is matched literally rather than by lowercasing the whole
	// string, because a case fold can change byte length and the index is
	// used to slice the original.
	const marker = "options:"
	at := strings.Index(detail, marker)
	if at < 0 {
		return detail, nil
	}
	var options []string
	for _, option := range strings.Split(detail[at+len(marker):], "|") {
		if option = strings.TrimSpace(option); option != "" {
			options = append(options, option)
		}
	}
	return strings.TrimSpace(detail[:at]), options
}

// renderDecision prints an outstanding decision as a decision rather than as
// one more status line. Waiting time leads because it is the field that was
// missing: on 2026-09-18 two goblins sat on a question for 8 hours 47 minutes
// and the Overlord noticed before the fleet did, which a listing that says
// how long each one has been waiting makes impossible to miss.
func renderDecision(w io.Writer, rec Record, verb, question string, options []string, now time.Time) error {
	if _, err := fmt.Fprintf(w, "  %d  DECISION  %s  %s, waiting %s\n",
		rec.Seq, terminalText(rec.Key), verb, waited(now.Sub(rec.Time))); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "       question: %s\n", terminalText(question)); err != nil {
		return err
	}
	if rec.Answered != "" {
		who := "the Overlord answered on the board"
		if rec.AnsweredBy == AnsweredByCFO {
			who = "the CFO answered with cfo answer"
		}
		_, err := fmt.Fprintf(w, "       answered: %s: %s; the goblin has it, so ack it normally\n", who, terminalText(rec.Answered))
		return err
	}
	if len(options) == 0 {
		_, err := fmt.Fprintf(w, "       options:  none offered; answer with `cfo send %s \"...\"`\n", terminalText(rec.Key))
		return err
	}
	for i, option := range options {
		label := "       options: "
		if i > 0 {
			label = "                "
		}
		if _, err := fmt.Fprintf(w, "%s %d) %s\n", label, i+1, terminalText(option)); err != nil {
			return err
		}
	}
	return nil
}

// waited renders how long a decision has gone unanswered, to the minute.
// Go's own Duration string keeps a trailing "0s" that buries the number the
// reader is looking for, and a record stamped in the future (clock skew, or a
// hand-edited queue) reads as no wait at all rather than as a negative one.
func waited(d time.Duration) string {
	if d < time.Minute {
		return "under a minute"
	}
	d = d.Round(time.Minute)
	if hours := int(d / time.Hour); hours > 0 {
		return fmt.Sprintf("%dh%02dm", hours, int(d/time.Minute)%60)
	}
	return fmt.Sprintf("%dm", int(d/time.Minute))
}

// terminalText preserves durable wake evidence while making controls visible
// in plain terminal output. Queue records remain raw for acknowledgement and
// programmatic consumers; only presentation uses this escape form.
func terminalText(value string) string {
	var out strings.Builder
	for _, char := range value {
		if unicode.IsControl(char) {
			fmt.Fprintf(&out, "\\u%04X", char)
			continue
		}
		out.WriteRune(char)
	}
	return out.String()
}
