package tickets

import (
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// TaskPullRequests are the pull requests task id is on record with, each
// once: the one goblins pr check recorded, the one its retirement recorded,
// each its done reports named and each its ticket named.
func TaskPullRequests(stateDir, id string) ([]string, error) {
	var pulls []string
	add := func(pull string) {
		if pull = strings.TrimSpace(pull); pullAddress.MatchString(pull) && !slices.Contains(pulls, pull) {
			pulls = append(pulls, pull)
		}
	}
	values, err := state.ReadMeta(state.TaskMetaPath(stateDir, id))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	add(values["pr"])
	outcome, err := state.ReadOutcome(stateDir, id)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	add(outcome.PR)
	lines, err := fsx.ReadLines(state.StatusPath(stateDir, id))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, line := range lines {
		if _, event := state.SplitStatus(line); strings.HasPrefix(event, "done: PR ") {
			add(strings.TrimPrefix(event, "done: PR "))
		}
	}
	record, err := ReadRecord(stateDir, id)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, pull := range record.PullRequestsOnRecord() {
		add(pull)
	}
	return pulls, nil
}

// TaskBrief is the text of the brief at path, or of the task's brief in the
// home's data folder when path names none or none is there, and "" when the
// task has neither.
func TaskBrief(h home.Home, id, path string) (string, error) {
	for _, candidate := range []string{path, filepath.Join(h.Data, id, "brief.md")} {
		if candidate == "" {
			continue
		}
		text, err := fsx.ReadFile(candidate)
		if err == nil {
			return string(text), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	}
	return "", nil
}
