// Package wake owns the durable wake queue: sequenced records a watcher
// appends and a drain turn acknowledges, surviving restarts in between.
package wake

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/goblinname"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const queueFile = ".wake-queue"

// wakeLockName is the named lock every wake-state mutation (Append,
// AckThrough, PublishEpisode, AckEpisode) holds for its read-modify-write,
// serializing them across processes. Read-only paths (Pending, ReadEpisode)
// take no lock and create nothing, keeping INERT MEANS INERT intact.
// The lock is NOT reentrant. A caller that needs several acks done
// together, such as cfo drain, must call them sequentially, one at a time,
// never nested.
const wakeLockName = ".wake-queue.lock"

// kinds is the whitelist Append enforces: upstream's four documented wake
// kinds, the `notify` kind cfo notify appends, the `orphan` kind the reaper's
// sweep appends, the `review` kind the supervisor appends when the Overlord
// answers a goblin's item or page, the `memory`, `ci` and `pr` kinds it
// appends when memory comes back for waiting work, CI finishes or cannot be
// read, and a pull request conflicts or falls behind, and the `disk` kind it
// appends when free disk falls under the mark the CFO is woken at, the
// `allowance` kind it appends when a subscription window nears its end or
// renews after it was used up, and no others.
var kinds = map[string]bool{
	"signal":    true,
	"stale":     true,
	"check":     true,
	"heartbeat": true,
	"notify":    true,
	"orphan":    true,
	"review":    true,
	"memory":    true,
	"ci":        true,
	"pr":        true,
	"disk":      true,
	"idle":      true,
	"allowance": true,
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
	Once   string    `json:"once,omitempty"`
	// Answered is the answer this blocking notify received outside the
	// queue, and AnsweredBy who gave it: AnsweredByOverlord on the board or
	// AnsweredByCFO with cfo answer. Pending attaches both from their own
	// marker; they are never written into the queue.
	Answered   string `json:"answered,omitempty"`
	AnsweredBy string `json:"answered_by,omitempty"`
	// Goblin is the name of the goblin a record is about, which Pending
	// attaches from its task's record so the listing names it as "Name
	// (id)". It is never written into the queue.
	Goblin string `json:"goblin,omitempty"`
}

// ackFile persists the highest acknowledged sequence so acked sequences stay
// retired even once the queue file empties.
const ackFile = ".wake-ack"

// lockBudget is how long a wake-state change waits for a live holder of
// state/.wake-queue.lock, whose read-modify-write a loaded machine can slow
// for seconds.
const lockBudget = 5 * time.Second

var mutationMutex sync.Mutex

// lockHeld runs each time this process takes the wake lock, for a test that
// counts how many holds one change of the queue costs.
var lockHeld = func() {}

// withLock serializes a wake-state read-modify-write behind
// state/.wake-queue.lock. The process-local mutex serializes goroutines,
// since the file lock accepts a same-process holder.
// A live holder is waited out within lockBudget, and past it the contention
// is returned to the caller rather than swallowed; a dead holder is stolen
// by the lock package itself, so a process killed inside fn cannot wedge the
// home.
func withLock(dir string, fn func() error) error {
	mutationMutex.Lock()
	defer mutationMutex.Unlock()

	if _, err := lock.AcquireNamedOwnerWithin(dir, wakeLockName, os.Getpid(), "wake", lockBudget); err != nil {
		return err
	}
	defer lock.ReleaseNamed(dir, wakeLockName)
	lockHeld()
	return fn()
}

func readAckFloor(dir string) (int, error) {
	data, err := fsx.ReadFile(filepath.Join(dir, ackFile))
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
	staged, err := stageQueue(dir, records)
	if err != nil {
		return err
	}
	return staged.Commit()
}

// stageQueue stages the queue file writeQueue would write.
func stageQueue(dir string, records []Record) (*fsx.Staged, error) {
	var b []byte
	for _, r := range records {
		line, err := json.Marshal(r)
		if err != nil {
			return nil, err
		}
		b = append(b, line...)
		b = append(b, '\n')
	}
	return fsx.Stage(filepath.Join(dir, queueFile), b)
}

