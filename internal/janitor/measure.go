package janitor

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

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
	// Incomplete names the buckets that held an entry the sweep could not
	// read, whose bytes are then what it could count, not all they hold: a
	// worktree or scratch folder by its task, a cache by its folder, or bin,
	// state or data.
	Incomplete []string `json:"incomplete,omitempty"`
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
	measure := func(bucket, path string) int64 {
		bytes, isWhole := sizeOf(path)
		if !isWhole && !slices.Contains(buckets.Incomplete, bucket) {
			buckets.Incomplete = append(buckets.Incomplete, bucket)
		}
		return bytes
	}
	for _, meta := range tasks {
		var bytes int64
		for _, path := range append([]string{meta.Worktree}, meta.Extras...) {
			if path != "" {
				bytes += measure("worktree "+meta.ID, path)
			}
		}
		buckets.Worktrees[meta.ID] = bytes
		if scratch, err := state.TaskScratch(h.State, meta); err == nil {
			buckets.Scratch[meta.ID] = measure("scratch "+meta.ID, scratch)
		}
	}
	entries, _ := os.ReadDir(h.Caches())
	for _, entry := range entries {
		if entry.IsDir() {
			buckets.Caches[entry.Name()] = measure("cache "+entry.Name(), filepath.Join(h.Caches(), entry.Name()))
		}
	}
	buckets.Bin = measure("bin", h.Bin())
	buckets.State = measure("state", h.State)
	buckets.Data = measure("data", h.Data)
	return buckets
}

// Size is the bytes of every file under path that can be read, links not
// followed.
func Size(path string) int64 {
	bytes, _ := sizeOf(path)
	return bytes
}

// sizeOf is Size and whether it read every entry under path. A path that is
// not there holds nothing, and an entry gone while it was walked is read.
func sizeOf(path string) (bytes int64, isWhole bool) {
	isWhole = true
	_ = filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
		if err == nil && entry.Type().IsRegular() {
			var info fs.FileInfo
			if info, err = entry.Info(); err == nil {
				bytes += info.Size()
			}
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			isWhole = false
		}
		return nil
	})
	return bytes, isWhole
}
