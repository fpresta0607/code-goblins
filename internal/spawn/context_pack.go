package spawn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/memory"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// contextPackName is the pack a goblin reads beside its brief, and
// contextPackRecord the fingerprints of the entries its conversation holds.
const (
	contextPackName   = "context-pack.md"
	contextPackRecord = "context-pack.json"
	// contextPackReads bounds the git reads of the repository's live state.
	contextPackReads = 10 * time.Second
)

// pullRequestURL is a GitHub pull request a status line names.
var pullRequestURL = regexp.MustCompile(`https://github\.com/[\w.-]+/[\w.-]+/pull/\d+`)

type contextPackDelivery struct {
	Selected []string `json:"selected"`
}

// writeContextPack writes the task's context pack beside its brief and
// returns the sentence that tells the goblin to read it, and the step that
// records the pack as delivered, which the caller takes only once the goblin
// was given its instruction. A conversation resumed in place is given only
// what changed since the pack it holds, and any other start the whole pack,
// with the old record removed first: a new conversation holds nothing, and
// a record that claims less than a conversation holds only repeats entries.
// A pack whose memory cannot be read says so in its place. A Service with
// no DataDir writes none.
func (s Service) writeContextPack(ctx context.Context, meta state.TaskMeta, brief string, isResumed bool) (string, func() error, error) {
	if s.DataDir == "" {
		return "", func() error { return nil }, nil
	}
	path := filepath.Join(meta.TaskTmp, contextPackName)
	record := filepath.Join(meta.TaskTmp, contextPackRecord)
	budget, err := memory.ReadBudget(s.DataDir, "context-pack-budget", memory.DefaultPackBudget)
	if err != nil {
		return "", nil, fmt.Errorf("spawn: context pack budget: %w", err)
	}
	project := filepath.Base(meta.Project)
	input := memory.PackInput{Task: meta.ID, Project: project, Title: meta.Title, Brief: brief, Budget: budget, Now: time.Now()}
	if isResumed {
		content, err := fsx.ReadFile(record)
		var delivered contextPackDelivery
		if err == nil {
			err = memory.Decode(content, &delivered)
		}
		switch {
		case err == nil:
			input.Delivered = delivered.Selected
		case !errors.Is(err, fs.ErrNotExist):
			input.DeliveredUnread = err.Error()
		}
	} else if err := fsx.Remove(record); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", nil, fmt.Errorf("spawn: remove the last context pack's delivery record: %w", err)
	}
	reading, cancel := context.WithTimeout(ctx, contextPackReads)
	defer cancel()
	if input.Live, err = s.liveTasks(reading, meta, project); err != nil {
		return "", nil, err
	}
	input.Commits, input.CommitsUnread = s.commitsSinceBase(reading, meta.Worktree)
	text, selected := "", []string{}
	index, err := memory.BuildIndex(s.DataDir, s.StateDir, input.Now)
	var pack memory.Pack
	if err == nil {
		pack, err = index.BuildPack(input)
	}
	if err != nil {
		text = fmt.Sprintf("# Context pack: %s, %s\n\nThe fleet's memory could not be read for this pack: %v\nAsk the CEO for what the task needs to know, and run ceo memory recall \"<question>\" once the memory reads again.\n", project, meta.ID, err)
	} else {
		text, selected = pack.Text, pack.Selected
	}
	if err := fsx.AtomicWriteFile(path, []byte(text)); err != nil {
		return "", nil, fmt.Errorf("spawn: write the context pack: %w", err)
	}
	encoded, err := json.Marshal(contextPackDelivery{Selected: selected})
	if err != nil {
		return "", nil, err
	}
	recordDelivery := func() error {
		if err := fsx.AtomicWriteFile(record, encoded); err != nil {
			return fmt.Errorf("its context pack's delivery record could not be written, so a resumed conversation may be given entries it already holds: %w", err)
		}
		return nil
	}
	return fmt.Sprintf(" Read the context pack at %s before you start: the rulings, facts, other work in this repository and recent reports picked for this task within %d estimated tokens, each with its source to open. When you need to know more, run ceo memory recall \"<question>\" --project %s.", path, budget, project), recordDelivery, nil
}

// liveTasks is every other task the fleet runs in project now, with its
// branch, the last pull request its reports name and its last report.
func (s Service) liveTasks(ctx context.Context, meta state.TaskMeta, project string) ([]memory.LiveTask, error) {
	paths, err := filepath.Glob(filepath.Join(s.StateDir, "*.meta"))
	if err != nil {
		return nil, err
	}
	slices.Sort(paths)
	var live []memory.LiveTask
	for _, path := range paths {
		id := strings.TrimSuffix(filepath.Base(path), ".meta")
		if id == meta.ID {
			continue
		}
		other, err := state.ReadTaskMeta(s.StateDir, id)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			// Whether it works in this repository is unknown, so it is named
			// rather than left out or let stop this goblin's start.
			live = append(live, memory.LiveTask{ID: id, Title: "its task record could not be read: " + err.Error()})
			continue
		}
		if !strings.EqualFold(filepath.Base(other.Project), project) {
			continue
		}
		task := memory.LiveTask{ID: id, Title: other.Title, Goblin: other.GoblinName}
		lines, err := fsx.ReadLines(state.StatusPath(s.StateDir, id))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			task.Status = "unreadable: " + err.Error()
		}
		if len(lines) > 0 {
			_, task.Status = state.SplitStatus(lines[len(lines)-1])
		}
		for index := len(lines) - 1; index >= 0 && task.PullRequest == ""; index-- {
			task.PullRequest = pullRequestURL.FindString(lines[index])
		}
		if other.Worktree != "" {
			if task.Branch, err = s.git(ctx, other.Worktree, "branch", "--show-current"); err != nil {
				task.Branch = "unreadable: " + err.Error()
			}
		}
		live = append(live, task)
	}
	return live, nil
}

// commitsSinceBase is main's commits that the worktree's branch has not
// merged, from the refs the last fetch left, or why they could not be read.
func (s Service) commitsSinceBase(ctx context.Context, worktreePath string) ([]string, string) {
	base, err := s.git(ctx, worktreePath, "merge-base", "HEAD", "origin/HEAD")
	if err != nil {
		return nil, "no merge base with origin/HEAD: " + err.Error()
	}
	log, err := s.git(ctx, worktreePath, "log", "--format=%h %s", "-n", "15", base+"..origin/HEAD")
	if err != nil {
		return nil, err.Error()
	}
	if log == "" {
		return nil, ""
	}
	return strings.Split(log, "\n"), ""
}

// git is git's trimmed output in dir, or why it failed there.
func (s Service) git(ctx context.Context, dir string, args ...string) (string, error) {
	result, err := s.commands().Run(ctx, execx.Request{Dir: dir, Name: "git", Args: args, NoConsole: true})
	if err == nil && result.ExitCode != 0 {
		err = fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(string(result.Stderr)))
	}
	return strings.TrimSpace(string(result.Stdout)), err
}
