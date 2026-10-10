package wake

import (
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Waiters queue for the wake lock, so whoever asks during an acknowledgement
// waits out its whole hold. With the ack floor, the queue and the episode
// each written under the lock, a notify behind an acknowledgement took 3.1 s
// at the median beside a cold build, where it took 0.4 s before the line.
// Under the lock an acknowledgement only renames: every file it leaves was
// staged before it took the lock, where making a new file, which the virus
// scanner reads, costs nobody in line a wait.
func TestAnAcknowledgementStagesNothingWhileItHoldsTheWakeLock(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	if _, err := Append(dir, "notify", "g0", "done: g0"); err != nil {
		t.Fatal(err)
	}
	done, err := Append(dir, "notify", "g1", "done: g1")
	if err != nil {
		t.Fatal(err)
	}
	generation, err := PublishEpisode(dir)
	if err != nil {
		t.Fatal(err)
	}
	stagedBeforeTheLock := int64(0)
	lockHeld = func() { stagedBeforeTheLock = fsx.Stages() }
	t.Cleanup(func() { lockHeld = func() {} })

	// Act
	acknowledged, err := Acknowledge(dir, Ack{Through: &done.Seq, Generation: &generation})
	stagedUnderTheLock := fsx.Stages() - stagedBeforeTheLock

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if stagedUnderTheLock != 0 {
		t.Errorf("the acknowledgement made %d new file(s) while it held the wake lock, want none", stagedUnderTheLock)
	}
	if len(acknowledged.Pending) != 0 || acknowledged.Episode.Pending {
		t.Errorf("the acknowledgement left %+v, want an empty queue and the episode acknowledged", acknowledged)
	}
	if isAcked, err := Acked(dir, done.Seq); err != nil || !isAcked {
		t.Errorf("sequence %d reads acknowledged = %t, %v, want the ack floor at it", done.Seq, isAcked, err)
	}
	if episode, err := ReadEpisode(dir); err != nil || episode.Pending || episode.Gen != generation {
		t.Errorf("the episode reads %+v, %v, want generation %d acknowledged", episode, err, generation)
	}
	if again, err := Append(dir, "notify", "g2", "done: g2"); err != nil || again.Seq != done.Seq+1 {
		t.Errorf("the next record is %+v, %v, want sequence %d", again, err, done.Seq+1)
	}
}

// A record queued after an acknowledgement staged its files, and before it
// took the lock, is not in the queue it staged. That queue is left unused,
// the queue is written from what is queued now, and the record stays.
func TestARecordQueuedAfterAnAcknowledgementStagedItsFilesIsKept(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	done, err := Append(dir, "notify", "g1", "done: g1")
	if err != nil {
		t.Fatal(err)
	}
	queued, err := readAll(dir)
	if err != nil {
		t.Fatal(err)
	}
	var staged stagedAck
	if err := staged.records(dir, queued, done.Seq); err != nil {
		t.Fatal(err)
	}
	late, err := Append(dir, "notify", "g2", "blocked: which way")
	if err != nil {
		t.Fatal(err)
	}

	// Act
	var kept []Record
	err = withLock(dir, func() error {
		records, err := readAll(dir)
		if err != nil {
			return err
		}
		kept, err = retireThrough(dir, records, done.Seq, map[string]bool{}, queued, &staged)
		return err
	})
	staged.discard()

	// Assert
	if err != nil || len(kept) != 1 || kept[0].Seq != late.Seq {
		t.Fatalf("the acknowledgement kept %+v, %v, want the one record queued after it staged", kept, err)
	}
	if pending, err := Pending(dir); err != nil || len(pending) != 1 || pending[0].Seq != late.Seq {
		t.Errorf("the queue holds %+v, %v, want the record queued after the acknowledgement staged", pending, err)
	}
	for _, name := range stateEntries(t, dir) {
		if strings.HasPrefix(name, ".cfo-tmp-") {
			t.Errorf("the acknowledgement left the staged file %s behind", name)
		}
	}
}
