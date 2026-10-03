package supervisor

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
)

// NativePromptSince reports whether the harness of task taskID, in spawn
// generation generation, reported through its native hooks taking a prompt
// for a turn at or after since: in an event still waiting in the spool, or in
// its session as the supervisor recorded it. A harness without native hooks
// never does.
func NativePromptSince(stateDir, taskID, generation string, since time.Time) (bool, error) {
	return promptSince(stateDir, since, func(e nativehook.Event) bool {
		return e.TaskID == taskID && e.Generation == generation
	}, func(s Session) bool {
		return s.TaskID == taskID && s.Generation == generation
	})
}

// NativeHostPromptSince reports whether the exact native CFO recipient
// reported through its hooks taking a prompt at or after since.
func NativeHostPromptSince(stateDir string, recipient nativehook.CFORecipient, since time.Time) (bool, error) {
	return promptSince(stateDir, since, func(e nativehook.Event) bool {
		return e.Validate() == nil && e.Role == "cfo" && e.HostID == recipient.HostID && e.Harness == recipient.Harness && e.SessionID == recipient.SessionID && recipient.Matches(e.Recipient)
	}, func(s Session) bool {
		return s.Role == "cfo" && s.Harness == recipient.Harness && s.NativeID == recipient.SessionID && recipient.Matches(s.PromptRecipient)
	})
}

// promptSince finds a prompt taken at or after since by a harness whose event
// or recorded session is ours.
func promptSince(stateDir string, since time.Time, ours func(nativehook.Event) bool, oursSession func(Session) bool) (bool, error) {
	matches := func(e nativehook.Event) bool {
		return e.Prompt && ours(e) && !e.OccurredAt.Before(since)
	}
	dir := nativehook.SpoolDir(stateDir)
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".event.json") {
			continue
		}
		data, err := fsx.ReadFile(filepath.Join(dir, entry.Name()))
		if errors.Is(err, fs.ErrNotExist) {
			// The supervisor took it into its store meanwhile.
			continue
		}
		if err != nil {
			return false, err
		}
		var e nativehook.Event
		if len(data) <= nativehook.MaxInputBytes && json.Unmarshal(data, &e) == nil && matches(e) {
			return true, nil
		}
	}
	data, err := fsx.ReadFile(filepath.Join(stateDir, ".supervisor.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if len(data) > maxStateBytes {
		return false, errors.New("supervisor state exceeds its bound")
	}
	var db Database
	if err := json.Unmarshal(data, &db); err != nil {
		return false, err
	}
	for _, session := range db.Sessions {
		if oursSession(session) && !session.PromptAt.IsZero() && !session.PromptAt.Before(since) {
			return true, nil
		}
	}
	return false, nil
}
