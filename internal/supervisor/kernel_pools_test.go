package supervisor

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// The record of the kernel's pools rides the supervisor's minute memory
// reading: it keeps a sample once every quarter hour, what each tag holding
// a megabyte or more held in the sign-in's first sample and its last, and the
// board names the tags that grew most since. A new sign-in keeps the last
// one's record beside its own, since the restart that frees a leak also ends
// the sign-in that recorded it.
func TestThePoolsAreKeptEveryQuarterHourAndNameTheTagsThatGrew(t *testing.T) {
	// Arrange
	const megabyte = 1 << 20
	signedIn := time.Date(2026, 10, 18, 11, 13, 27, 0, time.UTC)
	handler, h := startBoardWith(t, Memory{Available: 8 * gigabyte, Total: 32 * gigabyte, CommitAvailable: 40 * gigabyte, CommitLimit: 48 * gigabyte, PagedPool: 2 * gigabyte, NonpagedPool: gigabyte}, &spawnRecorder{})
	s := handler.Service
	s.Options.Comeback = &Comeback{SignedIn: func() (time.Time, error) { return signedIn, nil }}
	reads := 0
	s.Options.Dispatch.PoolTags = func() ([]PoolTag, error) {
		grown := uint64(reads) * 300 * megabyte
		reads++
		return []PoolTag{
			{Tag: "FMfn", Paged: 300*megabyte + grown},
			{Tag: "MSb+", Nonpaged: 100*megabyte + grown/3},
			{Tag: "Toke", Paged: 280 * megabyte},
			{Tag: "Tiny", Paged: 4096},
		}, nil
	}
	now := time.Now().UTC().Truncate(time.Minute)

	// Act
	readFleet(t, s, now, 0, 1, 14, 15, 29, 30)
	snapshot, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	next := &Service{Store: s.Store, Options: s.Options}
	next.Options.Comeback = &Comeback{SignedIn: func() (time.Time, error) { return signedIn.Add(96 * time.Hour), nil }}
	if err := next.keepPools(Memory{PagedPool: gigabyte}, now.Add(96*time.Hour)); err != nil {
		t.Fatal(err)
	}

	// Assert
	if reads != 4 {
		t.Errorf("the pool tags were read %d times, want once at each quarter hour and once in the next sign-in", reads)
	}
	before, err := readKernelPools(filepath.Join(h.State, kernelPoolsBeforeFile))
	if err != nil {
		t.Fatal(err)
	}
	var at []time.Time
	for _, sample := range before.Samples {
		at = append(at, sample.At)
	}
	if !before.SignedIn.Equal(signedIn) || !slices.Equal(at, []time.Time{now, now.Add(15 * time.Minute), now.Add(30 * time.Minute)}) || before.Samples[0].Paged != 2*gigabyte || before.Samples[0].Nonpaged != gigabyte {
		t.Errorf("the last sign-in's record = %+v, want its three quarter-hour samples with the pools' sizes", before)
	}
	if !slices.Equal(before.First.Tags, []PoolTag{{Tag: "FMfn", Paged: 300 * megabyte}, {Tag: "MSb+", Nonpaged: 100 * megabyte}, {Tag: "Toke", Paged: 280 * megabyte}}) || before.Last.Tags[0].Paged != 900*megabyte {
		t.Errorf("the record's first tags = %+v and last = %+v, want each tag of a megabyte or more as first and last read", before.First.Tags, before.Last.Tags)
	}
	want := []PoolGrowth{{Tag: "FMfn", Pool: "paged", Grown: 600 * megabyte, Holds: 900 * megabyte}, {Tag: "MSb+", Pool: "nonpaged", Grown: 200 * megabyte, Holds: 300 * megabyte}}
	if snapshot.Memory == nil || !slices.Equal(snapshot.Memory.PoolGrowth, want) || snapshot.Memory.PoolLine != poolLine {
		t.Errorf("the board's memory = %+v, want the tags that grew since the sign-in began and the line", snapshot.Memory)
	}
	current, err := readKernelPools(filepath.Join(h.State, kernelPoolsFile))
	if err != nil || !current.SignedIn.Equal(signedIn.Add(96*time.Hour)) || len(current.Samples) != 1 {
		t.Errorf("the new sign-in's record = %+v, %v, want one sample of its own", current, err)
	}
}

// A board that reads no pool tags keeps no record.
func TestABoardThatReadsNoPoolTagsKeepsNoRecord(t *testing.T) {
	// Arrange
	handler, h := startBoard(t, 8*gigabyte, &spawnRecorder{})
	handler.Service.Options.Comeback = &Comeback{SignedIn: func() (time.Time, error) { return time.Now(), nil }}

	// Act
	readFleet(t, handler.Service, time.Now().UTC(), 0)

	// Assert
	if _, err := os.Stat(filepath.Join(h.State, kernelPoolsFile)); !os.IsNotExist(err) {
		t.Errorf("the record of the pools = %v, want none", err)
	}
}
