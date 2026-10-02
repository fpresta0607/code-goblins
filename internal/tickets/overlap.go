package tickets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// TeammateOverlaps is the part of the report's overlaps that someone other
// than the Overlord has in flight: what a dispatch stops for. The Overlord's
// own pull requests and issues are his fleet's, which the CFO already sees
// on the board, so they stay in the report and out of this.
func (r Report) TeammateOverlaps() Overlaps {
	theirs := Overlaps{Files: []FileOverlap{}, Issues: []IssueMatch{}}
	if r.Overlaps == nil {
		return theirs
	}
	theirs.Area = r.Overlaps.Area
	for _, overlap := range r.Overlaps.Files {
		if !strings.EqualFold(overlap.Author.Login, r.Overlord) {
			theirs.Files = append(theirs.Files, overlap)
		}
	}
	for _, match := range r.Overlaps.Issues {
		if !strings.EqualFold(match.Author.Login, r.Overlord) {
			theirs.Issues = append(theirs.Issues, match)
		}
	}
	return theirs
}

// Lines says each overlap in one line: who has what in flight and where it
// meets the area.
func (o Overlaps) Lines() []string {
	var lines []string
	for _, overlap := range o.Files {
		what := "branch " + overlap.Branch
		if overlap.PullRequest != 0 {
			what = fmt.Sprintf("PR #%d", overlap.PullRequest)
		}
		lines = append(lines, fmt.Sprintf("%s by %s changes %s", what, actorName(overlap.Author), strings.Join(overlap.Files, ", ")))
	}
	for _, match := range o.Issues {
		var why []string
		if len(match.Paths) > 0 {
			why = append(why, "names "+strings.Join(match.Paths, ", "))
		}
		if len(match.Words) > 0 {
			why = append(why, "shares the words "+strings.Join(match.Words, ", "))
		}
		lines = append(lines, fmt.Sprintf("issue #%d by %s %s: %s", match.Number, actorName(match.Author), strings.Join(why, " and "), match.Title))
	}
	return lines
}

// An overlap note is the CFO's reason for starting a task beside a
// teammate's work. cfo spawn writes it, and the supervisor, the tickets'
// only writer, reads it into the task's ticket.

// WriteOverlapNote keeps the reason a task was started beside other work.
func WriteOverlapNote(directory, taskID, note string) error {
	path, err := overlapNotePath(directory, taskID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, []byte(note))
}

// ReadOverlapNote reads the reason a task was started beside other work, or
// "" when it was not.
func ReadOverlapNote(directory, taskID string) (string, error) {
	path, err := overlapNotePath(directory, taskID)
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

func overlapNotePath(directory, taskID string) (string, error) {
	if err := state.ValidTaskID(taskID); err != nil {
		return "", err
	}
	return filepath.Join(directory, "ticket-overlaps", taskID+".txt"), nil
}
