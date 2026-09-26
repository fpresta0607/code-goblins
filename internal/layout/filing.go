package layout

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
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
//   - finished: no metadata, a status log or a state archive entry says it
//     was dispatched, and its brief has not changed since. It moves to
//     archive/finished.
//   - never dispatched: anything else. Its brief is parked in
//     archive/parked once StaleBriefAge has passed since the brief last
//     changed, or as soon as a Parked backlog row names it.
//
// What is still read keeps its folder. A brief with a queued backlog row is
// queued work, however old. A finished folder stays while an open backlog
// row names it by id, or while anything the fleet or the Overlord still reads
// names its path (see namedInLiveText), so a queued row or a live goblin's
// brief that says "start from data/<id>/handoff.md" keeps working.
// harnessMemory is the CFO harness's own memory folder, read as part of that.
func Plan(h home.Home, harnessMemory string, now time.Time) ([]Move, error) {
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
	type folder struct {
		id               string
		brief            fs.FileInfo
		live, dispatched bool
	}
	var folders []folder
	var briefs []string
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
		f := folder{id: id, brief: brief}
		if f.live, err = exists(filepath.Join(h.State, id+".meta")); err != nil {
			return nil, err
		}
		// The task's last record is its status log or its newest state
		// archive entry. A brief written after it is a new brief for the same
		// id rather than the finished task's, so it is not finished work.
		var lastRecord time.Time
		if info, err := os.Stat(filepath.Join(h.State, id+".status")); err == nil {
			f.dispatched, lastRecord = true, info.ModTime()
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
			f.dispatched = true
			if info.ModTime().After(lastRecord) {
				lastRecord = info.ModTime()
			}
		}
		if brief.ModTime().After(lastRecord) {
			f.dispatched = false
		}
		if f.live || !f.dispatched {
			briefs = append(briefs, filepath.Join(h.Data, id, "brief.md"))
		}
		folders = append(folders, f)
	}
	named, err := namedInLiveText(h, harnessMemory, briefs)
	if err != nil {
		return nil, err
	}

	var moves []Move
	for _, f := range folders {
		if f.live {
			continue
		}
		queued, parked := mentions(backlog.Queued, f.id), mentions(backlog.Parked, f.id)
		var move Move
		switch {
		case f.dispatched && !queued && !parked && !named[strings.ToLower(f.id)]:
			move = Move{ID: f.id, Reason: "finished"}
		case !f.dispatched && !queued && (parked || now.Sub(f.brief.ModTime()) >= StaleBriefAge):
			since := f.brief.ModTime().UTC().Format("2006-01-02")
			move = Move{ID: f.id, Parked: true, Reason: "brief not dispatched since " + since}
			if !parked {
				move.Row = fmt.Sprintf("- [ ] %s - Brief not dispatched since %s, parked %s: data/%s/%s/brief.md", f.id, since, now.UTC().Format("2006-01-02"), ParkedDir, f.id)
			}
		default:
			continue
		}
		dir := FinishedDir
		if move.Parked {
			dir = ParkedDir
		}
		move.From = filepath.Join(h.Data, f.id)
		if move.To, err = freeTarget(filepath.Join(h.Data, filepath.FromSlash(dir), f.id), now); err != nil {
			return nil, err
		}
		moves = append(moves, move)
	}
	return moves, nil
}

// dataPathName matches a path into the data folder, in either slash
// direction, and captures the folder it names. The word boundary keeps
// "metadata/x" from reading as a path into data.
var dataPathName = regexp.MustCompile(`(?i)\bdata[\\/]([A-Za-z0-9._-]+)`)

// namedInLiveText returns, lowercased, every data folder named by path in
// what the fleet or the Overlord still reads: all of the backlog, the
// directives, the home's memory and the harness's memory folder, the given
// briefs, and every open Command Center question, review item and run card.
// A file that is not there reads as empty.
func namedInLiveText(h home.Home, harnessMemory string, briefs []string) (map[string]bool, error) {
	files := append([]string{filepath.Join(h.Data, Backlog), filepath.Join(h.Data, "overlord.md")}, briefs...)
	for _, dir := range []string{filepath.Join(h.Data, filepath.FromSlash(path.Dir(MemoryIndex))), harnessMemory} {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		for _, entry := range entries {
			if entry.Type().IsRegular() {
				files = append(files, filepath.Join(dir, entry.Name()))
			}
		}
	}
	var text strings.Builder
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		text.Write(data)
		text.WriteByte('\n')
	}
	open, err := openBoardText(h)
	if err != nil {
		return nil, err
	}
	text.WriteString(open)
	named := map[string]bool{}
	for _, match := range dataPathName.FindAllStringSubmatch(text.String(), -1) {
		named[strings.ToLower(strings.TrimRight(match[1], "."))] = true
	}
	return named, nil
}

// openBoardText is the text of every Command Center item still waiting in
// the supervisor's database: a question not yet closed, an open review item
// and a run card ready or running.
func openBoardText(h home.Home) (string, error) {
	data, err := os.ReadFile(filepath.Join(h.State, ".supervisor.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var board struct {
		Questions []struct {
			Text, Message, Status string
			Options               []string
		}
		Reviews []struct{ Title, Reason, State string }
		Runs    []struct{ Title, Command, Cwd, State string }
	}
	if err := json.Unmarshal(data, &board); err != nil {
		return "", fmt.Errorf("layout: read the Command Center's open items: %w", err)
	}
	var text strings.Builder
	for _, q := range board.Questions {
		if !slices.Contains([]string{"cleared", "succeeded", "failed", "superseded"}, q.Status) {
			text.WriteString(strings.Join(append([]string{q.Text, q.Message}, q.Options...), "\n") + "\n")
		}
	}
	for _, r := range board.Reviews {
		if r.State == "open" {
			text.WriteString(r.Title + "\n" + r.Reason + "\n")
		}
	}
	for _, r := range board.Runs {
		if r.State == "ready" || r.State == "running" {
			text.WriteString(r.Title + "\n" + r.Command + "\n" + r.Cwd + "\n")
		}
	}
	return text.String(), nil
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

// File files a laid-out home's data now: see Plan and Apply, with Claude
// Code's memory folder for the home as the harness memory it reads. A home
// without the layout marker is left alone, because reorganising it is the
// migration its owner decides on. A pass that fails is recorded in the
// filing log, once until the failure changes, so a folder a process holds
// open shows there instead of being retried in silence.
func File(h home.Home, now time.Time) ([]Move, error) {
	laidOut, err := exists(filepath.Join(h.Data, Marker))
	if err != nil || !laidOut {
		return nil, err
	}
	moves, err := Plan(h, ClaudeMemoryFolder(h.Root), now)
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

// ClaudeMemoryFolder is Claude Code's own memory folder for a session run
// in root: ~/.claude/projects/<root with every character but a letter or a
// digit replaced by a hyphen>/memory, so C:\dev\code-goblins keeps its
// memory under C--dev-code-goblins. It is empty when there is no user folder.
func ClaudeMemoryFolder(root string) string {
	user, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, root)
	return filepath.Join(user, ".claude", "projects", name, "memory")
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
