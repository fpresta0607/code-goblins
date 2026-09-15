package wake

import (
	"sync"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestProducerCrashReplayIsIdempotentBeforeAndAfterAck(t *testing.T) {
	dir := t.TempDir()
	first, err := AppendOnce(dir, "stale", "task", "parked review", "episode-one")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		got, err := AppendOnce(dir, "stale", "task", "parked review", "episode-one")
		if err != nil || got.Seq != first.Seq {
			t.Fatalf("duplicate receipt %+v %v", got, err)
		}
		if err := AckThrough(dir, first.Seq); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := Pending(dir)
	if err != nil || len(pending) != 0 {
		t.Fatalf("acked replay wakes again: %+v %v", pending, err)
	}
	next, err := AppendOnce(dir, "stale", "task", "new unresolved episode", "episode-two")
	if err != nil || next.Seq != first.Seq+1 {
		t.Fatalf("next %+v %v", next, err)
	}
}

func TestNotificationReplayAcrossDurableBoundaries(t *testing.T) {
	detail := "blocked: preserve review custody"

	t.Run("status written", func(t *testing.T) {
		dir := t.TempDir()
		if err := state.AppendStatus(dir, "task", detail); err != nil {
			t.Fatal(err)
		}
		first, err := Notify(dir, "task", detail)
		if err != nil {
			t.Fatal(err)
		}
		again, err := Notify(dir, "task", detail)
		if err != nil || again.Seq != first.Seq {
			t.Fatalf("retry receipt = %+v, %v", again, err)
		}
		lines, err := state.TailStatus(dir, "task", 10)
		pending, pendingErr := Pending(dir)
		if err != nil || pendingErr != nil || len(lines) != 1 || len(pending) != 1 {
			t.Fatalf("status=%v pending=%+v errors=%v %v", lines, pending, err, pendingErr)
		}
	})

	t.Run("queue written", func(t *testing.T) {
		dir := t.TempDir()
		if err := state.AppendStatus(dir, "task", detail); err != nil {
			t.Fatal(err)
		}
		_, eventID, err := currentNotifyEvent(dir, "task", detail)
		if err != nil {
			t.Fatal(err)
		}
		queued, err := AppendOnce(dir, "notify", "task", detail, eventID)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Notify(dir, "task", detail)
		if err != nil || got.Seq != queued.Seq {
			t.Fatalf("retry receipt = %+v, %v", got, err)
		}
		if episode, err := ReadEpisode(dir); err != nil || !episode.Pending || episode.Gen != 1 {
			t.Fatalf("episode = %+v, %v", episode, err)
		}
	})

	t.Run("acknowledgement and state advance permit later events", func(t *testing.T) {
		dir := t.TempDir()
		first, err := Notify(dir, "task", detail)
		if err != nil {
			t.Fatal(err)
		}
		if err := AckThrough(dir, first.Seq); err != nil {
			t.Fatal(err)
		}
		second, err := Notify(dir, "task", detail)
		if err != nil || second.Seq != first.Seq+1 {
			t.Fatalf("post-ack event = %+v, %v", second, err)
		}
		if err := state.AppendStatus(dir, "task", "working: facts changed"); err != nil {
			t.Fatal(err)
		}
		third, err := Notify(dir, "task", detail)
		if err != nil || third.Seq != second.Seq+1 {
			t.Fatalf("post-advance event = %+v, %v", third, err)
		}
		pending, err := Pending(dir)
		if err != nil || len(pending) != 2 {
			t.Fatalf("pending = %+v, %v", pending, err)
		}
	})
}

func TestConcurrentSameProcessProducersDoNotLoseRecords(t *testing.T) {
	dir := t.TempDir()
	var workers sync.WaitGroup
	for i := 0; i < 5; i++ {
		workers.Go(func() {
			if _, err := Append(dir, "notify", "task", "independent decision"); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	pending, err := Pending(dir)
	if err != nil || len(pending) != 5 {
		t.Fatalf("lost writes: %+v %v", pending, err)
	}
}
