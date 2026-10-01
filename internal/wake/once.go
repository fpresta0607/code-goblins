package wake

import (
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
	if identity == "" || !kinds[kind] {
		return Record{}, errors.New("a notice identity and known wake kind are required")
	}
	var record Record
	err := withLock(directory, func() error {
		data, err := fsx.ReadFile(oncePath(directory, identity))
		if err == nil {
			if err := json.Unmarshal(data, &record); err != nil {
				return err
			}
			return sameNotice(record, identity, kind, key, detail)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		records, err := readAll(directory)
		if err != nil {
			return err
		}
		for _, prior := range records {
			if prior.Once == identity {
				record = prior
				return sameNotice(record, identity, kind, key, detail)
			}
		}
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
	return record, err
}

func sameNotice(record Record, identity, kind, key, detail string) error {
	if record.Once != identity || record.Kind != kind || record.Key != key || record.Detail != detail {
		return fmt.Errorf("wake notice %q already has different content", identity)
	}
	return nil
}

func oncePath(directory, identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return filepath.Join(directory, "wake-notices", hex.EncodeToString(sum[:])+".json")
}

// Acknowledgement preserves the identity before removing the queued record,
// so a caller retrying after a crash cannot deliver a completed action twice.
func keepOnce(directory string, record Record) error {
	path := oncePath(directory, record.Once)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, data)
}
