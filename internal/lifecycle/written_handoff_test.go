package lifecycle_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/githubtest"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// handoffCommands answers the git and gh commands and the GitHub API requests
// a written handoff reads, and records them. An empty answer is a command or
// request that fails. origin is the worktree's origin remote, the github.com
// repository handoffOrigin unless set: GitHub's API lists the open pull
// requests, pulls, from branch of a github.com origin and gh fails to, and gh
// lists them for an origin on another host.
type handoffCommands struct {
	branch, head, pulls string
	origin              string
	ran                 *[]string
}

// handoffOrigin is a worktree's origin on github.com.
const handoffOrigin = "https://github.com/owner/repo.git"

func (commands handoffCommands) isOnGitHub() bool {
	return commands.origin == "" || commands.origin == handoffOrigin
}

func (commands handoffCommands) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	command := request.Name + " " + strings.Join(request.Args, " ")
	if commands.ran != nil {
		*commands.ran = append(*commands.ran, command)
	}
	origin := commands.origin
	if origin == "" {
		origin = handoffOrigin
	}
	listed := "gh pr list --head " + commands.branch + " --state open --json url,isDraft"
	if command == listed && commands.isOnGitHub() {
		return execx.Result{}, errors.New("gh started for a read GitHub's API answers: " + command)
	}
	answers := map[string]string{
		"git rev-parse --abbrev-ref HEAD": commands.branch,
		"git rev-parse HEAD":              commands.head,
		listed:                            commands.pulls,
	}
	if len(request.Args) == 5 && request.Args[0] == "-C" && request.Args[2] == "remote" {
		answers[command] = origin
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

// ServeHTTP answers GitHub's API's list of the open pull requests of
// owner/repo from branch with pulls, and fails it for an origin on another
// host.
func (commands handoffCommands) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	query, variables, err := githubtest.Query(request)
	if commands.ran != nil {
		*commands.ran = append(*commands.ran, request.Method+" "+strings.TrimPrefix(request.URL.Path, "/"))
	}
	switch {
	case err != nil:
		githubtest.Refuse(w, http.StatusBadRequest, err.Error())
	case !commands.isOnGitHub():
		githubtest.Refuse(w, http.StatusInternalServerError, "GitHub's API was asked of an origin on another host")
	case commands.pulls == "":
		githubtest.Refuse(w, http.StatusBadGateway, "Bad Gateway")
	case !strings.Contains(query, "pullRequests(states:OPEN") || variables["owner"] != "owner" || variables["name"] != "repo" || variables["head"] != commands.branch:
		githubtest.Refuse(w, http.StatusBadRequest, "not the list of owner/repo's open pull requests from "+commands.branch)
	default:
		_, _ = w.Write([]byte(`{"data":{"repository":{"pullRequests":{"nodes":` + commands.pulls + `}}}}`))
	}
}

