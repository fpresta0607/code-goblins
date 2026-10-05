package fleet

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type QueuedTask struct {
	Row         BacklogRow
	Detail      string
	Revision    string
	IsBriefOnly bool
}

var ErrNotQueued = errors.New("task is not queued")

var queuedTitleSuffix = regexp.MustCompile(`(?i)\s*(?:\((?:repo|kind|priority|hold|hold-kind|harness|model|effort|mode)\s*:|blocked-by:|https?://|data/[^\s]+/report\.md)`)

func SaveQueuedTask(h home.Home, id, revision, text string) error {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" || len(text) > 8000 {
		return errors.New("enter a task title and detail, up to 8000 characters")
	}
	title, detail, _ := strings.Cut(text, "\n")
	if len(title) > 200 || queuedTitleSuffix.MatchString(title) || strings.ContainsAny(title, "\r\x00") {
		return errors.New("use a title up to 200 characters without backlog metadata")
	}
	for _, line := range strings.Split(detail, "\n") {
		line = strings.TrimSpace(line)
		if parseBacklogRow(line).Structured || levelTwoHeading.MatchString(line) || strings.ContainsRune(line, '\x00') {
			return errors.New("task detail cannot contain backlog task rows or section headings")
		}
	}
	return changeQueuedTask(h, id, revision, strings.TrimSpace(title), strings.TrimSpace(detail), false)
}

