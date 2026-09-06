package reap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// RecordSchema versions the persisted audit, and RecordFile is where it lives.
// The dot keeps it out of state.ScanIDs alongside the rest of CFO's own
// bookkeeping.
const (
	RecordSchema = "cfo-reap.v1"
	RecordFile   = ".reap-audit.json"
)

// Record is one audit, persisted so a surface that must not shell out can
// still report orphans. The session-start digest is exactly that surface: it
// composes inside a 1s budget with no subprocess stage, so it reads the
// watcher's last sweep rather than taking its own.
type Record struct {
	Schema   string    `json:"schema"`
	Time     time.Time `json:"time"`
	Findings []Finding `json:"findings"`
	Notes    []string  `json:"notes,omitempty"`
	// Error is set when the sweep could not complete. An audit that failed is
	// itself worth reporting: a fleet nobody can see is not a clean fleet.
	Error string `json:"error,omitempty"`
	// Digest identifies the finding set, so a watcher can tell a new orphan
	// from the same orphan it already reported and wake the CFO only once.
	Digest string `json:"digest"`
}

// RecordPath is state/.reap-audit.json.
func RecordPath(stateDir string) string {
	return filepath.Join(stateDir, RecordFile)
}

// ReadRecord returns the last persisted audit. A missing file is os.ErrNotExist,
// which every caller treats as "no sweep has run yet" rather than a failure.
func ReadRecord(stateDir string) (Record, error) {
	data, err := os.ReadFile(RecordPath(stateDir))
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, fmt.Errorf("reap: decode audit record: %w", err)
	}
	if record.Schema != RecordSchema {
		return Record{}, fmt.Errorf("reap: unsupported audit record schema %q", record.Schema)
	}
	return record, nil
}

// WriteRecord persists one audit atomically.
func WriteRecord(stateDir string, record Record) error {
	record.Schema = RecordSchema
	record.Digest = FindingsDigest(record.Findings)
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(RecordPath(stateDir), append(data, '\n'))
}

// FindingsDigest is a stable fingerprint of a finding set: the same orphans in
// any order produce the same value, and one new orphan changes it. It is what
// keeps a persistent leak from waking the CFO on every watcher cycle while a
// genuinely new one still wakes it immediately.
func FindingsDigest(findings []Finding) string {
	keys := make([]string, 0, len(findings))
	for _, finding := range findings {
		keys = append(keys, fmt.Sprintf("%s|%s|%d|%s", finding.Class, finding.TaskID, finding.PID, normalizePath(finding.Path)))
	}
	sort.Strings(keys)
	sum := sha256.Sum256([]byte(strings.Join(keys, "\n")))
	return hex.EncodeToString(sum[:8])
}

// Actionable is the subset that means something is still running and spending
// resources. A worktree or a stale record can wait for the next session; an
// unsupervised harness cannot.
func Actionable(findings []Finding) []Finding {
	var out []Finding
	for _, finding := range findings {
		if finding.Class == OrphanProcess || finding.Class == StaleServer {
			out = append(out, finding)
		}
	}
	return out
}

// Summary is the one-line count used in wake details and digest headers.
func Summary(findings []Finding) string {
	if len(findings) == 0 {
		return "no orphans"
	}
	counts := make(map[Class]int, 5)
	for _, finding := range findings {
		counts[finding.Class]++
	}
	var parts []string
	for _, class := range []Class{OrphanProcess, StaleServer, OrphanWorktree, OrphanMeta, OrphanStatus} {
		if counts[class] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[class], class))
		}
	}
	return strings.Join(parts, ", ")
}

// statusListCap bounds how many orphan status logs are listed by name. A home
// that has been running for months accumulates hundreds of them, every line
// identical, and cleanup leaves them behind deliberately: they are history, not
// a leak anything is still spending on. Listing them all would bury the one
// unsupervised harness that actually matters, so they are counted instead. The
// JSON form and the persisted record still carry every one.
const statusListCap = 5

// Render writes the human report shared by cfo reap and the digest section, so
// the two presentations cannot drift.
func Render(w io.Writer, result Result) error {
	if _, err := fmt.Fprintln(w, "ORPHANS: "+Summary(result.Findings)); err != nil {
		return err
	}
	statusTotal := 0
	for _, finding := range result.Findings {
		if finding.Class == OrphanStatus {
			statusTotal++
		}
	}
	statusShown := 0
	for _, finding := range result.Findings {
		if finding.Class == OrphanStatus {
			if statusShown == statusListCap {
				statusShown++
				if _, err := fmt.Fprintf(w, "  (+%d more orphan status logs, all the same shape)\n", statusTotal-statusListCap); err != nil {
					return err
				}
			}
			if statusShown > statusListCap {
				continue
			}
			statusShown++
		}
		if _, err := fmt.Fprintln(w, "  "+finding.Line()); err != nil {
			return err
		}
	}
	for _, line := range result.Applied {
		if _, err := fmt.Fprintln(w, "  "+line); err != nil {
			return err
		}
	}
	for _, note := range result.Notes {
		if _, err := fmt.Fprintln(w, "  note: "+note); err != nil {
			return err
		}
	}
	return nil
}

// RenderRecord writes the persisted audit as the digest's ORPHANS section: the
// same lines Render produces, plus how old the sweep is, because a stale audit
// and a clean fleet must never read the same.
func RenderRecord(w io.Writer, stateDir string, now time.Time) error {
	record, err := ReadRecord(stateDir)
	if errors.Is(err, os.ErrNotExist) {
		_, err := fmt.Fprintln(w, "ORPHANS: no sweep recorded yet; run cfo reap")
		return err
	}
	if err != nil {
		_, err := fmt.Fprintf(w, "ORPHANS: UNREADABLE (%s)\n", err)
		return err
	}
	if record.Error != "" {
		if _, err := fmt.Fprintf(w, "ORPHANS: last sweep failed (%s)\n", record.Error); err != nil {
			return err
		}
	}
	if err := Render(w, Result{Findings: record.Findings, Notes: record.Notes}); err != nil {
		return err
	}
	age := now.Sub(record.Time).Round(time.Second)
	_, err = fmt.Fprintf(w, "  swept %s ago (%s); cfo reap re-runs it, cfo reap --apply acts on it\n", age, record.Time.UTC().Format(time.RFC3339))
	return err
}
