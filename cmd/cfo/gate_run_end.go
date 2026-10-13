package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// runEndSendWait bounds how long telling a goblin its run ended may take, as
// typing into its terminal and seeing it taken does.
const runEndSendWait = time.Minute

// runEndWhatReach is how much of a run's name the line that tells its goblin
// it ended carries.
const runEndWhatReach = 200

// tellGoblinRunEnded tells the goblin whose own native terminal ran this run
// how it ended, typed as cfo send types, when the goblin's harness starts no
// turn of its own as a background run ends and the goblin sits idle at its
// prompt. A goblin starts a heavy run in the background, as its brief says,
// and may end its turn while it waits: on 2026-10-13 Codex goblins sat at
// their prompt 17 to 26 minutes after their own prepush had ended, until the
// CFO typed to them. A goblin in a turn is not told: one that waited on the
// run in its turn has read how it ended, and text typed into a turn would
// reach it after that, or start a turn after it said it was done. The run is
// the goblin's own only when its terminal's proof is in this process's
// environment and dir is in one of its worktrees: a gate the goblin started
// inherits that environment, and its steps run in the gate's own worktree.
// What cannot be told is said on stderr and changes no exit code.
func tellGoblinRunEnded(ctx context.Context, runtime commandRuntime, getenv func(string) string, name, dir, what string, exit int, took time.Duration, stderr io.Writer) {
	id, proof := getenv(host.IDVariable), getenv(host.ProofVariable)
	if id == "" || proof == "" || state.ValidTaskID(id) != nil || runtime.resolveHome == nil || runtime.sendText == nil || runtime.screen == nil {
		return
	}
	h, err := runtime.resolveHome()
	if err != nil {
		return
	}
	proofs, err := host.Proofs(h.State, id)
	if err != nil || !slices.Contains(proofs, host.ProofSum(proof)) {
		return
	}
	meta, err := state.ReadTaskMeta(h.State, id)
	kind := harness.Kind(meta.Harness)
	if err != nil || kind.StartsTurnWhenBackgroundEnds() || !inWorktree(dir, append([]string{meta.Worktree}, meta.Extras...)) {
		return
	}
	screens, isReadable := harness.NativeScreens(kind)
	if !isReadable {
		return
	}
	if screen, err := runtime.screen(h.State, id); err != nil || !screens.IsIdleAtEmptyComposer(screen) {
		return
	}
	what = strings.Join(strings.Fields(what), " ")
	if runes := []rune(what); len(runes) > runEndWhatReach {
		what = string(runes[:runEndWhatReach]) + "..."
	}
	text := fmt.Sprintf("Your run %s ended with exit %d after %s; read its output and carry on from it.", what, exit, took.Round(time.Second))
	ctx, cancel := context.WithTimeout(ctx, runEndSendWait)
	defer cancel()
	if err := runtime.sendText(ctx, h, id, text); err != nil && !errors.Is(err, fleet.ErrQueuedForToolCall) {
		fmt.Fprintf(stderr, "%s: %s could not be told that this run ended: %v\n", name, id, err)
	}
}

// inWorktree reports whether dir is one of worktrees or inside one.
func inWorktree(dir string, worktrees []string) bool {
	dir, err := fsx.Canonical(dir)
	if err != nil {
		return false
	}
	for _, worktree := range worktrees {
		if worktree == "" {
			continue
		}
		root, err := fsx.Canonical(worktree)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(strings.ToLower(root), strings.ToLower(dir))
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
