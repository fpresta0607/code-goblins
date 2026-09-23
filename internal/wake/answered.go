package wake

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// answeredDir holds one file per blocking notify the Overlord answered on the
// board, named by the notify's sequence and holding the answer. It lives
// beside the queue rather than in it, so a record stays exactly what the
// goblin filed.
const answeredDir = ".wake-answered"

// MarkAnswered records that the Overlord answered the blocking notify at seq
// on the board. The goblin already has the answer, so the fleet stops
// treating the record as a question still owed one: the monitor no longer
// re-asks it and cfo drain acks it without --ack-blocking. Retiring the
// record is still the CFO's ordinary ack.
func MarkAnswered(dir string, seq int, answer string) error {
	if err := os.MkdirAll(filepath.Join(dir, answeredDir), 0o755); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(dir, answeredDir, strconv.Itoa(seq)), []byte(answer))
}

// attachAnswers fills Answered on the records the Overlord answered.
func attachAnswers(dir string, records []Record) ([]Record, error) {
	for i := range records {
		data, err := os.ReadFile(filepath.Join(dir, answeredDir, strconv.Itoa(records[i].Seq)))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		records[i].Answered = string(data)
	}
	return records, nil
}

// pruneAnswers drops the markers of records at or below an ack floor; the
// ack retired the record they annotate.
func pruneAnswers(dir string, floor int) {
	entries, err := os.ReadDir(filepath.Join(dir, answeredDir))
	if err != nil {
		return
	}
	for _, entry := range entries {
		if seq, err := strconv.Atoi(entry.Name()); err == nil && seq <= floor {
			_ = os.Remove(filepath.Join(dir, answeredDir, entry.Name()))
		}
	}
}