// Append adds one record and returns it with its assigned sequence. kind
// must be one of the whitelisted kinds.
// Rewrite the entire queue atomically; O(n) is acceptable for small queue and
// single-writer, and gains AtomicWriteFile's bounded retry on Windows sharing locks.
func Append(dir, kind, key, detail string) (Record, error) {
	if !kinds[kind] {
		return Record{}, fmt.Errorf("wake: unknown kind %q, want one of signal, stale, check, heartbeat, notify, orphan, review, memory, ci, pr, disk, idle", kind)
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
	return attachAnswers(dir, attachGoblins(dir, records))
}

// attachGoblins names the goblin each record is about, from its live record
// or, once it finished, its outcome; a record about no named goblin keeps
// its bare key.
func attachGoblins(dir string, records []Record) []Record {
	names := map[string]string{}
	for i, rec := range records {
		name, known := names[rec.Key]
		if !known {
			if meta, err := state.ReadTaskMeta(dir, rec.Key); err == nil {
				name = meta.GoblinName
			} else if outcome, err := state.ReadOutcome(dir, rec.Key); err == nil {
				name = outcome.GoblinName
			}
			names[rec.Key] = name
		}
		records[i].Goblin = name
	}
	return records
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

// Acked reports whether seq is at or below the durable ack floor, so its
// record has been retired from the queue.
func Acked(dir string, seq int) (bool, error) {
	floor, err := readAckFloor(dir)
	if err != nil {
		return false, err
	}
	return seq > 0 && seq <= floor, nil
}

// AckThrough retires every record with Seq <= seq, a goblin's unanswered
// question included, and advances the durable ack floor. Acking an
// already-empty or already-acked range is a no-op.
func AckThrough(dir string, seq int) error {
	_, err := Acknowledge(dir, Ack{Through: &seq, ShouldRetireQuestions: true})
	return err
}

// Ack is what one acknowledgement asks of the wake queue.
type Ack struct {
	// Through retires every record at or below this sequence and advances
	// the durable ack floor to it. Nil retires nothing.
	Through *int
	// ShouldRetireQuestions retires the unanswered questions among those
	// records too. Without it one of them refuses the whole acknowledgement.
	ShouldRetireQuestions bool
	// Generation acknowledges the recovery episode pending at this
	// generation, once nothing is left queued, so a partial acknowledgement
	// never retires an episode whose records are still queued. Nil leaves
	// the episode alone.
	Generation *int
}

// Acknowledged is how an acknowledgement left the wake queue.
type Acknowledged struct {
	// Refused is every unanswered question at or below Ack.Through that
	// stopped the acknowledgement. When it lists one, nothing was changed
	// and the other fields are empty.
	Refused []Record
	// Pending is every record still queued, as Pending returns them, and
	// Episode the recovery episode, both as the acknowledgement left them.
	Pending []Record
	Episode Episode
	// HasGenerationMoved says the episode pending is not the one at
	// Ack.Generation, so it was left: the caller drains again.
	HasGenerationMoved bool
}

// Acknowledge retires the records and the recovery episode ack names in one
// hold of the wake lock, and returns what it left. cfo drain took the lock
// once for the records and again for the episode, and read the queue five
// times on the way.
//
// A blocked or failed notify is a goblin waiting on a CFO decision. Retiring
// it retires the only durable record of that question, so a drain whose
// output was cut short can bury it and leave the goblin parked forever: such
// a record refuses the acknowledgement unless ack says the CFO has read it,
// or the Overlord already answered it on the board. The refusal reads the
// records as they are queued, never a folded view: a later notify from the
// same goblin is no evidence that its question was answered.
//
// Every retired record's notice is kept before the lock is taken: on
// 2026-10-09 an acknowledgement of 55 records wrote its notices under it,
// held it for over a minute, and the supervisor's append was refused after
// its five seconds. A notice kept while its record is still queued changes
// nothing, since AppendFirst answers from either. Under the lock only a
// record queued since is left to keep.
//
// The files it leaves are staged before the lock too (stagedAck), and under
// the lock they are only renamed into place. Waiters queue for the lock, so
// whoever asks during an acknowledgement waits out its whole hold: with the
// ack floor, the queue and the episode each written under it, a notify behind
// one took 3.1 s at the median beside a cold build.
func Acknowledge(dir string, ack Ack) (Acknowledged, error) {
	var staged stagedAck
	defer staged.discard()
	noticed := map[string]bool{}
	var queued []Record
	if ack.Through != nil {
		var err error
		if queued, err = readAll(dir); err != nil {
			return Acknowledged{}, err
		}
		if refused, err := unreadQuestions(dir, queued, ack); err != nil || len(refused) > 0 {
			return Acknowledged{Refused: refused}, err
		}
		var retiring []Record
		for _, rec := range queued {
			if rec.Seq <= *ack.Through && rec.Once != "" {
				retiring = append(retiring, rec)
				noticed[rec.Once] = true
			}
		}
		if err := keepNoticed(dir, retiring); err != nil {
			return Acknowledged{}, err
		}
		if err := staged.records(dir, queued, *ack.Through); err != nil {
			return Acknowledged{}, err
		}
	}
	if ack.Generation != nil {
		var err error
		if staged.episode, err = stageEpisode(dir, "acked", *ack.Generation); err != nil {
			return Acknowledged{}, err
		}
	}
	var acknowledged Acknowledged
	err := withLock(dir, func() error {
		kept, err := readAll(dir)
		if err != nil {
			return err
		}
		if ack.Through != nil {
			if acknowledged.Refused, err = unreadQuestions(dir, kept, ack); err != nil || len(acknowledged.Refused) > 0 {
				return err
			}
			if kept, err = retireThrough(dir, kept, *ack.Through, noticed, queued, &staged); err != nil {
				return err
			}
		}
		episode, err := readEpisode(dir)
		if err != nil {
			return err
		}
		if ack.Generation != nil && len(kept) == 0 {
			if !pendingAt(episode, *ack.Generation) {
				acknowledged.HasGenerationMoved = true
			} else if err := staged.episode.Commit(); err != nil {
				return err
			} else {
				episode.Pending = false
			}
		}
		acknowledged.Pending, acknowledged.Episode = kept, episode
		return nil
	})
	if err != nil || len(acknowledged.Refused) > 0 {
		return Acknowledged{Refused: acknowledged.Refused}, err
	}
	// The answers of the records retired go once the lock is let go: nothing
	// reads the answer of a record no longer queued, and every file handled
	// under the lock is time another writer waits.
	if ack.Through != nil {
		pruneAnswers(dir, *ack.Through)
	}
	acknowledged.Pending, err = attachAnswers(dir, attachGoblins(dir, acknowledged.Pending))
	return acknowledged, err
}

// unreadQuestions returns the questions among records that ack would retire
// without the CFO having read them: none when it retires questions
// deliberately, else every blocking notify at or below its sequence that
// nobody answered.
func unreadQuestions(dir string, records []Record, ack Ack) ([]Record, error) {
	if ack.ShouldRetireQuestions {
		return nil, nil
	}
	var questions []Record
	for _, rec := range records {
		if _, isQuestion := BlockingNotify(rec); isQuestion && rec.Seq <= *ack.Through {
			questions = append(questions, rec)
		}
	}
	questions, err := attachAnswers(dir, questions)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(questions, func(rec Record) bool { return rec.Answered != "" }), nil
}

// stagedAck is what an acknowledgement staged before it took the wake lock:
// the ack floor at its sequence, the queue as it leaves it, and the episode
// acknowledged. Under the lock each is committed where it still holds, and
// what is left over is discarded.
type stagedAck struct {
	floor, queue, episode *fsx.Staged
}

// records stages the ack floor at seq and the queue as queued, read before
// the lock, leaves it once every record at or below seq is retired.
func (s *stagedAck) records(dir string, queued []Record, seq int) error {
	var err error
	if s.floor, err = fsx.Stage(filepath.Join(dir, ackFile), []byte(fmt.Sprintf("%d\n", seq))); err != nil {
		return err
	}
	s.queue, err = stageQueue(dir, slices.DeleteFunc(slices.Clone(queued), func(rec Record) bool { return rec.Seq <= seq }))
	return err
}

func (s *stagedAck) discard() {
	s.floor.Discard()
	s.queue.Discard()
	s.episode.Discard()
}

// retireThrough drops every record at or below seq from the queue, which the
// caller holds the wake lock for, and returns the ones it kept. A retired
// record whose identity is not in noticed was queued after the notices were
// kept, and its notice is kept here. The floor and the queue staged before
// the lock are committed: the queue only where it still is the one queued,
// read before the lock, since a record queued in between is not in it.
func retireThrough(dir string, records []Record, seq int, noticed map[string]bool, queued []Record, staged *stagedAck) ([]Record, error) {
	var kept, late []Record
	for _, rec := range records {
		switch {
		case rec.Seq > seq:
			kept = append(kept, rec)
		case rec.Once != "" && !noticed[rec.Once]:
			late = append(late, rec)
		}
	}
	if err := keepNoticed(dir, late); err != nil {
		return nil, err
	}
	floor, err := readAckFloor(dir)
	if err != nil {
		return nil, err
	}
	// Persist ack floor first; a crash between writes leaves acked records in queue
	// (harmless re-delivery) rather than an empty queue with a stale floor (sequence reuse).
	if seq > floor {
		if err := staged.floor.Commit(); err != nil {
			return nil, err
		}
	}
	isAsStaged := slices.EqualFunc(records, queued, func(now, then Record) bool { return now.Seq == then.Seq })
	if isAsStaged {
		err = staged.queue.Commit()
	} else {
		err = writeQueue(dir, kept)
	}
	if err != nil {
		return nil, err
	}
	return kept, nil
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
// Apart from that withheld-ack line, which only RenderWithin's bounded
// listing reaches, Render prints one of four output shapes: an empty queue with no pending
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
	_, err := RenderWithin(w, records, ep, now, math.MaxInt)
	return err
}

// RenderWithin is Render for a reader with room for only so many bytes: a
// hook's output reaches a session whole only up to a limit, and a listing the
// harness cut off would still have printed its ack line into the part nobody
// was handed. It prints the records that fit, oldest first and each one
// whole, and reports whether that was all of them. A listing that is not
// withholds the ack line and says how many records it left out, because the
// ack command retires every record at or below its sequence.
func RenderWithin(w io.Writer, records []Record, ep Episode, now time.Time, room int) (bool, error) {
	if len(records) == 0 && !ep.Pending {
		_, err := fmt.Fprintln(w, "WAKE QUEUE: empty")
		return true, err
	}

	header := fmt.Sprintf("WAKE QUEUE: %d pending\n", len(records))
	if _, err := io.WriteString(w, header); err != nil {
		return false, err
	}
	withheld := func(left int) string {
		return fmt.Sprintf("WAKE_ACK_WITHHELD: %d of the %d records are not listed above; run \"cfo drain\" to read every record and get the ack command, because acking from this listing would retire records nobody has read\n", left, len(records))
	}
	used := len(header) + len(withheld(len(records)))
	displayed := make([]Record, 0, len(records))
	for _, rec := range records {
		var row bytes.Buffer
		if err := renderRecord(&row, rec, now); err != nil {
			return false, err
		}
		if row.Len() > room-used {
			break
		}
		used += row.Len()
		if _, err := w.Write(row.Bytes()); err != nil {
			return false, err
		}
		displayed = append(displayed, rec)
	}

	// The ack sequence comes from the rows written out, never from the full
	// set: an ack line covering records nobody read is a silent failure.
	// TestAckSequenceRefusesToOutrunTheListing asserts the premise by driving
	// ackSequence with a narrowed listing.
	maxSeq, complete := ackSequence(records, displayed)
	if !complete {
		_, err := io.WriteString(w, withheld(len(records)-len(displayed)))
		return false, err
	}

	if !ep.Pending {
		_, err := fmt.Fprintf(w, "WAKE_ACK_REQUIRED: cfo drain --ack-through %d\n", maxSeq)
		return true, err
	}

	if _, err := fmt.Fprintf(w, "RECOVERY EPISODE: pending, generation %d\n", ep.Gen); err != nil {
		return false, err
	}
	_, err := fmt.Fprintf(w, "WAKE_ACK_REQUIRED: cfo drain --ack-through %d --recovery-generation %d\n", maxSeq, ep.Gen)
	return true, err
}

// renderRecord prints one record: a decision as its block, anything else as
// one line.
func renderRecord(w io.Writer, rec Record, now time.Time) error {
	if verb, question, options, ok := decision(rec); ok {
		return renderDecision(w, rec, verb, question, options, now)
	}
	line := fmt.Sprintf("  %d  %-6s  ", rec.Seq, rec.Kind)
	if rec.Key != rec.Kind {
		line += terminalText(goblinname.Called(rec.Goblin, rec.Key)) + ": "
	}
	line += terminalText(rec.Detail)
	_, err := fmt.Fprintln(w, line)
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

// stallAwaitingAnswer, stallGoblinIdle and stallGoblinAsks are the detail
// prefixes the monitor writes for a goblin whose agent turn ended at its
// prompt, for one that then sat there idle, and for one whose last reply
// asked in prose; proseAskQuote opens the question the last of them quotes,
// which ends its detail. They are spelled out here for the same reason
// BlockingNotify spells out its verbs: the queue stores rendered text, and
// this package must read that text without importing the monitor.
const (
	stallAwaitingAnswer = "awaiting_answer:"
	stallGoblinIdle     = "goblin_idle:"
	stallGoblinAsks     = "goblin_asks:"
	proseAskQuote       = `It asked: "`
)

// AwaitingAnswerStall is the third arm: the monitor's own stall record for a
// goblin whose turn ended waiting on input, that sat idle at its prompt, or
// that asked in prose, without filing a notify of its own. Such a goblin
// asked nothing formally, so the notify and signal arms both miss it - and an
// answer is owed all the same. That gap is how this class went quiet twice on
// 2026-09-18.
//
// Only those stalls count. The monitor's own re-asks are stall records too,
// and counting them would make a goblin unanswered forever: the re-ask would
// be its own evidence, outliving the record it re-asked about.
func AwaitingAnswerStall(rec Record, id string) bool {
	return rec.Kind == "stale" && rec.Key == id && (strings.HasPrefix(rec.Detail, stallAwaitingAnswer) || strings.HasPrefix(rec.Detail, stallGoblinIdle) || strings.HasPrefix(rec.Detail, stallGoblinAsks))
}

// ProseAsk is the question the monitor quoted for goblin id when its last
// reply asked the CFO something in prose instead of with a notify: the board
// shows that goblin waiting on the CFO with it, as it shows a blocked notify.
func ProseAsk(rec Record, id string) (string, bool) {
	if rec.Kind != "stale" || rec.Key != id || !strings.HasPrefix(rec.Detail, stallGoblinAsks) {
		return "", false
	}
	_, question, ok := strings.Cut(rec.Detail, proseAskQuote)
	return strings.TrimSuffix(question, `"`), ok
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
// `cfo notify <id> --blocked "<question> options: <answer> | <answer>"` produces. A question
// with no marker offered no options, and the rendering says so rather than
// inventing choices the goblin never named. The choices end the question, so
// they follow the last marker: one the goblin names earlier in its words,
// such as in a detail line, is part of the question.
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
	at := strings.LastIndex(detail, marker)
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
		rec.Seq, terminalText(goblinname.Called(rec.Goblin, rec.Key)), verb, waited(now.Sub(rec.Time))); err != nil {
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
