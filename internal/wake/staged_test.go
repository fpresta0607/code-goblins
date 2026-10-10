package wake

import (
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
