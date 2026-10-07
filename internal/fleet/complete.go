package fleet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// CompleteQueuedTask reconciles one retired, delivered task with its backlog
// source, preserving every continuation line and every unrelated byte.
func CompleteQueuedTask(h home.Home, id string) (err error) {
	if err := state.ValidTaskID(id); err != nil {
		return err
	}
	for _, name := range []string{".queued-" + id + ".lock", ".spawn.lock", ".backlog.lock"} {
		if _, err := lock.AcquireExclusiveNamed(h.State, name); err != nil {
			return err
		}
		defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(h.State, name)) }()
	}
	if _, err := os.Stat(state.TaskMetaPath(h.State, id)); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return err
		}
		return errors.New("task still has live metadata; finish cleanup before closing its backlog row")
	}
	outcome, err := state.ReadOutcome(h.State, id)
	if err != nil {
		return err
	}
	if outcome.Phase != "done" {
		return errors.New("backlog completion requires a delivered task outcome")
	}
	path := filepath.Join(h.Data, "backlog.md")
	data, err := fsx.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(data)
	newline := "\n"
	if strings.Contains(text, "\r\n") {
		newline = "\r\n"
	}
	lines := strings.SplitAfter(text, "\n")
	section := ""
	start, end, anchor, matches := -1, -1, -1, 0
	isDone := false
	for index, raw := range lines {
		if strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t") {
			continue
		}
		line := strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")
		if heading := levelTwoHeading.FindStringSubmatch(line); heading != nil {
			section = strings.TrimSpace(heading[1])
			if section == "Done" && anchor < 0 {
				anchor = index + 1
			}
			continue
		}
		row := parseBacklogRow(line)
		if !row.Structured || row.ID != id {
			continue
		}
		matches++
		isDone = section == "Done"
		if section != "Queued" || strings.EqualFold(metadataValue(line, "hold-kind"), "parked") {
			continue
		}
		start, end = index, index+1
		for next := end; next < len(lines); next++ {
			if strings.TrimSpace(lines[next]) == "" {
				continue
			}
			if !continuesRow(strings.TrimSuffix(strings.TrimSuffix(lines[next], "\n"), "\r")) {
				break
			}
			end = next + 1
		}
	}
	if matches == 0 {
		return ErrNotQueued
	}
	if matches != 1 {
		return fmt.Errorf("backlog completion needs exactly one source row for %s; found %d", id, matches)
	}
	if start < 0 {
		if isDone {
			return nil
		}
		return ErrNotQueued
	}
	if outcome.At.IsZero() {
		return errors.New("completion outcome has no delivery time")
	}
	group := append([]string(nil), lines[start:end]...)
	header := strings.TrimSuffix(strings.TrimSuffix(group[0], "\n"), "\r")
	match := checkboxBacklogRow.FindStringSubmatch(header)
	if match == nil {
		match = boldBacklogRow.FindStringSubmatch(header)
	}
	header = "- [x] " + id + " - " + match[2]
	if outcome.PR != "" && !strings.Contains(header, outcome.PR) {
		header += " " + outcome.PR
	}
	header += " (done " + outcome.At.UTC().Format("2006-01-02") + ")"
	if strings.HasSuffix(group[0], "\n") {
		header += newline
	}
	group[0] = header
	remaining := append(append([]string(nil), lines[:start]...), lines[end:]...)
	if anchor > start {
		anchor -= end - start
	}
	if anchor < 0 {
		prefix := strings.Join(remaining, "")
		if prefix != "" && !strings.HasSuffix(prefix, "\n") {
			prefix += newline
		}
		return fsx.AtomicWriteFile(path, []byte(prefix+newline+"## Done"+newline+strings.Join(group, "")))
	}
	prefix, suffix := strings.Join(remaining[:anchor], ""), strings.Join(remaining[anchor:], "")
	if !strings.HasSuffix(prefix, "\n") {
		prefix += newline
	}
	closed := strings.Join(group, "")
	if suffix != "" && !strings.HasSuffix(closed, "\n") {
		closed += newline
	}
	return fsx.AtomicWriteFile(path, []byte(prefix+closed+suffix))
}