func TestWrittenHandoffSaysWhereTheTaskStands(t *testing.T) {
	head := strings.Repeat("a", 40)
	for _, test := range []struct {
		name     string
		commands handoffCommands
		status   string
		owner    string
		// isWaiting has a second run of the task's wait in line behind
		// the turn its first holds.
		isWaiting bool
		want      []string
		unwanted  []string
	}{
		{
			name:      "on a branch with a draft pull request and a turn",
			commands:  handoffCommands{branch: "fix/pause", head: head, pulls: `[{"url":"https://github.com/owner/repo/pull/7","title":"Pause writes the handoff","isDraft":true}]`},
			status:    "waiting on memory: the suite waits in the heavy line",
			owner:     "task",
			isWaiting: true,
			want: []string{
				"# Handoff for task", "You wrote no handoff of your own by the pause's deadline, so the pause wrote this one for you",
				"- Branch: fix/pause\n", "- HEAD: " + head + "\n",
				"- Open pull request: https://github.com/owner/repo/pull/7 (draft)\n",
				"- Your last status line: ", "waiting on memory: the suite waits in the heavy line\n",
				"start again each you still need", "- holding its turn since ", ": go test ./... in the worktree, task Zora (task) (pid ",
				"- waiting in line since ", ": go build ./... in the worktree, task Zora (task) (pid ",
			},
			unwanted: []string{"Pause writes the handoff"},
		},
		{
			name:     "with no open pull request and another task's turn",
			commands: handoffCommands{branch: "fix/pause", head: head, pulls: "[]"},
			owner:    "other",
			want:     []string{"- Open pull requests: none from fix/pause\n", "- Your last status line: none recorded\n", "None of the 1 runs in the line was found to be yours.\n"},
			unwanted: []string{"go test ./...", "None held a turn"},
		},
		{
			name:     "on a branch whose origin is on another host",
			commands: handoffCommands{branch: "fix/pause", head: head, origin: "https://git.example.com/owner/repo.git", pulls: `[{"url":"https://git.example.com/owner/repo/pull/7","isDraft":false}]`},
			want:     []string{"- Open pull request: https://git.example.com/owner/repo/pull/7\n"},
		},
		{
			name:     "on a detached HEAD",
			commands: handoffCommands{branch: "HEAD", head: head},
			want:     []string{"- Branch: none (detached HEAD)\n", "- HEAD: " + head + "\n"},
			unwanted: []string{"pull request"},
		},
		{
			name:     "where neither git nor GitHub answers",
			commands: handoffCommands{branch: "fix/pause"},
			want:     []string{"- HEAD: could not be read (git rev-parse exited 1: not answered)\n", "- Open pull requests: could not be read (", "Bad Gateway"},
		},
		{
			name:     "where gh does not answer for an origin on another host",
			commands: handoffCommands{branch: "fix/pause", head: head, origin: "https://git.example.com/owner/repo.git"},
			want:     []string{"- Open pull requests: could not be read (gh pr exited 1: not answered)\n"},
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
			t.Setenv("GOBLINS_VERIFY_DIR", store)
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
			if test.isWaiting {
				waiting, stopWaiting := context.WithCancel(t.Context())
				waited := make(chan struct{})
				waitingRun := "go build ./... in the worktree, task Zora (task)"
				go func() {
					defer close(waited)
					if turn, err := (verify.Admission{Dir: filepath.Join(store, "slots"), Available: func() (uint64, error) { return 16 << 30, nil }, Who: waitingRun, Budget: time.Hour, Limit: time.Minute, Poll: 5 * time.Millisecond}).Wait(waiting); err == nil {
						t.Error("the second run took the turn the first holds")
						_ = turn.Release()
					}
				}()
				t.Cleanup(func() { stopWaiting(); <-waited })
				// A run joins the line before it writes the card that names
				// it, so the line counts it before it can be read by name.
				for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(10 * time.Millisecond) {
					if _, line, err := verify.MachineLine(); err == nil && len(line) == 1 && line[0].Who == waitingRun {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("the second run never joined the line under its name")
					}
				}
			}
			var ran []string
			test.commands.ran = &ran
			writer := lifecycle.HandoffWriter{StateDir: stateDir, Commands: test.commands, GitHub: githubtest.Client(test.commands), Owner: func(pid int) string {
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
				isListing := strings.HasPrefix(command, "gh ") || strings.HasPrefix(command, "POST ")
				if isListing && test.commands.branch == "HEAD" {
					t.Errorf("a detached HEAD has no branch to list pull requests from, but %q ran", command)
				}
				if strings.HasPrefix(command, "gh ") && test.commands.isOnGitHub() {
					t.Errorf("GitHub's API lists the pull requests of a github.com origin, but %q ran", command)
				}
			}
		})
	}
}

// A heavy line that cannot be read is said to be unread, never taken for an
// empty one.
func TestWrittenHandoffNamesAnUnreadHeavyLine(t *testing.T) {
	// Arrange
	t.Setenv("GOBLINS_VERIFY_DIR", "")
	writer := lifecycle.HandoffWriter{StateDir: t.TempDir(), Commands: handoffCommands{branch: "HEAD"}, GitHub: githubtest.Client(handoffCommands{}), Owner: func(int) string { return "task" }}

	// Act
	note := string(writer.Handoff(t.Context(), state.TaskMeta{ID: "task"}))

	// Assert
	if !strings.Contains(note, "## Your runs in the machine's heavy line\n\nThey could not be read (") || strings.Contains(note, "None held a turn") {
		t.Errorf("handoff over an unread heavy line:\n%s", note)
	}
}
