package supervisor

import (
	"errors"
	"fmt"
	"os"
	"strings"

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
	// The answer joins what the note held before the board's messages kept
	// in it, as keptMessages reads it, so those messages stay for the resume.
	before, messages := keptMessages(record.ResumeNote)
	note := []string{strings.TrimPrefix(before+"\n"+text, "\n")}
	for _, message := range messages {
		note = append(note, fromOverlord(message))
	}
	record.ResumeNote = strings.Join(note, "\n")
	return true, state.WriteLifecycle(stateDir, record)
}
