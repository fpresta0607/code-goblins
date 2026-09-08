package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// NormalizeStatusDetail replaces the control characters that would corrupt a
// status line's one-line-per-event grammar (or a wake detail) with spaces, so
// a caller-supplied value from a shell or a pane never injects extra lines.
func NormalizeStatusDetail(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
}

// AppendStatus appends one event to state/<id>.status, creating the log on
// first use, and stamps it with the UTC time it was recorded. Without the
// stamp the status log is the only durable per-task record of what happened -
// the wake queue is acknowledged away - and no elapsed time between two events
// can be recovered from it, so "decision requested" to "response applied" is
// unanswerable after the fact. Lines otherwise carry their own grammar; this
// layer adds nothing else. The open retries briefly because antivirus and
// indexer scans on Windows hold transient sharing locks.
func AppendStatus(dir, id, line string) error {
	line = time.Now().UTC().Format(time.RFC3339) + " " + line
	path := filepath.Join(dir, id+".status")
	var f *os.File
	var openErr error
	for attempt := 0; attempt < 10; attempt++ {
		if f, openErr = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); openErr == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if f == nil {
		return openErr
	}
	_, werr := f.WriteString(line + "\n")
	return errors.Join(werr, f.Close())
}

// TailStatus returns the last n lines of state/<id>.status. A missing log
// returns (nil, nil): "no status yet" is a real fleet state, not an error.
// ponytail: whole-file read; switch to a reverse block scan if logs outgrow
// the line caps a later plan ports.
func TailStatus(dir, id string, n int) ([]string, error) {
	lines, err := fsx.ReadLines(filepath.Join(dir, id+".status"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

// SplitStatus separates the stamp AppendStatus wrote from the event text, so
// a caller can measure the elapsed time between two events and a reader can
// match on the event alone. A line recorded before stamping returns the zero
// time and the line unchanged.
func SplitStatus(line string) (time.Time, string) {
	stamp, event, found := strings.Cut(strings.TrimSpace(line), " ")
	if !found {
		return time.Time{}, line
	}
	recorded, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return time.Time{}, line
	}
	return recorded, event
}
