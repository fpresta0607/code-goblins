package janitor

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// Buckets is what the home holds, in bytes, by what holds it: each task's
// worktrees (its own and its extras) and scratch folder, each shared cache,
// the binaries, the fleet's state and the Overlord's data.
type Buckets struct {
	Worktrees map[string]int64 `json:"worktrees,omitempty"`
	Scratch   map[string]int64 `json:"scratch,omitempty"`
	Caches    map[string]int64 `json:"caches,omitempty"`
	Bin       int64            `json:"bin"`
	State     int64            `json:"state"`
	Data      int64            `json:"data"`
}

// Total is the bytes every bucket holds together.
func (b Buckets) Total() int64 {
	total := b.Bin + b.State + b.Data
	for _, group := range []map[string]int64{b.Worktrees, b.Scratch, b.Caches} {
		for _, bytes := range group {
			total += bytes
		}
	}
	return total
}

// Sum is the bytes one group of buckets holds.
func Sum(group map[string]int64) int64 {
	var total int64
	for _, bytes := range group {
		total += bytes
	}
	return total
}

// Measure walks the home's buckets for the tasks given. A task's worktree
// outside the home, where an older build put it, is measured where it is.
func Measure(h home.Home, tasks []state.TaskMeta) Buckets {
	buckets := Buckets{Worktrees: map[string]int64{}, Scratch: map[string]int64{}, Caches: map[string]int64{}}
	for _, meta := range tasks {
		var bytes int64
		for _, path := range append([]string{meta.Worktree}, meta.Extras...) {
			if path != "" {
				bytes += Size(path)
			}
		}
		buckets.Worktrees[meta.ID] = bytes
		if scratch, err := state.TaskScratch(h.State, meta); err == nil {
			buckets.Scratch[meta.ID] = Size(scratch)
		}
	}
	entries, _ := os.ReadDir(h.Caches())
	for _, entry := range entries {
		if entry.IsDir() {
			buckets.Caches[entry.Name()] = Size(filepath.Join(h.Caches(), entry.Name()))
		}
	}
	buckets.Bin = Size(h.Bin())
	buckets.State = Size(h.State)
	buckets.Data = Size(h.Data)
	return buckets
}

// Size is the bytes of every file under path, links not followed; what
// cannot be read counts as nothing.
func Size(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.Type().IsRegular() {
			if info, err := entry.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}
