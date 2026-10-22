package supervisor

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// The kernel's pools are tracked for each Windows sign-in from the memory
// reading the supervisor takes once a minute, so a pool that grows for days
// names the tag behind it. On 2026-10-18, four days after its boot, this PC's
// kernel held 7.61 GB of paged pool and 3.35 GB of nonpaged pool, against
// 0.68 and 1.15 GB right after the restart that freed them, and nothing had
// kept which tag grew.

const (
	// kernelPoolsFile is this sign-in's record of the pools, and
	// kernelPoolsBeforeFile the sign-in's before it, kept so the record of a
	// leak survives the restart that frees it.
	kernelPoolsFile       = "kernel-pools.json"
	kernelPoolsBeforeFile = "kernel-pools-before.json"
	// poolSampleEvery is how often a memory reading keeps the pools' sizes
	// and reads what each tag holds.
	poolSampleEvery = 15 * time.Minute
	// maxPoolSamples keeps two weeks of samples, the oldest dropped first.
	maxPoolSamples = 14 * 24 * 4
	// poolTagKept is the least a tag holds in either pool for a sample to
	// keep it: about a hundred of the kernel's three thousand tags, and
	// every one that holds enough to matter.
	poolTagKept = 1 << 20
	// poolGrowthShown is how many of the tags that grew most since the
	// sign-in began the board names.
	poolGrowthShown = 3
)

// PoolTag is how much of the kernel's paged and nonpaged pools one pool tag
// holds, in bytes, and how many allocations each pool made under it since
// the PC started and how many of the paged pool's it freed, each count
// wrapping at 2^32. The record keeps the counts only of the tags that count
// processes and tokens.
type PoolTag struct {
	Tag            string `json:"tag"`
	Paged          uint64 `json:"paged"`
	Nonpaged       uint64 `json:"nonpaged"`
	PagedAllocs    uint32 `json:"-"`
	PagedFrees     uint32 `json:"-"`
	NonpagedAllocs uint32 `json:"-"`
}

// PoolGrowth is a pool tag that grew in one pool, paged or nonpaged, since
// this sign-in's first sample: how much it grew and how much it holds now.
type PoolGrowth struct {
	Tag   string `json:"tag"`
	Pool  string `json:"pool"`
	Grown uint64 `json:"grown"`
	Holds uint64 `json:"holds"`
}

// poolSample is one reading of the pools: when it was taken and their sizes,
// and in the first and last sample of a sign-in what each tag held.
// Processes counts the processes the PC started since it booted, the
// kernel's process objects (pool tag Proc), wrapping at 2^32, and Tokens is
// how many tokens the kernel holds (pool tag Toke): on 2026-10-19 about two
// in five processes left their token behind for good, so the two read side
// by side over hours say how many tokens each process start leaks.
type poolSample struct {
	At        time.Time `json:"at"`
	Paged     uint64    `json:"paged"`
	Nonpaged  uint64    `json:"nonpaged"`
	Processes uint32    `json:"processes,omitempty"`
	Tokens    int64     `json:"tokens,omitempty"`
	Tags      []PoolTag `json:"tags,omitempty"`
}

// kernelPools is one sign-in's record of the kernel's pools: its first and
// its last sample with their tags, and every sample's sizes, oldest first.
// Board is every process the board started since BoardStarted, by program
// and by the code that started it, as of the last sample.
type kernelPools struct {
	SignedIn     time.Time       `json:"signed_in"`
	First        poolSample      `json:"first"`
	Last         poolSample      `json:"last"`
	Samples      []poolSample    `json:"samples"`
	BoardStarted time.Time       `json:"board_started,omitzero"`
	Board        []execx.Started `json:"board,omitempty"`
}

