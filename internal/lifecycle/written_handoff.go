package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// handoffBound bounds reading where a task stands for the handoff a pause
// writes for its goblin, which holds up the pause's stop.
const handoffBound = 10 * time.Second

// HandoffWriter writes the handoff of a goblin that wrote none of its own by
// its pause's deadline, from what its task's record says of where it stands.
// Claude goblins kept answering the pause's request with text alone and no
// tool call, so the wait ran out and their resume read nothing but the
// worktree (issue #1338).
type HandoffWriter struct {
	StateDir string
	Commands execx.Runner
	// Owner says whose task a process is (OwnerOf), for the task's own runs
	// in the machine's heavy line.
	Owner func(pid int) string
}

// Handoff is the handoff for meta's goblin as its task stands now: its
// branch and head, its open pull requests, its last status line and its
// runs in the machine's heavy line, which the pause's stop ends with every
// other process of the task's. What could not be read is named as such, so
// the goblin never takes a gap for a fact.
func (writer HandoffWriter) Handoff(ctx context.Context, meta state.TaskMeta) []byte {
	ctx, cancel := context.WithTimeout(ctx, handoffBound)
	defer cancel()
	run := func(name string, args ...string) (string, error) {
		result, err := writer.Commands.Run(ctx, execx.Request{Dir: meta.Worktree, Name: name, Args: args})
		if err != nil {
			return "", err
		}
		if result.ExitCode != 0 {
			return "", fmt.Errorf("%s %s exited %d: %s", name, args[0], result.ExitCode, strings.TrimSpace(string(result.Stderr)))
		}
		return strings.TrimSpace(string(result.Stdout)), nil
	}
	var note strings.Builder
	fmt.Fprintf(&note, "# Handoff for %s\n\n", meta.ID)
	fmt.Fprintf(&note, "You wrote no handoff of your own by the pause's deadline, so the pause wrote this one for you at %s from your task's record as the pause was taken. It says where the work stood, not what you were in the middle of.\n\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&note, "- Worktree: %s\n", meta.Worktree)
	branch, branchErr := run("git", "rev-parse", "--abbrev-ref", "HEAD")
	switch {
	case branchErr != nil:
		fmt.Fprintf(&note, "- Branch: could not be read (%v)\n", branchErr)
	case branch == "HEAD":
		note.WriteString("- Branch: none (detached HEAD)\n")
	default:
		fmt.Fprintf(&note, "- Branch: %s\n", branch)
	}
	if head, err := run("git", "rev-parse", "HEAD"); err != nil {
		fmt.Fprintf(&note, "- HEAD: could not be read (%v)\n", err)
	} else {
		fmt.Fprintf(&note, "- HEAD: %s\n", head)
	}
	if branchErr == nil && branch != "HEAD" {
		note.WriteString(writer.pullRequests(run, branch))
	}
	if lines, err := state.TailStatus(writer.StateDir, meta.ID, 1); err != nil {
		fmt.Fprintf(&note, "- Your last status line: could not be read (%v)\n", err)
	} else if len(lines) == 0 {
		note.WriteString("- Your last status line: none recorded\n")
	} else {
		fmt.Fprintf(&note, "- Your last status line: %s\n", lines[0])
	}
	note.WriteString("\n## Your runs in the machine's heavy line\n\n")
	note.WriteString(writer.turns(meta))
	return []byte(note.String())
}

// pullRequests names the open pull requests from branch, as gh in the task's
// worktree lists them.
func (writer HandoffWriter) pullRequests(run func(string, ...string) (string, error), branch string) string {
	listed, err := run("gh", "pr", "list", "--head", branch, "--state", "open", "--json", "url,title,isDraft")
	var open []struct {
		URL     string `json:"url"`
		Title   string `json:"title"`
		IsDraft bool   `json:"isDraft"`
	}
	if err == nil {
		err = json.Unmarshal([]byte(listed), &open)
	}
	if err == nil && open == nil {
		err = errors.New("gh listed nothing")
	}
	if err != nil {
		return fmt.Sprintf("- Open pull requests: could not be read (%v)\n", err)
	}
	if len(open) == 0 {
		return "- Open pull requests: none from " + branch + "\n"
	}
	var named strings.Builder
	for _, pull := range open {
		draft := ""
		if pull.IsDraft {
			draft = " (draft)"
		}
		fmt.Fprintf(&named, "- Open pull request: %s%s, %s\n", pull.URL, draft, pull.Title)
	}
	return named.String()
}

// turns names the runs of meta's task that hold a turn of the machine's
// heavy line or wait for one.
func (writer HandoffWriter) turns(meta state.TaskMeta) string {
	holding, waiting, err := verify.MachineLine()
	if err != nil {
		return fmt.Sprintf("They could not be read (%v).\n", err)
	}
	var named strings.Builder
	for _, run := range holding {
		if writer.Owner(run.PID) == meta.ID {
			fmt.Fprintf(&named, "- holding its turn since %s: %s (pid %d)\n", run.Since.UTC().Format(time.RFC3339), run.Who, run.PID)
		}
	}
	for _, run := range waiting {
		if writer.Owner(run.PID) == meta.ID {
			fmt.Fprintf(&named, "- waiting in line since %s: %s (pid %d)\n", run.Since.UTC().Format(time.RFC3339), run.Who, run.PID)
		}
	}
	if named.Len() == 0 {
		return "None held a turn or waited for one.\n"
	}
	return "The pause ends every process of yours, these among them, so none of them finishes: start again each you still need once you resume.\n\n" + named.String()
}
