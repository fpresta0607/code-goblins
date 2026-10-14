package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Conversation is the conversation a task's goblin was in, as its own
// running harness proved it: Claude Code's record of its own process, or the
// Codex rollout of the goblin's worktree. The board keeps the last one it
// read for each task in state/conversations/<id>.json, with the generation
// that ran it, so a relaunch once that harness has ended, as a restart, a
// pause or a crash leaves it, resumes the goblin's own conversation without
// the native session hooks, which a home may not have installed.
type Conversation struct {
	ID         string    `json:"id"`
	Generation string    `json:"generation"`
	Harness    string    `json:"harness"`
	Session    string    `json:"session"`
	ProvedAt   time.Time `json:"proved_at"`
}

func conversationPath(directory, id string) string {
	return filepath.Join(directory, "conversations", id+".json")
}

// ReadConversation reads the conversation kept for task id; a task with
// none kept returns an error that is os.ErrNotExist.
func ReadConversation(directory, id string) (Conversation, error) {
	if err := ValidTaskID(id); err != nil {
		return Conversation{}, err
	}
	data, err := fsx.ReadFile(conversationPath(directory, id))
	if err != nil {
		return Conversation{}, err
	}
	var kept Conversation
	if err := json.Unmarshal(data, &kept); err != nil {
		return Conversation{}, fmt.Errorf("read the conversation kept for %s: %w", id, err)
	}
	if kept.ID != id {
		return Conversation{}, errors.New("the conversation kept for " + id + " names another task")
	}
	return kept, nil
}

// WriteConversation keeps kept as its task's conversation.
func WriteConversation(directory string, kept Conversation) error {
	if err := ValidTaskID(kept.ID); err != nil {
		return err
	}
	data, err := json.Marshal(kept)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(directory, "conversations"), 0o700); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(conversationPath(directory, kept.ID), data)
}
