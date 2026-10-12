package tickets

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// MaxTitle is the most characters a ticket's title has. Teammates read it in
// GitHub's lists, where a longer title is a paragraph.
const MaxTitle = 100

// trailingHarness is the harness a title names at its end, as the backlog's
// rows end in "; Claude Code" or "; Claude Code, max". The ticket's goblin:
// label already says it.
var trailingHarness = regexp.MustCompile(`(?i)(?:\s*;\s*(?:claude code|claude|codex|pi)(?:\s*,\s*[\w.-]+)?)+\s*$`)

// ShortTitle is a title as a ticket carries it: one line, without a harness
// named at its end, and at most MaxTitle characters, cut at a word with an
// ellipsis when it is longer.
func ShortTitle(title string) string {
	title = strings.Join(strings.Fields(title), " ")
	title = strings.TrimSpace(trailingHarness.ReplaceAllString(title, ""))
	characters := []rune(title)
	if len(characters) <= MaxTitle {
		return title
	}
	cut := string(characters[:MaxTitle])
	if space := strings.LastIndex(cut, " "); space >= len(cut)/2 {
		cut = cut[:space]
	} else {
		cut = string(characters[:MaxTitle-1])
	}
	return strings.TrimRight(cut, " ,;:-") + "…"
}

// A title note is the title the CFO gave a task with cfo title. cfo title
// writes it, and the supervisor, the tickets' only writer, puts it on the
// task's ticket, even once the task has finished; it is the one thing that
// changes the title of an issue already open.

// WriteTitleNote keeps the title the CFO gave a task.
func WriteTitleNote(directory, taskID, title string) error {
	path, err := titleNotePath(directory, taskID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, []byte(title))
}

// ReadTitleNote reads the title the CFO gave a task, or "" when he gave none.
func ReadTitleNote(directory, taskID string) (string, error) {
	path, err := titleNotePath(directory, taskID)
	if err != nil {
		return "", err
	}
	data, err := fsx.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func titleNotePath(directory, taskID string) (string, error) {
	if err := state.ValidTaskID(taskID); err != nil {
		return "", err
	}
	return filepath.Join(directory, "ticket-titles", taskID+".txt"), nil
}
