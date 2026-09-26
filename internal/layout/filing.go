package layout

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// StaleBriefAge is how long a brief may sit undispatched, with no queued
// backlog row waiting on it, before filing parks it. Past it the brief is no
// longer work about to start, and a board card saying Not started only hides
// the work that is.
const StaleBriefAge = 72 * time.Hour

// The folders filing moves task folders into, and its record of every move,
// slash-separated under the data folder.
const (
	FinishedDir = "archive/finished"
	ParkedDir   = "archive/parked"
	FilingLog   = "archive/filed.md"
)

// notTasks are the data folders the layout owns, which are never a task's
// folder whatever they hold.
var notTasks = []string{"projects", "memory", "archive"}

// Move is one task folder filing moves out of data/.
type Move struct {
	ID string
	// From and To are absolute paths.
	From, To string
	// Parked is a brief set aside before dispatch; otherwise the task
	// finished.
	Parked bool
	// Reason says in plain words why the folder moves.
	Reason string
	// Row is the backlog row parking adds under ## Parked, empty when a
	// parked row already names the task.
	Row string
}

// Plan returns what filing would do to h's data now, moving nothing. Each
// folder data/<id> holding a brief.md is one of three things, read from the
// fleet's own records:
//
//   - live: state/<id>.meta exists. It stays.
//   - finished: no metadata, and a status log or a state archive entry says
//     it was dispatched. It moves to archive/finished.
//   - never dispatched: nothing in state names it. Its brief is parked in
//     archive/parked once StaleBriefAge has passed since the brief last
//     changed, or as soon as a Parked backlog row names it.
//
// A folder an open backlog row still points at stays where it is, so a
// queued row that says "start from data/<id>/handoff.md" keeps working: a
// finished folder waits until nothing open refers to it, and a brief with a
// queued row is queued work, however old.
func Plan(h home.Home, now time.Time) ([]Move, error) {
	entries, err := os.ReadDir(h.Data)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	backlog, err := fleet.ReadBacklog(h)
	if err != nil {
		return nil, err
	}
	archived, err := os.ReadDir(filepath.Join(h.State, state.ArchiveDirName))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	var moves []Move
	for _, entry := range entries {
		id := entry.Name()
		if !entry.IsDir() || state.ValidTaskID(id) != nil || isLayoutFolder(id) {
			continue
		}
		brief, err := os.Stat(filepath.Join(h.Data, id, "brief.md"))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		live, err := exists(filepath.Join(h.State, id+".meta"))
		if err != nil {
			return nil, err
		}
		if live {
			continue
		}
		// The task's last record is its status log or its newest state
		// archive entry. A brief written after it is a new brief for the same
		// id rather than the finished task's, so it is not finished work.
		var lastRecord time.Time
		dispatched := false
		if info, err := os.Stat(filepath.Join(h.State, id+".status")); err == nil {
			dispatched, lastRecord = true, info.ModTime()
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		for _, a := range archived {
			if !strings.HasPrefix(strings.ToLower(a.Name()), strings.ToLower(id)+".") {
				continue
			}
			info, err := a.Info()
			if err != nil {
				return nil, err
			}
			dispatched = true
			if info.ModTime().After(lastRecord) {
				lastRecord = info.ModTime()
			}
		}
		if brief.ModTime().After(lastRecord) {
			dispatched = false
		}
		queued, parked := mentions(backlog.Queued, id), mentions(backlog.Parked, id)
		var move Move
		switch {
		case dispatched && !queued && !parked:
			move = Move{ID: id, Reason: "finished"}
		case !dispatched && !queued && (parked || now.Sub(brief.ModTime()) >= StaleBriefAge):
			since := brief.ModTime().UTC().Format("2006-01-02")
			move = Move{ID: id, Parked: true, Reason: "brief not dispatched since " + since}
			if !parked {
				move.Row = fmt.Sprintf("- [ ] %s - Brief not dispatched since %s, parked %s: data/%s/%s/brief.md", id, since, now.UTC().Format("2006-01-02"), ParkedDir, id)
			}
		default:
			continue
		}
		dir := FinishedDir
		if move.Parked {
			dir = ParkedDir
		}
		move.From = filepath.Join(h.Data, id)
		if move.To, err = freeTarget(filepath.Join(h.Data, filepath.FromSlash(dir), id), now); err != nil {
			return nil, err
		}
		moves = append(moves, move)
	}
	return moves, nil
}

// Apply makes moves in order: it moves each folder whole, adds its parked
// row, and records the move in the filing log. It stops at the first move
// that fails and returns the ones it made, so a folder a process still holds
// open stays exactly where it was for the next pass.
func Apply(h home.Home, moves []Move, now time.Time) ([]Move, error) {
	var done []Move
	for _, move := range moves {
		if err := os.MkdirAll(filepath.Dir(move.To), 0o755); err != nil {
			return done, err
		}
		if err := os.Rename(move.From, move.To); err != nil {
			return done, fmt.Errorf("file %s: %w", move.ID, err)
		}
		done = append(done, move)
		if move.Row != "" {
			if err := addParkedRow(filepath.Join(h.Data, Backlog), move.Row); err != nil {
				return done, fmt.Errorf("record %s as parked in the backlog: %w", move.ID, err)
			}
		}
		if err := appendLog(filepath.Join(h.Data, filepath.FromSlash(FilingLog)), logLine(h, move, now)); err != nil {
			return done, err
		}
	}
	return done, nil
}

// logLine is the filing log's record of one move.
func logLine(h home.Home, move Move, now time.Time) string {
	verb := "archived"
	if move.Parked {
		verb = "parked"
	}
	return fmt.Sprintf("- %s %s %s (%s): %s -> %s\n", now.UTC().Format(time.RFC3339), verb, move.ID, move.Reason, dataPath(h, move.From), dataPath(h, move.To))
}

// File files a laid-out home's data now: see Plan and Apply. A home without
// the layout marker is left alone, because reorganising it is the migration
// its owner decides on. A pass that fails is recorded in the filing log,
// once until the failure changes, so a folder a process holds open shows
// there instead of being retried in silence.
func File(h home.Home, now time.Time) ([]Move, error) {
	laidOut, err := exists(filepath.Join(h.Data, Marker))
	if err != nil || !laidOut {
		return nil, err
	}
	moves, err := Plan(h, now)
	if err == nil {
		moves, err = Apply(h, moves, now)
	}
	if err != nil {
		err = errors.Join(err, recordFailure(h, now, err))
	}
	return moves, err
}

func recordFailure(h home.Home, now time.Time, failure error) error {
	path := filepath.Join(h.Data, filepath.FromSlash(FilingLog))
	message := " could not file: " + strings.ReplaceAll(failure.Error(), "\n", " ")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if strings.HasSuffix(lines[len(lines)-1], message) {
		return nil
	}
	return appendLog(path, "- "+now.UTC().Format(time.RFC3339)+message+"\n")
}

func isLayoutFolder(name string) bool {
	for _, folder := range notTasks {
		if strings.EqualFold(name, folder) {
			return true
		}
	}
	return false
}

// mentions reports whether any row names the task, by its id or by a path
// into its folder, in either slash direction and any case, as Windows reads
// paths.
func mentions(rows []fleet.BacklogRow, id string) bool {
	path := regexp.MustCompile(`(?i)data[\\/]` + regexp.QuoteMeta(id) + `([\\/]|$|[^A-Za-z0-9._-])`)
	for _, row := range rows {
		if strings.EqualFold(row.ID, id) || path.MatchString(row.Raw) {
			return true
		}
	}
	return false
}

// freeTarget returns path, or path with the filing time appended when an
// earlier run of the same task was already filed there.
func freeTarget(path string, now time.Time) (string, error) {
	taken, err := exists(path)
	if err != nil || !taken {
		return path, err
	}
	return path + "." + now.UTC().Format("20060102T150405Z"), nil
}

func exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func dataPath(h home.Home, path string) string {
	rel, err := filepath.Rel(h.Data, path)
	if err != nil {
		return path
	}
	return "data/" + filepath.ToSlash(rel)
}

// addParkedRow adds row to the backlog file: see withParkedRow.
func addParkedRow(path, row string) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return fsx.AtomicWriteFile(path, withParkedRow(data, row))
}

