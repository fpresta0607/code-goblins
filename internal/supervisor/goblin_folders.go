package supervisor

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/disk"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fleetconfig"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/janitor"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const (
	// folderNoticeWithin bounds one send of the folder notice.
	folderNoticeWithin = 30 * time.Second
	// folderNoticeTries is how many sends of the folder notice to one goblin
	// may fail before the supervisor stops sending it and says so.
	folderNoticeTries = 3
)

// folderNotice is whether a goblin was told its folders passed goblin_disk_gb,
// and how many sends to it failed.
type folderNotice struct {
	IsTold bool `json:"told,omitempty"`
	Failed int  `json:"failed,omitempty"`
}

// checkGoblinFolders tells each live goblin whose worktrees or scratch folder
// the janitor's last sweep measured past goblin_disk_gb, once in its task's
// life, to clear what it can rebuild, keeping every file its reports, cards
// and questions name. The fleet removes nothing of a live or paused task's on
// its own; a paused goblin is told once it runs again. A send that fails is
// tried again on the next reading, folderNoticeTries times at most.
func (s *Service) checkGoblinFolders(ctx context.Context, w *fleetWakes) error {
	if s.Options.CEO == nil {
		return nil
	}
	settings, err := fleetconfig.Read(s.Store.Home.Root)
	if err != nil {
		return nil
	}
	record, err := janitor.ReadRecord(s.Store.Home.State)
	if err != nil {
		return nil
	}
	limit := int64(fleetconfig.Bytes(settings.GoblinDiskGB))
	live := map[string]bool{}
	var errs []error
	for _, meta := range liveTasks(s.Store.Home.State) {
		key := folderKey(meta)
		live[key] = true
		// The sweep measures by task id, so a sweep from before this task was
		// first spawned measured an earlier task under the same id.
		generation := firstGeneration(meta)
		spawned, err := strconv.ParseInt(strings.TrimPrefix(generation, "s"), 10, 64)
		if err != nil || !strings.HasPrefix(generation, "s") || record.Time.Before(time.Unix(0, spawned)) {
			continue
		}
		worktrees, scratch := record.Buckets.Worktrees[meta.ID], record.Buckets.Scratch[meta.ID]
		notice := w.GoblinFolders[key]
		if worktrees <= limit && scratch <= limit || notice.IsTold || notice.Failed >= folderNoticeTries {
			continue
		}
		if terminal, err := host.ReadRecord(s.Options.CEO.State, meta.ID); err != nil || !host.Running(terminal) {
			continue
		}
		sending, cancel := context.WithTimeout(ctx, folderNoticeWithin)
		_, err = s.Options.CEO.SendGoblin(sending, meta.ID, goblinIdentity(meta), folderNoticeText(worktrees, scratch, settings.GoblinDiskGB))
		cancel()
		switch {
		case err == nil || errors.Is(err, fleet.ErrQueuedForToolCall):
			notice.IsTold = true
		default:
			notice.Failed++
			if notice.Failed >= folderNoticeTries {
				errs = append(errs, fmt.Errorf("goblin folders: %s could not be told its folders passed goblin_disk_gb after %d tries: %w", meta.ID, notice.Failed, err))
			}
		}
		if w.GoblinFolders == nil {
			w.GoblinFolders = map[string]folderNotice{}
		}
		w.GoblinFolders[key] = notice
	}
	for key := range w.GoblinFolders {
		if !live[key] {
			delete(w.GoblinFolders, key)
		}
	}
	return errors.Join(errs...)
}

// folderKey names a task by its id and the generation it was first spawned
// on, so a later task under the same id is told again.
func folderKey(meta state.TaskMeta) string {
	return meta.ID + "@" + firstGeneration(meta)
}

// firstGeneration is the generation a task was first spawned on, which a
// relaunch does not move. A task an older build spawned records no first
// generation, and gives its spawn's.
func firstGeneration(meta state.TaskMeta) string {
	if meta.FirstGen == "" {
		return meta.SpawnGen
	}
	return meta.FirstGen
}

// folderNoticeText is the notice a goblin whose folders passed limitGB gets.
func folderNoticeText(worktrees, scratch int64, limitGB float64) string {
	var over []string
	if worktrees > int64(fleetconfig.Bytes(limitGB)) {
		over = append(over, fmt.Sprintf("your worktrees hold %.1f GB", disk.GB(uint64(worktrees))))
	}
	if scratch > int64(fleetconfig.Bytes(limitGB)) {
		over = append(over, fmt.Sprintf("your scratch folder holds %.1f GB", disk.GB(uint64(scratch))))
	}
	return fmt.Sprintf("Disk: %s, past the %.0f GB one goblin's folders may hold (goblin_disk_gb), and the fleet shares the drive. Clear now what you can rebuild: build output, test homes, logs you no longer read and dependency folders you can install again. Keep every file your reports, cards and questions name, and all uncommitted work. You are told this once.", strings.Join(over, " and "), limitGB)
}
