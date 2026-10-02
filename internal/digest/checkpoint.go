package digest

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

const CheckpointFile = "compact-checkpoint.md"

// WriteCheckpoint snapshots disk state without acknowledging or changing it.
func WriteCheckpoint(h home.Home, now time.Time) error {
	var text bytes.Buffer
	ew := &werr{w: &text}
	ew.printf("CFO compact checkpoint, written %s\n", now.UTC().Format(time.RFC3339))
	ew.println("This holds only what is on disk. Holds and freezes declared only in conversation cannot be recovered here; read the CFO handoff named below.")

	ew.println("\n== FLEET ==")
	scan, err := state.ScanIDs(h.State)
	if err != nil {
		ew.printf("state\\: UNREADABLE (%s)\n", err)
	} else if len(scan.MetaIDs) == 0 {
		ew.println("(no goblins in flight)")
	}
	generations := make(map[string]string)
	for _, id := range scan.MetaIDs {
		meta, err := state.ReadMeta(filepath.Join(h.State, id+".meta"))
		if err != nil {
			ew.printf("%s: metadata UNREADABLE (%s)\n", id, err)
			continue
		}
		generations[id] = meta["spawn_gen"]
		pr := meta["pr"]
		if pr == "" {
			pr = "no pull request"
		}
		ew.printf("%s  %s/%s  %s  %s\n", id, meta["harness"], meta["model"], meta["kind"], pr)
		lines, err := state.TailStatus(h.State, id, 3)
		if err != nil {
			ew.printf("  status UNREADABLE (%s)\n", err)
		} else if len(lines) == 0 {
			ew.println("  no status yet")
		}
		for _, line := range lines {
			ew.println("  " + line)
		}
	}

	ew.println("\n== HOLDS AND FREEZES ==")
	if notice := afk.NoticeFor(h.State); len(notice) != 0 {
		for _, line := range notice {
			ew.println(line)
		}
	} else {
		ew.println("AFK mode is off.")
	}
	hasHold := false
	for _, id := range scan.MetaIDs {
		record, err := state.ReadLifecycle(h.State, id)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			hasHold = true
			ew.printf("%s: lifecycle UNREADABLE (%s)\n", id, err)
			continue
		}
		if generations[id] == "" || record.Generation != generations[id] {
			continue
		}
		switch record.Phase {
		case "pausing", "paused", "resuming", "stopping", "stopped":
			hasHold = true
			ew.printf("%s: %s (%s)\n", id, record.Phase, record.Reason)
		}
	}
	if !hasHold && err == nil {
		ew.println("No goblin is paused or stopped.")
	}

	ew.println("\n== OPEN QUESTIONS ==")
	items, err := supervisor.OpenItems(h.State)
	if err != nil {
		ew.printf("Command Center: UNREADABLE (%s)\n", err)
	} else if len(items) == 0 {
		ew.println("Nothing waits on the Overlord in the Command Center.")
	}
	for _, item := range items {
		task := item.Task
		if task == "" {
			task = "the CFO"
		}
		ew.printf("%s %s (%s): %s\n", item.Kind, item.ID, task, item.Text)
	}
	records, wakeErr := wake.Pending(h.State)
	hasQuestion := false
	for _, record := range records {
		if _, isBlocking := wake.BlockingNotify(record); isBlocking && record.Answered == "" {
			hasQuestion = true
			ew.printf("%s %s\n", record.Key, record.Detail)
		}
	}
	if wakeErr != nil {
		ew.printf("wake queue: UNREADABLE (%s)\n", wakeErr)
	} else if !hasQuestion {
		ew.println("No goblin waits on your answer.")
	}

	ew.println("\n== OWED ==")
	if wakeErr != nil {
		ew.printf("wake queue: UNREADABLE (%s)\n", wakeErr)
	} else if len(records) == 0 {
		ew.println("No wake waits to be acknowledged.")
	} else {
		ew.printf("%d wakes are not acknowledged; run cfo drain to read and handle them.\n", len(records))
		for _, record := range records {
			ew.printf("[%d] %s %s: %s\n", record.Seq, record.Kind, record.Key, record.Detail)
			if record.Answered != "" {
				ew.printf("  answered by %s: %s\n", record.AnsweredBy, record.Answered)
			}
		}
	}

	ew.println("\n== READ THIS NEXT ==")
	entries, err := os.ReadDir(h.Data)
	newest, modified := "", time.Time{}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		ew.printf("data\\: UNREADABLE (%s)\n", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "cfo-handoff-") || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			ew.printf("%s: UNREADABLE (%s)\n", entry.Name(), err)
			continue
		}
		if newest == "" || !info.ModTime().Before(modified) {
			newest, modified = entry.Name(), info.ModTime()
		}
	}
	if newest == "" {
		ew.println("no cfo-handoff-*.md was found in data\\; conversation-only holds need a handoff on disk.")
	} else {
		ew.println(filepath.Join(h.Data, newest))
	}
	ew.println(filepath.Join(h.State, FullDigestFile))
	return fsx.AtomicWriteFile(filepath.Join(h.State, CheckpointFile), text.Bytes())
}