// WriteQueuedBrief writes data/<id>/brief.md from a queued row and every
// detail line under it, and never replaces a brief that already exists.
func WriteQueuedBrief(h home.Home, queued QueuedTask) error {
	row := queued.Row
	brief := filepath.Join(h.Data, row.ID, "brief.md")
	if err := os.MkdirAll(filepath.Dir(brief), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(brief, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	mode := row.Mode
	if mode == "" {
		mode = "no-mistakes"
	}
	kind := row.Kind
	if kind == "" {
		kind = "ship"
	}
	_, writeErr := fmt.Fprintf(file, "# Brief %s\n\n## Project\n\n%s\n\n## Task\n\n%s\n\n%s\n\n## Acceptance criteria\n\nDeliver the task described above and verify its behavior.\n\n## Constraints\n\nFollow the project's instructions and the task detail above.\n\n## Authentication\n\nUse the project's configured authentication preflight before dispatch.\n\n## Commits\n\nNever name an AI product, company, model, agent or assistant identity as a commit co-author.\n\n## Delivery\n\nkind: %s\nmode: %s\nharness: %s\nmodel: %s\neffort: %s\n", row.ID, row.Repo, row.Title, queued.Detail, kind, mode, row.Harness, row.Model, row.Effort)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return errors.Join(err, os.Remove(brief))
	}
	return nil
}

func RemoveQueuedTask(h home.Home, id, revision string) error {
	return changeQueuedTask(h, id, revision, "", "", true)
}

func changeQueuedTask(h home.Home, id, revision, title, detail string, remove bool) (err error) {
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
	if before.IsBriefOnly && remove {
		return nil
	}
	path := filepath.Join(h.Data, "backlog.md")
	data, err := fsx.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && before.IsBriefOnly {
		data, err = []byte("## Queued\n"), nil
	}
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	blocks, _ := queuedBlocks(lines, nil)
	var changed []string
	if !remove {
		match := checkboxBacklogRow.FindStringSubmatch(strings.TrimSpace(before.Row.Raw))
		if match == nil {
			match = boldBacklogRow.FindStringSubmatch(strings.TrimSpace(before.Row.Raw))
		}
		suffix := ""
		if at := queuedTitleSuffix.FindStringIndex(match[2]); at != nil {
			suffix = " " + strings.TrimSpace(match[2][at[0]:])
		}
		changed = []string{"- [ ] " + id + " - " + title + suffix}
		if detail != "" {
			for _, line := range strings.Split(detail, "\n") {
				changed = append(changed, "  "+line)
			}
		}
	}
	var next string
	for _, block := range blocks {
		if block.id == id {
			out := append([]string(nil), lines[:block.start]...)
			out = append(out, changed...)
			next = strings.Join(append(out, lines[block.end:]...), "\n")
			break
		}
	}
	if before.IsBriefOnly {
		order := make([]string, 0, len(blocks)+1)
		for _, block := range blocks {
			order = append(order, block.id)
		}
		next, err = reorderedBacklog(string(data), append(order, id), map[string]string{id: strings.Join(changed, "\n")}, nil)
		if err != nil {
			return err
		}
	}
	brief := filepath.Join(h.Data, id, "brief.md")
	var oldBrief []byte
	if !remove {
		oldBrief, err = fsx.ReadFile(brief)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil {
			section := regexp.MustCompile(`(?ms)^## Task\r?\n.*?(\n## |\z)`)
			location := section.FindIndex(oldBrief)
			if location == nil {
				return errors.New("the existing brief has no Task section; ask the CFO to repair it")
			}
			end := location[1]
			if strings.HasSuffix(string(oldBrief[location[0]:end]), "\n## ") {
				end -= len("## ")
			}
			replacement := "## Task\n\n" + title + "\n\n" + detail + "\n\nAdjusted from the board at " + time.Now().UTC().Format(time.RFC3339) + ".\n\n"
			newBrief := append(append(append([]byte(nil), oldBrief[:location[0]]...), []byte(replacement)...), oldBrief[end:]...)
			if err := fsx.AtomicWriteFile(brief, newBrief); err != nil {
				return err
			}
		}
	}
	if err := fsx.AtomicWriteFile(path, []byte(next)); err != nil {
		if oldBrief != nil {
			return errors.Join(err, fsx.AtomicWriteFile(brief, oldBrief))
		}
		return err
	}
	return nil
}

func ReadQueuedTask(h home.Home, id string) (QueuedTask, error) {
	if err := state.ValidTaskID(id); err != nil {
		return QueuedTask{}, err
	}
	backlog, err := ReadBacklog(h)
	if err != nil {
		return QueuedTask{}, err
	}
	return backlog.ReadQueuedTask(h, id)
}

// ReadQueuedTask reuses this reading of the backlog; a brief without a row
// is read separately, without parsing the backlog again for each task.
func (backlog BacklogRows) ReadQueuedTask(h home.Home, id string) (QueuedTask, error) {
	if err := state.ValidTaskID(id); err != nil {
		return QueuedTask{}, err
	}
	if _, err := os.Stat(filepath.Join(h.State, "outcomes", id+".json")); err == nil {
		outcome, err := state.ReadOutcome(h.State, id)
		if err != nil {
			return QueuedTask{}, err
		}
		if outcome.Phase == "done" {
			return QueuedTask{}, ErrNotQueued
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return QueuedTask{}, err
	}
	if backlog.duplicates[id] {
		return QueuedTask{}, fmt.Errorf("backlog queues %s twice", id)
	}
	if task, found := backlog.queuedTasks[id]; found {
		return task, nil
	}
	if backlog.listed[id] {
		return QueuedTask{}, ErrNotQueued
	}
	return readUndispatchedBrief(h, id)
}

func readUndispatchedBrief(h home.Home, id string) (QueuedTask, error) {
	for _, suffix := range []string{".meta", ".status"} {
		if _, err := os.Stat(filepath.Join(h.State, id+suffix)); !errors.Is(err, os.ErrNotExist) {
			return QueuedTask{}, ErrNotQueued
		}
	}
	archived, err := os.ReadDir(filepath.Join(h.State, state.ArchiveDirName))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return QueuedTask{}, err
	}
	for _, entry := range archived {
		if strings.HasPrefix(entry.Name(), id+".") {
			return QueuedTask{}, ErrNotQueued
		}
	}
	brief, err := fsx.ReadFile(filepath.Join(h.Data, id, "brief.md"))
	if errors.Is(err, os.ErrNotExist) {
		return QueuedTask{}, ErrNotQueued
	}
	if err != nil {
		return QueuedTask{}, err
	}
	sections := map[string][]string{}
	section := ""
	for _, line := range strings.Split(strings.ReplaceAll(string(brief), "\r\n", "\n"), "\n") {
		if heading := levelTwoHeading.FindStringSubmatch(line); heading != nil {
			section = heading[1]
		} else {
			sections[section] = append(sections[section], line)
		}
	}
	title, detail, _ := strings.Cut(strings.TrimSpace(strings.Join(sections["Task"], "\n")), "\n")
	if title == "" {
		title = id
	}
	project, _, _ := strings.Cut(strings.TrimSpace(strings.Join(sections["Project"], "\n")), "\n")
	if project == "" || strings.ContainsAny(project, "()\r") {
		return QueuedTask{}, errors.New("the queued brief needs a plain project name or path")
	}
	raw := "- **" + id + "** - " + title + " (repo: " + project + ")"
	for _, line := range sections["Delivery"] {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok && (key == "mode" || key == "harness" || key == "model" || key == "effort" || key == "kind") {
			raw += " (" + key + ": " + strings.TrimSpace(value) + ")"
		}
	}
	return QueuedTask{Row: parseBacklogRow(raw), Detail: strings.TrimSpace(detail), Revision: fmt.Sprintf("brief:%x", sha256.Sum256(brief)), IsBriefOnly: true}, nil
}