// keepPools keeps a sample of the pools memory read, now, once every
// poolSampleEvery: the pools' sizes, and what each tag holds, beside the
// first sample of this sign-in, which names the tags that grew since. A
// board that reads no pool tags, or no Windows sign-in, keeps nothing.
func (s *Service) keepPools(memory Memory, now time.Time) error {
	readTags := s.Options.Dispatch.PoolTags
	if readTags == nil {
		return nil
	}
	s.mu.Lock()
	isDue := s.poolsKept.IsZero() || now.Sub(s.poolsKept) >= poolSampleEvery
	s.mu.Unlock()
	if !isDue {
		return nil
	}
	signedIn, err := s.currentSignIn()
	if err != nil {
		return nil
	}
	tags, err := readTags()
	if err != nil {
		return fmt.Errorf("the kernel's pool tags cannot be read: %w", err)
	}
	sample := poolSample{At: now, Paged: memory.PagedPool, Nonpaged: memory.NonpagedPool}
	for _, tag := range tags {
		switch tag.Tag {
		case "Proc":
			sample.Processes = tag.NonpagedAllocs
		case "Toke":
			sample.Tokens = int64(tag.PagedAllocs) - int64(tag.PagedFrees)
		}
		if tag.Paged >= poolTagKept || tag.Nonpaged >= poolTagKept {
			sample.Tags = append(sample.Tags, tag)
		}
	}
	path := filepath.Join(s.Store.Home.State, kernelPoolsFile)
	record, err := readKernelPools(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		record = kernelPools{SignedIn: signedIn, First: sample}
	case err != nil:
		return err
	case !record.SignedIn.Equal(signedIn):
		if err := os.Rename(path, filepath.Join(s.Store.Home.State, kernelPoolsBeforeFile)); err != nil {
			return fmt.Errorf("the last sign-in's record of the kernel's pools cannot be kept: %w", err)
		}
		record = kernelPools{SignedIn: signedIn, First: sample}
	}
	record.Last = sample
	record.Samples = append(record.Samples, poolSample{At: now, Paged: sample.Paged, Nonpaged: sample.Nonpaged, Processes: sample.Processes, Tokens: sample.Tokens})
	record.BoardStarted, record.Board = s.Started, execx.Starts()
	record.Samples = record.Samples[max(0, len(record.Samples)-maxPoolSamples):]
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := fsx.AtomicWriteFile(path, data); err != nil {
		return fmt.Errorf("the record of the kernel's pools cannot be written: %w", err)
	}
	s.mu.Lock()
	s.poolsKept, s.poolGrowth = now, poolGrowth(record.First, record.Last)
	s.mu.Unlock()
	return nil
}

func readKernelPools(path string) (kernelPools, error) {
	data, err := fsx.ReadFile(path)
	if err != nil {
		return kernelPools{}, err
	}
	var record kernelPools
	if err := json.Unmarshal(data, &record); err != nil {
		return kernelPools{}, fmt.Errorf("the record of the kernel's pools cannot be read: %w", err)
	}
	return record, nil
}

// poolGrowth names the tags that grew most in either pool from first to
// last, the most first. A tag first held too little to be kept grew from
// nothing.
func poolGrowth(first, last poolSample) []PoolGrowth {
	before := map[string]PoolTag{}
	for _, tag := range first.Tags {
		before[tag.Tag] = tag
	}
	var grown []PoolGrowth
	for _, tag := range last.Tags {
		was := before[tag.Tag]
		if tag.Paged > was.Paged {
			grown = append(grown, PoolGrowth{Tag: tag.Tag, Pool: "paged", Grown: tag.Paged - was.Paged, Holds: tag.Paged})
		}
		if tag.Nonpaged > was.Nonpaged {
			grown = append(grown, PoolGrowth{Tag: tag.Tag, Pool: "nonpaged", Grown: tag.Nonpaged - was.Nonpaged, Holds: tag.Nonpaged})
		}
	}
	slices.SortStableFunc(grown, func(a, b PoolGrowth) int { return cmp.Compare(b.Grown, a.Grown) })
	return grown[:min(len(grown), poolGrowthShown)]
}

// currentSignIn is when this Windows sign-in began, on a board that brings
// the fleet back after a restart.
func (s *Service) currentSignIn() (time.Time, error) {
	if s.Options.Comeback == nil || s.Options.Example {
		return time.Time{}, errors.New("this board reads no Windows sign-in")
	}
	s.comeback.Lock()
	defer s.comeback.Unlock()
	return s.signInBegan()
}
