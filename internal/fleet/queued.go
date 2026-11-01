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

	"github.com/fpresta0607/code-goblins/internal/auth"
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

// BriefVerification is what the Verification section of task id's brief
// says: what its goblin runs before each push. That is the fast tier of the
// project's record, by the command the CFO runs it by, in place of tests the
// goblin would pick by name, which left CI the first to run a test the
// change broke.
func BriefVerification(id string) string {
	return "Before each push run `cfo verify " + id + "`. It runs the check this project's record names for a change, the one the CFO runs, and when the check fails it prints the line that says what failed and the file that holds the rest. Fix that before you push. Do not pick tests to run by name in its place: what the check leaves to CI it says, and CI is the check of that."
}

// BriefProcesses is what the Constraints section of every brief says of the
// processes its goblin starts, as the Overlord asked on 2026-10-11: that what
// goblins open to run or check tests finishes and is cleaned up, and that
// nothing is forgotten or runs in loops, and that no terminal a goblin starts
// shows on his desktop, as he asked on 2026-10-14.
func BriefProcesses() string {
	return "Browsers run headless and terminals hidden in tests, unless a picture needs a window. A heavy run starts in the background as cfo gate turn --detach <log-file> -- <command>, which starts it hidden with its pid file and log, never with a process start of the goblin's own, which opens a window on the Overlord's desktop. Every process the task starts is tracked by its pid and ended by that pid, never by its name. No retry or polling loop runs without a cap."
}

// BriefRootIssues is what the Constraints section of every brief says of a
// root issue its goblin finds outside the brief, as the Overlord asked on
// 2026-10-14: it is filed on its project's repository, where the fleet picks
// it up, and named in the report, never only mentioned.
func BriefRootIssues() string {
	return "A root issue you find outside this brief, a cause this task does not change, is filed with cfo issue file --project <project> --title <title> --body-file <file> and named by the address it prints in your report, never only mentioned. It comments on the open issue of the same root when there is one."
}

// BriefStepCards is what the Authentication section of every brief says of a
// browser step only the Overlord can take, as he asked on 2026-10-14: it
// reaches him as one Command Center card with the page and only his inputs,
// and the goblin goes on the moment the card closes.
func BriefStepCards() string {
	return "A browser or computer-use step only the Overlord can take, such as creating an account, a sign-in, a CAPTCHA, accepting terms or a token to copy, reaches him as one card: cfo auth request --project <project> --task <id> --why \"<what it is for>\" --link <the exact page> with only the inputs he enters and --wait, as your first instruction says, never a chat message or a Scrawl walkthrough."
}

// WriteQueuedBrief writes data/<id>/brief.md from a queued row and every
// detail line under it, and never replaces a brief that already exists.
func WriteQueuedBrief(h home.Home, queued QueuedTask) error {
	row := queued.Row
	body := QueuedBriefText(queued)
	manifest, err := auth.LoadManifest(h.Data, row.Repo)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if defaults := manifest.Grant(auth.Need{IsUnstated: true}).Services; len(defaults) > 0 {
		body = strings.Replace(body, "credentials: none\n", "credentials: "+strings.Join(defaults, ", ")+"\n", 1)
	}
	brief := filepath.Join(h.Data, row.ID, "brief.md")
	if err := os.MkdirAll(filepath.Dir(brief), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(brief, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(body)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return errors.Join(err, os.Remove(brief))
	}
	return nil
}

// QueuedBriefText is the brief WriteQueuedBrief writes for a queued row, so
// what routes the task before its brief exists reads what spawn will.
func QueuedBriefText(queued QueuedTask) string {
	row := queued.Row
	mode := row.Mode
	if mode == "" {
		mode = "no-mistakes"
	}
	kind := row.Kind
	if kind == "" {
		kind = "ship"
	}
	return fmt.Sprintf("# Brief %s\n\n## Project\n\n%s\n\n## Task\n\n%s\n\n%s\n\n## Acceptance criteria\n\nDeliver the task described above and verify its behavior.\n\n## Constraints\n\nFollow the project's instructions and the task detail above. %s %s\n\n## Verification\n\n%s\n\n## Authentication\n\ncredentials: none\nmcp: none\n\nThe brief records the project manifest's default services when it is written. The CFO grants another with cfo auth grant. %s\n\n## Commits\n\nNever name an AI product, company, model, agent or assistant identity as a commit co-author.\n\n## Delivery\n\nkind: %s\nmode: %s\nharness: %s\nmodel: %s\neffort: %s\nready: true\n", row.ID, row.Repo, row.Title, queued.Detail, BriefProcesses(), BriefRootIssues(), BriefVerification(row.ID), BriefStepCards(), kind, mode, row.Harness, row.Model, row.Effort)
}

// AppendQueuedTask adds row, the backlog row of task id, at the bottom of the
// Queued section under the backlog's lock, starting backlog.md with that
// section when there is none, and adds nothing when the backlog already lists
// id in any section.
func AppendQueuedTask(h home.Home, id, row string) (err error) {
	if _, err := lock.AcquireExclusiveNamed(h.State, ".backlog.lock"); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(h.State, ".backlog.lock")) }()
	backlog, err := ReadBacklog(h)
	if err != nil || backlog.Lists(id) {
		return err
	}
	path := filepath.Join(h.Data, "backlog.md")
	data, err := fsx.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		data, err = []byte("## Queued\n"), nil
	}
	if err != nil {
		return err
	}
	blocks, _ := queuedBlocks(strings.Split(string(data), "\n"), nil)
	order := make([]string, 0, len(blocks)+1)
	for _, block := range blocks {
		order = append(order, block.id)
	}
	next, err := reorderedBacklog(string(data), append(order, id), map[string]string{id: row}, nil)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, []byte(next))
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

// Lists says the backlog has a row for id, in any section.
func (backlog BacklogRows) Lists(id string) bool {
	return backlog.listed[id]
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
