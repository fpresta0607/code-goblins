// Package wake owns the durable wake queue: sequenced records a watcher
// appends and a drain turn acknowledges, surviving restarts in between.
package wake

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/lock"
)

const queueFile = ".wake-queue"

// wakeLockName is the named lock every wake-state mutation (Append,
// AckThrough, PublishEpisode, AckEpisode) holds for its read-modify-write,
// serializing them across processes. Read-only paths (Pending, ReadEpisode)
// take no lock and create nothing, keeping INERT MEANS INERT intact.
const wakeLockName = ".wake-queue.lock"

// kinds is the whitelist Append enforces: upstream's four documented wake
// kinds and no others.
var kinds = map[string]bool{
	"signal":    true,
	"stale":     true,
	"check":     true,
	"heartbeat": true,
}

// Record is one durable wake. Seq starts at 1 and is never reused; the ack
// floor only ever moves forward, matching upstream's --ack-through contract.
// Key identifies the thing the wake is about (a goblin id, a window id, a
// heartbeat) for drain-time dedup; old lines without a key unmarshal with it
// empty.
type Record struct {
	Seq    int       `json:"seq"`
	Time   time.Time `json:"time"`
	Kind   string    `json:"kind"`
	Key    string    `json:"key"`
	Detail string    `json:"detail"`
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
		return Record{}, fmt.Errorf("wake: unknown kind %q, want one of signal, stale, check, heartbeat", kind)
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

// Pending returns every unacknowledged record in sequence order.
func Pending(dir string) ([]Record, error) {
	return readAll(dir)
}

// Deduped folds records for presentation: last-write-wins per (kind, key),
// preserving first-seen order of surviving buckets. All heartbeat records
// collapse into one bucket regardless of key, since a heartbeat only ever
// tells the operator the watcher is alive, not which cycle emitted it.
func Deduped(records []Record) []Record {
	index := make(map[string]int, len(records))
	out := make([]Record, 0, len(records))
	for _, rec := range records {
		bucket := rec.Kind + "\x00" + rec.Key
		if rec.Kind == "heartbeat" {
			bucket = "heartbeat"
		}
		if i, ok := index[bucket]; ok {
			out[i] = rec
			continue
		}
		index[bucket] = len(out)
		out = append(out, rec)
	}
	return out
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
		return writeQueue(dir, kept)
	})
}
