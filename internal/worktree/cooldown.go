package worktree

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// CooldownDays is how long a release must have been public before a goblin's
// package manager resolves it. A hijacked release is found and pulled within
// days: on 2026-10-21 one reached three goblins within ten minutes of its
// upload, and was pulled from its index the same day.
const CooldownDays = 7

// Cooldown returns the variables that hold uv, pip and npm in a goblin's
// terminal to releases uploaded at least CooldownDays ago, and the uv.lock
// files the worktree tracks.
//
// uv's variable is left out where the worktree tracks a uv.lock: uv sets
// aside a lock that does not record the cutoff its environment names, so
// `uv sync --locked` fails and `uv run` rewrites the lock, a change no goblin
// authored. Such a project records its own cutoff, as [tool.uv]
// exclude-newer in pyproject.toml, which uv then keeps in its lock.
//
// npm before 11.10 takes no age, only a date, so its cutoff is the date
// CooldownDays before now, fixed for as long as the terminal runs.
func (s Service) Cooldown(ctx context.Context, worktreePath string, now time.Time) (env map[string]string, uvLocks []string, err error) {
	if s.Commands == nil {
		return nil, nil, errors.New("worktree: command runner is required to read the worktree's uv.lock files")
	}
	result, err := s.Commands.Run(ctx, execx.Request{Dir: worktreePath, Name: "git", Args: []string{"ls-files", "-z", "--", ":(glob)**/uv.lock"}})
	if err == nil && result.ExitCode != 0 {
		err = commandFailure("git ls-files", result)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("worktree: list the worktree's uv.lock files: %w", err)
	}
	for _, name := range strings.Split(string(result.Stdout), "\x00") {
		if name != "" {
			uvLocks = append(uvLocks, name)
		}
	}
	env = map[string]string{
		"PIP_UPLOADED_PRIOR_TO": "P" + strconv.Itoa(CooldownDays) + "D",
		"npm_config_before":     now.UTC().AddDate(0, 0, -CooldownDays).Format(time.RFC3339),
	}
	if len(uvLocks) == 0 {
		env["UV_EXCLUDE_NEWER"] = strconv.Itoa(CooldownDays) + " days"
	}
	return env, uvLocks, nil
}
