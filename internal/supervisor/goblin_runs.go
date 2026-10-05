package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// A goblin's wait or question can carry a command for the Overlord to run
// (the Overlord, 2026-10-02: "run in powershell button"). It reaches the board
// as a run item, the card the CFO's own requests use, named for the goblin:
// he reads the exact command and runs it with one click, in a window that
// stays open and usable, so a sign-in or cfo attach works there. The goblin
// hears how it ended, and a command nobody ran leaves the board once the
// goblin moves on.

// goblinRunInbox is where a goblin's notify leaves its command for the
// supervisor. A file there proves nothing about who wrote it, so what the
// supervisor takes from it can only ever be a live goblin's own item. It is
// not the folder the CFO's run requests once came through, which stays
// unread: the CFO's reach the board only over the supervisor's pipe.
func goblinRunInbox(stateDir string) string { return filepath.Join(stateDir, "goblin-runs-inbox") }

// goblinRunID is the ID of the command a goblin's notify seq carries.
func goblinRunID(taskID string, seq int) string { return fmt.Sprintf("run-%s-%d", taskID, seq) }

// PublishGoblinRun hands the command of goblin taskID's notify seq to the
// supervisor from the goblin's own process, proven the way its questions are.
// title says why he should run it, and the command runs in the goblin's
// worktree.
func PublishGoblinRun(h home.Home, taskID string, seq int, title, shell, command string) error {
	meta, err := goblinAsker(h.State, taskID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	r := Run{ID: goblinRunID(taskID, seq), Identity: goblinIdentity(meta), Task: taskID, Title: title, Shell: shell, Command: command, Cwd: meta.Worktree, Interactive: true, State: "ready", CreatedAt: now, ExpiresAt: now.Add(runLifetime)}
	if err := validRun(r); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(goblinRunInbox(h.State), 0700); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(goblinRunInbox(h.State), r.ID+".json"), data)
}

// goblinRun is the run item an inbox record may become: the goblin's own,
// under its own ID, never elevated, always in a usable window in its
// worktree. Everything else the record claims is dropped.
func goblinRun(stateDir string, claimed Run) (Run, error) {
	if claimed.Task == "" {
		return Run{}, errFromInbox
	}
	meta, err := state.ReadTaskMeta(stateDir, claimed.Task)
	switch {
	case err != nil:
		return Run{}, fmt.Errorf("task %s has no live record", claimed.Task)
	case goblinIdentity(meta) != claimed.Identity:
		return Run{}, fmt.Errorf("it is not from the goblin now running %s", claimed.Task)
	case !strings.HasPrefix(claimed.ID, "run-"+claimed.Task+"-"):
		return Run{}, fmt.Errorf("a goblin's command takes an ID of its own, run-%s-<notify>", claimed.Task)
	case claimed.Admin:
		return Run{}, errors.New("a goblin's command never runs as administrator")
	}
	r := Run{ID: claimed.ID, Identity: claimed.Identity, Task: claimed.Task, Title: claimed.Title, Shell: claimed.Shell, Command: claimed.Command, Cwd: meta.Worktree, Interactive: true, State: "ready", CreatedAt: claimed.CreatedAt}
	return r, validRun(r)
}

// ingestGoblinRuns takes each goblin's command from the inbox onto the board,
// its script written as the file Run executes. A record that is not a live
// goblin's own is refused, said once among the board's issues, and removed.
func (s *Store) ingestGoblinRuns() error {
	entries, err := os.ReadDir(goblinRunInbox(s.Home.State))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries[:min(len(entries), 2*maxRuns)] {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(goblinRunInbox(s.Home.State), entry.Name())
		invalid := s.ingestGoblinRun(path)
		if errors.Is(invalid, ErrStorage) {
			return invalid
		}
		if errors.Is(invalid, ErrDeferred) {
			continue
		}
		if invalid != nil {
			s.mu.Lock()
			s.db.Issues = append(s.db.Issues, "Run request rejected: "+bounded(invalid.Error(), 300))
			if len(s.db.Issues) > 20 {
				s.db.Issues = s.db.Issues[len(s.db.Issues)-20:]
			}
			err := s.save()
			s.mu.Unlock()
			if err != nil {
				return err
			}
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ingestGoblinRun(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() > 8*maxRunCommand {
		return errors.New("run request exceeds its size limit")
	}
	data, err := fsx.ReadFile(path)
	if err != nil {
		return err
	}
	var claimed Run
	if json.Unmarshal(data, &claimed) != nil {
		return errors.New("invalid run request JSON")
	}
	r, err := goblinRun(s.Home.State, claimed)
	if err != nil {
		return err
	}
	return s.admitRun(r)
}

// admitRun writes a new run item's script, the file Run executes, and records
// the item; a refused item leaves nothing behind.
func (s *Store) admitRun(r Run) error {
	dir := runDir(s.Home.State, r)
	name, script := runScript(r)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := fsx.AtomicWriteFile(filepath.Join(dir, name), script); err != nil {
		return errors.Join(err, os.RemoveAll(dir))
	}
	r.ScriptSum = runDigest(script)
	if err := s.acceptRun(r); err != nil {
		return errors.Join(err, os.RemoveAll(dir))
	}
	return nil
}

// goblinRunStands reports whether the goblin still waits on the Overlord to
// run r: its wait on him, or the question that carried the command, is still
// what it last said, from before the command reached the board.
func goblinRunStands(r Run, lines []string) bool {
	latestAt, latest := latestReport(lines, time.Time{})
	standingAt, standing := standingReport(lines, time.Time{})
	return strings.HasPrefix(standing, "waiting on overlord: ") && !standingAt.After(r.CreatedAt) ||
		strings.HasPrefix(latest, "blocked: ") && !latestAt.After(r.CreatedAt)
}

// retireGoblinRuns withdraws each goblin's command nobody ran once nobody
// waits on it: its goblin is gone or was replaced, or reported something
// newer, as its wait on the Overlord is retired.
func (s *Store) retireGoblinRuns() error {
	for _, r := range s.Snapshot().Runs {
		if r.Task == "" || r.State != "ready" || !time.Now().Before(r.ExpiresAt) {
			continue
		}
		meta, err := state.ReadTaskMeta(s.Home.State, r.Task)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		var reason string
		if err != nil || goblinIdentity(meta) != r.Identity {
			reason = r.Task + " is gone"
		} else {
			lines, err := state.TailStatus(s.Home.State, r.Task, 50)
			if err != nil {
				return err
			}
			if goblinRunStands(r, lines) {
				continue
			}
			_, latest := latestReport(lines, time.Time{})
			reason = r.Task + " reported again: " + latest
		}
		if err := s.withdrawRun(r.ID, bounded(reason, 1900)); err != nil {
			return err
		}
	}
	return nil
}
