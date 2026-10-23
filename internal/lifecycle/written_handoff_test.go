package lifecycle_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// handoffCommands answers the git and gh commands a written handoff reads,
// and records them. An empty answer is a command that fails.
type handoffCommands struct {
	branch, head, pulls string
	ran                 *[]string
}

func (commands handoffCommands) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	command := request.Name + " " + strings.Join(request.Args, " ")
	if commands.ran != nil {
		*commands.ran = append(*commands.ran, command)
	}
	answers := map[string]string{
		"git rev-parse --abbrev-ref HEAD": commands.branch,
		"git rev-parse HEAD":              commands.head,
		"gh pr list --head " + commands.branch + " --state open --json url,title,isDraft": commands.pulls,
	}
	answer, ok := answers[command]
	if !ok {
		return execx.Result{}, errors.New("unexpected command " + command)
	}
	if answer == "" {
		return execx.Result{ExitCode: 1, Stderr: []byte("not answered")}, nil
	}
	return execx.Result{Stdout: []byte(answer + "\n")}, nil
}

func TestWrittenHandoffSaysWhereTheTaskStands(t *testing.T) {
	head := strings.Repeat("a", 40)
	for _, test := range []struct {
		name     string
		commands handoffCommands
		status   string
		owner    string
		want     []string
		unwanted []string
	}{
		{
			name:     "on a branch with a draft pull request and a turn",
			commands: handoffCommands{branch: "fix/pause", head: head, pulls: `[{"url":"https://github.com/owner/repo/pull/7","title":"Pause writes the handoff","isDraft":true}]`},
			status:   "waiting on memory: the suite waits in the heavy line",
			owner:    "task",
			want: []string{
				"# Handoff for task", "You wrote no handoff of your own by the pause's deadline, so the pause wrote this one for you",
				"- Branch: fix/pause\n", "- HEAD: " + head + "\n",
				"- Open pull request: https://github.com/owner/repo/pull/7 (draft), Pause writes the handoff\n",
				"- Your last status line: ", "waiting on memory: the suite waits in the heavy line\n",
				"start again each you still need", "- holding its turn since ", ": go test ./... in the worktree, task Zora (task) (pid ",
			},
		},
		{
			name:     "with no open pull request and another task's turn",
			commands: handoffCommands{branch: "fix/pause", head: head, pulls: "[]"},
			owner:    "other",
			want:     []string{"- Open pull requests: none from fix/pause\n", "- Your last status line: none recorded\n", "None held a turn or waited for one.\n"},
			unwanted: []string{"go test ./..."},
		},
		{
			name:     "on a detached HEAD",
			commands: handoffCommands{branch: "HEAD", head: head},
			want:     []string{"- Branch: none (detached HEAD)\n", "- HEAD: " + head + "\n"},
			unwanted: []string{"pull request"},
		},
		{
			name:     "where neither git nor gh answers",
			commands: handoffCommands{branch: "fix/pause"},
			want:     []string{"- HEAD: could not be read (git rev-parse exited 1: not answered)\n", "- Open pull requests: could not be read (gh pr exited 1: not answered)\n"},
		},
		{
			name:     "where git cannot read the branch",
			commands: handoffCommands{head: head},
			want:     []string{"- Branch: could not be read (git rev-parse exited 1: not answered)\n"},
			unwanted: []string{"pull request"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			store := t.TempDir()
			t.Setenv("CFO_VERIFY_DIR", store)
			meta := state.TaskMeta{ID: "task", Worktree: filepath.Join(stateDir, "worktree")}
			if test.status != "" {
				if err := state.AppendStatus(stateDir, meta.ID, test.status); err != nil {
					t.Fatal(err)
				}
			}
			turn, err := verify.Admission{Dir: filepath.Join(store, "slots"), Available: func() (uint64, error) { return 16 << 30, nil }, Who: "go test ./... in the worktree, task Zora (task)", Budget: time.Hour, Limit: time.Minute, Poll: 5 * time.Millisecond}.Wait(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := turn.Release(); err != nil {
					t.Error(err)
				}
			})
			var ran []string
			test.commands.ran = &ran
			writer := lifecycle.HandoffWriter{StateDir: stateDir, Commands: test.commands, Owner: func(pid int) string {
				if pid == os.Getpid() {
					return test.owner
				}
				return ""
			}}

			// Act
			note := string(writer.Handoff(t.Context(), meta))

			// Assert
			for _, want := range test.want {
				if !strings.Contains(note, want) {
					t.Errorf("handoff lacks %q:\n%s", want, note)
				}
			}
			for _, unwanted := range test.unwanted {
				if strings.Contains(note, unwanted) {
					t.Errorf("handoff names %q:\n%s", unwanted, note)
				}
			}
			for _, command := range ran {
				if strings.HasPrefix(command, "gh ") && test.commands.branch == "HEAD" {
					t.Errorf("a detached HEAD has no branch to list pull requests from, but %q ran", command)
				}
			}
		})
	}
}

// A heavy line that cannot be read is said to be unread, never taken for an
// empty one.
func TestWrittenHandoffNamesAnUnreadHeavyLine(t *testing.T) {
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", "")
	writer := lifecycle.HandoffWriter{StateDir: t.TempDir(), Commands: handoffCommands{branch: "HEAD"}, Owner: func(int) string { return "task" }}

	// Act
	note := string(writer.Handoff(t.Context(), state.TaskMeta{ID: "task"}))

	// Assert
	if !strings.Contains(note, "## Your runs in the machine's heavy line\n\nThey could not be read (") || strings.Contains(note, "None held a turn") {
		t.Errorf("handoff over an unread heavy line:\n%s", note)
	}
}
