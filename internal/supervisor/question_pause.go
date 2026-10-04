package supervisor

import (
	"errors"
	"fmt"
	"os"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func savePausedAnswer(stateDir, task, identity, text string) (isSaved bool, err error) {
	name := ".lifecycle-" + task + ".lock"
	if _, err := lock.AcquireExclusiveNamed(stateDir, name); err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(stateDir, name)) }()
	meta, err := state.ReadTaskMeta(stateDir, task)
	if err != nil {
		return false, err
	}
	if goblinIdentity(meta) != identity {
		return false, fmt.Errorf("%w: the question belongs to an earlier task session; nothing was sent", ErrRejected)
	}
	record, err := state.ReadLifecycle(stateDir, task)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if record.Generation != meta.SpawnGen || record.Phase != "paused" {
		return false, nil
	}
	record.ResumeNote = text
	return true, state.WriteLifecycle(stateDir, record)
}
