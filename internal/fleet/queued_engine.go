package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
)

func SaveQueuedEngine(h home.Home, id, revision, harness, model, effort string) (err error) {
	for _, value := range []string{harness, model, effort} {
		if strings.ContainsAny(value, "(),\r\n\x00") || len(value) > 200 {
			return errors.New("invalid queued engine choice")
		}
	}
	if harness == "" {
		return errors.New("choose a harness")
	}
	if _, err := lock.AcquireExclusiveNamed(h.State, ".backlog.lock"); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(h.State, ".backlog.lock")) }()
	before, err := ReadQueuedTask(h, id)
	if err != nil {
		return err
	}
	if before.Revision != revision {
		return ErrQueueChanged
	}
	path := filepath.Join(h.Data, "backlog.md")
	data, err := fsx.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && before.IsBriefOnly {
		data, err = []byte("## Queued\n"), nil
	}
	if err != nil {
		return err
	}
	row := regexp.MustCompile(`\([^)]*\)`).ReplaceAllStringFunc(before.Row.Raw, func(group string) string {
		var kept []string
		hasEngine := false
		for _, setting := range strings.Split(group[1:len(group)-1], ",") {
			key, _, _ := strings.Cut(strings.TrimSpace(setting), ":")
			switch strings.ToLower(key) {
			case "harness", "model", "effort":
				hasEngine = true
			default:
				kept = append(kept, strings.TrimSpace(setting))
			}
		}
		if !hasEngine {
			return group
		}
		if len(kept) == 0 {
			return ""
		}
		return "(" + strings.Join(kept, ", ") + ")"
	})
	row = strings.TrimSpace(row) + " (harness: " + harness + ", model: " + model + ", effort: " + effort + ")"
	lines := strings.Split(string(data), "\n")
	blocks, _ := queuedBlocks(lines, nil)
	if before.IsBriefOnly {
		for _, line := range strings.Split(before.Detail, "\n") {
			if line != "" {
				row += "\n  " + line
			}
		}
		var order []string
		for _, block := range blocks {
			order = append(order, block.id)
		}
		next, err := reorderedBacklog(string(data), append(order, id), map[string]string{id: row}, nil)
		if err != nil {
			return err
		}
		return fsx.AtomicWriteFile(path, []byte(next))
	}
	for _, block := range blocks {
		if block.id == id {
			lines[block.start] = row
			return fsx.AtomicWriteFile(path, []byte(strings.Join(lines, "\n")))
		}
	}
	return ErrNotQueued
}
