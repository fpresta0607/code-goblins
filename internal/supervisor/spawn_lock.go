package supervisor

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
)

// spawnLockCommands are the cfo commands that take the home's spawn lock for
// their run, so one that meets another holding it fails at once.
var spawnLockCommands = []string{"spawn", "resume"}

// spawnLockWait bounds how long a start waits for the spawn lock to free.
const spawnLockWait = 10 * time.Minute

// runPastTheSpawnLock runs cfo with args, and when a spawn or a resume met
// the home's spawn lock held by another start, such as a cfo spawn the CFO
// runs by hand, runs it once more as soon as that lock frees: the start was
// only waiting its turn.
func (s *Service) runPastTheSpawnLock(dispatch *Dispatch, args []string) (string, error) {
	output, err := dispatch.Spawn(context.Background(), args)
	if err == nil || !slices.Contains(spawnLockCommands, args[0]) || !strings.Contains(output, lock.ErrHeld.Error()) {
		return output, err
	}
	for deadline := time.Now().Add(spawnLockWait); time.Now().Before(deadline) && spawnLockHeld(s.Store.Home.State); {
		time.Sleep(time.Second)
	}
	return dispatch.Spawn(context.Background(), args)
}

// spawnLockHeld says a live process holds the home's spawn lock; a record
// that cannot be read yet, as while it is written, reads as held.
func spawnLockHeld(stateDir string) bool {
	holder, err := lock.ReadNamed(stateDir, ".spawn.lock")
	return !errors.Is(err, os.ErrNotExist) && (err != nil || holder.Alive())
}
