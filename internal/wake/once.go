package wake

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

func AppendOnce(directory, identity, kind, key, detail string) (Record, error) {
	record, isNew, err := AppendFirst(directory, identity, kind, key, detail)
	if err != nil || isNew {
		return record, err
	}
	return record, sameNotice(record, identity, kind, key, detail)
}

// AppendFirst appends a wake for identity unless one was appended for it
// before, still queued or already acknowledged, and reports whether it did.
// The record it returns is the first one, whatever this detail says.
func AppendFirst(directory, identity, kind, key, detail string) (Record, bool, error) {
	if identity == "" || !kinds[kind] {
		return Record{}, false, errors.New("a notice identity and known wake kind are required")
	}
	var record Record
	isNew := false
	err := withLock(directory, func() error {
		noticed, isNoticed, err := readNoticed(directory, identity)
		if err != nil || isNoticed {
			record = noticed
			return err
		}
		records, err := readAll(directory)
		if err != nil {
			return err
		}
		for _, prior := range records {
			if prior.Once == identity {
				record = prior
				return nil
			}
		}
		isNew = true
		floor, err := readAckFloor(directory)
		if err != nil {
			return err
		}
		next := floor + 1
		if len(records) > 0 {
			next = max(next, records[len(records)-1].Seq+1)
		}
		record = Record{Seq: next, Time: time.Now().UTC(), Kind: kind, Key: key, Detail: detail, Once: identity}
		return writeQueue(directory, append(records, record))
	})
	return record, isNew && err == nil, err
}

func sameNotice(record Record, identity, kind, key, detail string) error {
	if record.Once != identity || record.Kind != kind || record.Key != key || record.Detail != detail {
		return fmt.Errorf("wake notice %q already has different content", identity)
	}
	return nil
}

// noticedFile holds the notice of every acknowledged record that carries an
// identity, one JSON line each, so a caller retrying after a crash cannot
// deliver a completed action twice. An acknowledgement appends all of its
// notices in one write. Until 2026-10 each notice was a file of its own, and
// every new file is one the virus scanner reads: on a loaded machine an
// acknowledgement of 55 records spent 84 s writing them.
const noticedFile = ".wake-noticed"

// readNoticed returns the notice kept for identity, if one was.
func readNoticed(directory, identity string) (Record, bool, error) {
	data, err := fsx.ReadFile(filepath.Join(directory, noticedFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Record{}, false, err
	}
	// Only a line that spells the identity is decoded: the file keeps every
	// notice the home ever acknowledged.
	spelled, err := json.Marshal(identity)
	if err != nil {
		return Record{}, false, err
	}
	for line := range bytes.Lines(data) {
		var record Record
		// A line that does not decode was cut off by a machine that stopped
		// in the middle of its write, and is no notice.
		if bytes.Contains(line, spelled) && json.Unmarshal(line, &record) == nil && record.Once == identity {
			return record, true, nil
		}
	}
	// A build before the one file kept each notice in a file of its own, and
	// a home it acknowledged still holds them.
	sum := sha256.Sum256([]byte(identity))
	data, err = fsx.ReadFile(filepath.Join(directory, "wake-notices", hex.EncodeToString(sum[:])+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	var record Record
	return record, true, json.Unmarshal(data, &record)
}

// keepNoticed appends the notices of records in one write. Acknowledgement
// preserves each identity before removing its queued record. The write opens
// with a line break, so a line a stopped machine cut off stays a line of its
// own and costs no notice kept after it.
func keepNoticed(directory string, records []Record) error {
	if len(records) == 0 {
		return nil
	}
	lines := []byte{'\n'}
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			return err
		}
		lines = append(append(lines, line...), '\n')
	}
	file, err := fsx.OpenAppend(filepath.Join(directory, noticedFile), 0o600)
	if err != nil {
		return err
	}
	_, err = file.Write(lines)
	return errors.Join(err, file.Close())
}
