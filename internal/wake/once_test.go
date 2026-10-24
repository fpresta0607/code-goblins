package wake

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestFirstNoticePublishesOnceAcrossConcurrentRetriesAndAcknowledgement(t *testing.T) {
	directory := t.TempDir()
	for round := 0; round < 2; round++ {
		var group sync.WaitGroup
		for range 16 {
			group.Go(func() {
				if err := AppendFirstAndPublish(directory, "call-1", "review", "task", "key kind"); err != nil {
					t.Error(err)
				}
			})
		}
		group.Wait()

		episode, err := ReadEpisode(directory)
		if err != nil || episode.Gen != 1 || episode.Pending != (round == 0) {
			t.Fatalf("episode = %+v, %v; want one publication", episode, err)
		}
		pending, err := Pending(directory)
		if err != nil || len(pending) != 1-round {
			t.Fatal("notice was duplicated or resurrected")
		}
		if round == 0 {
			if _, err := Acknowledge(directory, Ack{Through: &pending[0].Seq, Generation: &episode.Gen}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestFirstNoticeRepairsFailedPublicationOnce(t *testing.T) {
	directory := t.TempDir()
	marker := filepath.Join(directory, episodeFile)
	if err := os.Mkdir(marker, 0700); err != nil {
		t.Fatal(err)
	}
	if err := AppendFirstAndPublish(directory, "call-1", "review", "task", "key kind"); err == nil {
		t.Fatal("publication failure was hidden")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			if err := AppendFirstAndPublish(directory, "call-1", "review", "task", "key kind"); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()

	episode, err := ReadEpisode(directory)
	if err != nil || !episode.Pending || episode.Gen != 1 {
		t.Fatalf("episode = %+v, %v; want one recovered publication", episode, err)
	}
	if pending, err := Pending(directory); err != nil || len(pending) != 1 {
		t.Fatal("retry changed the notice count")
	}
}

func TestLifecycleCompletionNoticeIsDeliveredOnceAcrossAcknowledgement(t *testing.T) {
	directory := t.TempDir()
	first, err := AppendOnce(directory, "task:pause-1", "notify", "task", "paused: work kept")
	if err != nil {
		t.Fatal(err)
	}
	again, err := AppendOnce(directory, "task:pause-1", "notify", "task", "paused: work kept")
	if err != nil || again.Seq != first.Seq {
		t.Fatalf("retry appended another notice: %+v %v", again, err)
	}
	if err := AckThrough(directory, first.Seq); err != nil {
		t.Fatal(err)
	}
	again, err = AppendOnce(directory, "task:pause-1", "notify", "task", "paused: work kept")
	if err != nil || again.Seq != first.Seq {
		t.Fatalf("acked retry changed identity: %+v %v", again, err)
	}
	pending, err := Pending(directory)
	if err != nil || len(pending) != 0 {
		t.Fatalf("acked notice resurfaced: %+v %v", pending, err)
	}
	if _, err := AppendOnce(directory, "task:pause-1", "notify", "task", "different operation"); err == nil {
		t.Fatal("identity was reused with a different notice")
	}
}

// A condition that can come back, such as a file a goblin reinstalls after
// the CFO removed it, wakes him once while that wake is queued, however
// many callers report it at once, and again once he has acknowledged it.
func TestAppendUnlessQueuedWakesAgainOnlyOnceAcknowledged(t *testing.T) {
	// Arrange
	directory := t.TempDir()
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			if _, _, err := AppendUnlessQueued(directory, "start-hook task x.pth", "review", "task", "x.pth runs at every start"); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()

	// Act
	queued, err := Pending(directory)
	if err != nil || len(queued) != 1 {
		t.Fatalf("queued = %+v, %v, want one wake from callers reporting at once", queued, err)
	}
	if err := AckThrough(directory, queued[0].Seq); err != nil {
		t.Fatal(err)
	}
	again, isNew, err := AppendUnlessQueued(directory, "start-hook task x.pth", "review", "task", "x.pth runs at every start")

	// Assert
	if err != nil || !isNew || again.Seq <= queued[0].Seq {
		t.Fatalf("after acknowledgement = %+v, new %t, %v, want a new wake", again, isNew, err)
	}
}
