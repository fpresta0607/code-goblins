package wake

import (
	"testing"
)

func TestAppendAssignsSequence(t *testing.T) {
	dir := t.TempDir()
	for want := 1; want <= 3; want++ {
		rec, err := Append(dir, "signal", "goblin g1 finished")
		if err != nil {
			t.Fatalf("Append %d: %v", want, err)
		}
		if rec.Seq != want {
			t.Errorf("Seq = %d, want %d", rec.Seq, want)
		}
	}
}

func TestPendingReturnsAllInOrder(t *testing.T) {
	dir := t.TempDir()
	kinds := []string{"signal", "stale", "check"}
	for _, k := range kinds {
		if _, err := Append(dir, k, "detail of "+k); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Pending(dir)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for i, k := range kinds {
		if got[i].Kind != k || got[i].Seq != i+1 {
			t.Errorf("record %d = %+v, want kind %s seq %d", i, got[i], k, i+1)
		}
	}
}

func TestAckThroughDropsHandledKeepsRest(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		if _, err := Append(dir, "signal", "d"); err != nil {
			t.Fatal(err)
		}
	}
	if err := AckThrough(dir, 2); err != nil {
		t.Fatalf("AckThrough: %v", err)
	}
	got, err := Pending(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Seq != 3 {
		t.Errorf("pending = %+v, want only seq 3", got)
	}
}

func TestSequenceNeverReusedAfterAck(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		if _, err := Append(dir, "signal", "d"); err != nil {
			t.Fatal(err)
		}
	}
	if err := AckThrough(dir, 3); err != nil {
		t.Fatal(err)
	}
	rec, err := Append(dir, "signal", "after ack")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Seq != 4 {
		t.Errorf("Seq = %d, want 4 (sequences are never reused)", rec.Seq)
	}
}

func TestAckThroughIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, err := Append(dir, "signal", "d"); err != nil {
		t.Fatal(err)
	}
	if err := AckThrough(dir, 1); err != nil {
		t.Fatal(err)
	}
	if err := AckThrough(dir, 1); err != nil {
		t.Fatalf("second identical ack must succeed, got %v", err)
	}
	got, err := Pending(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("pending = %+v, want empty", got)
	}
}

func TestPendingEmptyWhenNoQueueFile(t *testing.T) {
	got, err := Pending(t.TempDir())
	if err != nil {
		t.Fatalf("missing queue must mean empty, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("pending = %+v, want empty", got)
	}
}
