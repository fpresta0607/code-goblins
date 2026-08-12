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
)

const queueFile = ".wake-queue"

// Record is one durable wake. Seq starts at 1 and is never reused; the ack
// floor only ever moves forward, matching upstream's --ack-through contract.
type Record struct {
	Seq    int       `json:"seq"`
	Time   time.Time `json:"time"`
	Kind   string    `json:"kind"`
	Detail string    `json:"detail"`
}

// ackFile persists the highest acknowledged sequence so acked sequences stay
// retired even once the queue file empties.
const ackFile = ".wake-ack"

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

// Append adds one record and returns it with its assigned sequence.
// ponytail: single-writer sequencing (the watcher); add a lock file here if a
// second emitter ever appends concurrently.
// Rewrite the entire queue atomically; O(n) is acceptable for small queue and
// single-writer, and gains AtomicWriteFile's bounded retry on Windows sharing locks.
func Append(dir, kind, detail string) (Record, error) {
	records, err := readAll(dir)
	if err != nil {
		return Record{}, err
	}
	floor, err := readAckFloor(dir)
	if err != nil {
		return Record{}, err
	}
	next := floor + 1
	if n := len(records); n > 0 {
		next = records[n-1].Seq + 1
	}
	rec := Record{Seq: next, Time: time.Now().UTC(), Kind: kind, Detail: detail}
	records = append(records, rec)
	var b []byte
	for _, r := range records {
		line, err := json.Marshal(r)
		if err != nil {
			return Record{}, err
		}
		b = append(b, line...)
		b = append(b, '\n')
	}
	if err := fsx.AtomicWriteFile(filepath.Join(dir, queueFile), b); err != nil {
		return Record{}, err
	}
	return rec, nil
}

// Pending returns every unacknowledged record in sequence order.
func Pending(dir string) ([]Record, error) {
	return readAll(dir)
}

// AckThrough retires every record with Seq <= seq and advances the durable
// ack floor. Acking an already-empty or already-acked range is a no-op.
func AckThrough(dir string, seq int) error {
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
	var b []byte
	for _, rec := range kept {
		line, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		b = append(b, line...)
		b = append(b, '\n')
	}
	return fsx.AtomicWriteFile(filepath.Join(dir, queueFile), b)
}
