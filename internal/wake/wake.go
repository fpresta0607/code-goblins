// Package wake owns the durable wake queue: sequenced records a watcher
// appends and a drain turn acknowledges, surviving restarts in between.
package wake

import (
	"crypto/sha256"
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
	"github.com/fpresta0607/code-goblins/internal/state"
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
	EventID string    `json:"event_id,omitempty"`
	Seq     int       `json:"seq"`
	Time    time.Time `json:"time"`
	Kind    string    `json:"kind"`
	Key     string    `json:"key"`
	Detail  string    `json:"detail"`
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
		if _, err := lock.AcquireExclusiveNamed(dir, wakeLockName); err != nil {
			if errors.Is(err, lock.ErrHeld) {
				lastErr = err
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return err
		}
		defer lock.ReleaseExclusiveNamed(dir, wakeLockName)
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

// AcknowledgedThrough is the durable recipient outcome receipt written by
// guarded drain acknowledgement. Missing queue records alone are not a receipt.
func AcknowledgedThrough(dir string) (int, error) { return readAckFloor(dir) }

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
	return AppendOnce(dir, kind, key, detail, "")
}

// AppendOnce gives a persisted producer event an idempotent queue receipt.
// The latest acknowledged receipt per producer key is retained for crash replay.
func AppendOnce(dir, kind, key, detail, eventID string) (Record, error) {
	if !kinds[kind] {
		return Record{}, fmt.Errorf("wake: unknown kind %q, want one of signal, stale, check, heartbeat, notify, orphan", kind)
	}
	var rec Record
	err := withLock(dir, func() error {
		records, err := readAll(dir)
		if err != nil {
			return err
		}
		if eventID != "" {
			for _, existing := range records {
				if existing.EventID == eventID && existing.Kind == kind && existing.Key == key {
					rec = existing
					return nil
				}
			}
		}
		floor, err := readAckFloor(dir)
		if err != nil {
			return err
		}
		rec = nextRecord(records, floor, kind, key, detail, eventID)
		return writeQueue(dir, append(records, rec))
	})
	return rec, err
}

func nextRecord(records []Record, floor int, kind, key, detail, eventID string) Record {
	next := floor + 1
	if n := len(records); n > 0 {
		next = max(next, records[n-1].Seq+1)
	}
	return Record{EventID: eventID, Seq: next, Time: time.Now().UTC(), Kind: kind, Key: key, Detail: detail}
}

func Notify(dir, key, detail string) (Record, error) {
	if err := state.ValidTaskID(key); err != nil {
		return Record{}, err
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
		raw, eventID, err := currentNotifyEvent(dir, key, detail)
		if err != nil {
			return err
		}
		if raw != "" {
			if existing, found := notificationReceipt(dir, records, floor, key, detail, eventID); found {
				if existing.Seq > floor {
					rec = existing
					return ensureEpisodePending(dir)
				}
				raw = ""
			}
		}
		if raw == "" {
			if err := state.AppendStatus(dir, key, detail); err != nil {
				return err
			}
			_, eventID, err = currentNotifyEvent(dir, key, detail)
			if err != nil || eventID == "" {
				if err == nil {
					err = errors.New("wake: appended notification status is unavailable")
				}
				return err
			}
		}
		rec = nextRecord(records, floor, "notify", key, detail, eventID)
		if err := writeQueue(dir, append(records, rec)); err != nil {
			return err
		}
		return ensureEpisodePending(dir)
	})
	return rec, err
}

func currentNotifyEvent(dir, key, detail string) (string, string, error) {
	lines, err := state.TailStatus(dir, key, int(^uint(0)>>1))
	if err != nil || len(lines) == 0 {
		return "", "", err
	}
	raw := lines[len(lines)-1]
	_, event := state.SplitStatus(raw)
	if event != detail {
		return "", "", nil
	}
	occurrence := 0
	for _, line := range lines {
		if line == raw {
			occurrence++
		}
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", key, raw, occurrence)))
	return raw, fmt.Sprintf("notify:%x", sum[:16]), nil
}

func notificationReceipt(dir string, records []Record, floor int, key, detail, eventID string) (Record, bool) {
	for _, record := range records {
		if record.Kind == "notify" && record.Key == key && record.EventID == eventID {
			return record, true
		}
	}
	info, err := os.Stat(filepath.Join(dir, key+".status"))
	if err != nil {
		return Record{}, false
	}
	signalID := fmt.Sprintf("%s.status:%d:%d", key, info.Size(), info.ModTime().UnixNano())
	for _, record := range records {
		if record.Kind == "signal" && record.Key == key+".status" && record.Detail == detail && record.EventID == signalID {
			return record, record.Seq > floor
		}
	}
	return Record{}, false
}

func ensureEpisodePending(dir string) error {
	episode, err := readEpisode(dir)
	if err != nil || episode.Pending {
		return err
	}
	return writeEpisode(dir, "pending", episode.Gen+1)
}

// Pending returns every unacknowledged record in sequence order.
func Pending(dir string) ([]Record, error) {
	records, err := readAll(dir)
	if err != nil {
		return nil, err
	}
	floor, err := readAckFloor(dir)
	if err != nil {
		return nil, err
	}
	kept := records[:0]
	for _, record := range records {
		if record.Seq > floor {
			kept = append(kept, record)
		}
	}
	return kept, nil
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
		latest := map[string]int{}
		for _, rec := range records {
			if rec.EventID != "" && rec.Seq <= floor {
				latest[rec.Kind+"\x00"+rec.Key] = rec.Seq
			}
		}
		kept := records[:0]
		for _, rec := range records {
			if rec.Seq > floor || (rec.EventID != "" && latest[rec.Kind+"\x00"+rec.Key] == rec.Seq) {
				kept = append(kept, rec)
			}
		}
		return writeQueue(dir, kept)
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
func Render(w io.Writer, records []Record, ep Episode) error {
	if len(records) == 0 && !ep.Pending {
		_, err := fmt.Fprintln(w, "WAKE QUEUE: empty")
		return err
	}

	if _, err := fmt.Fprintf(w, "WAKE QUEUE: %d pending\n", len(records)); err != nil {
		return err
	}
	displayed := make([]Record, 0, len(records))
	for _, rec := range records {
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
