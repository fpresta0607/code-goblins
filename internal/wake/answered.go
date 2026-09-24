package wake

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// answeredDir holds one file per blocking notify answered outside the queue,
// named by the notify's sequence and holding who answered and the answer. It
// lives beside the queue rather than in it, so a record stays exactly what
// the goblin filed.
const answeredDir = ".wake-answered"

// AnsweredByOverlord and AnsweredByCFO name who answered a blocking notify:
// the Overlord on the board or the CFO with cfo answer.
const (
	AnsweredByOverlord = "overlord"
	AnsweredByCFO      = "cfo"
)

type answeredMarker struct {
	By     string `json:"by"`
	Answer string `json:"answer"`
}

// MarkAnswered records that by answered the blocking notify at seq. The
// goblin already has the answer, so the fleet stops treating the record as a
// question still owed one: the monitor no longer re-asks it and cfo drain
// acks it without --ack-blocking. Retiring the record is still the CFO's
// ordinary ack.
func MarkAnswered(dir string, seq int, by, answer string) error {
	if err := os.MkdirAll(filepath.Join(dir, answeredDir), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(answeredMarker{By: by, Answer: answer})
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(dir, answeredDir, strconv.Itoa(seq)), data)
}

// attachAnswers fills Answered and AnsweredBy on the records answered. A
// marker holding only the answer was written by a build where the Overlord
// was its only writer.
func attachAnswers(dir string, records []Record) ([]Record, error) {
	for i := range records {
		data, err := os.ReadFile(filepath.Join(dir, answeredDir, strconv.Itoa(records[i].Seq)))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var marker answeredMarker
		if json.Unmarshal(data, &marker) != nil || marker.By == "" {
			marker = answeredMarker{By: AnsweredByOverlord, Answer: string(data)}
		}
		records[i].Answered, records[i].AnsweredBy = marker.Answer, marker.By
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
