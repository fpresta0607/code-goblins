package wake

import (
	"sync"
	"testing"
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