// withParkedRow returns the backlog with row as the last row of its
// ## Parked section, adding that section first when there is none. The
// backlog keeps its own line endings.
func withParkedRow(data []byte, row string) []byte {
	lines, newline := backlogLines(withParkedSection(data))
	section := headingIndex(lines, "Parked")
	at := section + 1
	for i := section + 1; i < len(lines) && !isHeading(lines[i]); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			at = i + 1
		}
	}
	return joinLines(slices.Insert(lines, at, row), newline)
}

// withParkedSection returns the backlog with an empty ## Parked section
// before ## Done, or at the end, when it has none, and otherwise as it is.
func withParkedSection(data []byte) []byte {
	lines, newline := backlogLines(data)
	if headingIndex(lines, "Parked") >= 0 {
		return data
	}
	if done := headingIndex(lines, "Done"); done >= 0 {
		return joinLines(slices.Insert(lines, done, "## Parked", ""), newline)
	}
	return joinLines(append(lines, "", "## Parked"), newline)
}

func backlogLines(data []byte) ([]string, string) {
	newline := "\n"
	if strings.Contains(string(data), "\r\n") {
		newline = "\r\n"
	}
	return strings.Split(strings.TrimRight(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n"), "\n"), newline
}

func joinLines(lines []string, newline string) []byte {
	return []byte(strings.Join(lines, newline) + newline)
}

func isHeading(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "## ")
}

// headingIndex is the line of the first level-two heading named name, or -1.
func headingIndex(lines []string, name string) int {
	for i, line := range lines {
		if heading, ok := strings.CutPrefix(strings.TrimSpace(line), "## "); ok && strings.TrimSpace(heading) == name {
			return i
		}
	}
	return -1
}

func appendLog(path, line string) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := file.WriteString(line)
	return errors.Join(werr, file.Close())
}
