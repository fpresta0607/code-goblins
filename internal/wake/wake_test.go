package wake

import (
	"strings"
	"testing"
)

func TestAppendAssignsSequence(t *testing.T) {
	dir := t.TempDir()
	for want := 1; want <= 3; want++ {
		rec, err := Append(dir, "signal", "signal", "goblin g1 finished")
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
		if _, err := Append(dir, k, k, "detail of "+k); err != nil {
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
		if _, err := Append(dir, "signal", "signal", "d"); err != nil {
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
		if _, err := Append(dir, "signal", "signal", "d"); err != nil {
			t.Fatal(err)
		}
	}
	if err := AckThrough(dir, 3); err != nil {
		t.Fatal(err)
	}
	rec, err := Append(dir, "signal", "signal", "after ack")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Seq != 4 {
		t.Errorf("Seq = %d, want 4 (sequences are never reused)", rec.Seq)
	}
}

func TestAckThroughIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, err := Append(dir, "signal", "signal", "d"); err != nil {
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

func TestAppendRejectsUnknownKind(t *testing.T) {
	dir := t.TempDir()
	_, err := Append(dir, "bogus", "k", "d")
	if err == nil {
		t.Fatal("Append with unknown kind must error")
	}
	for _, kind := range []string{"signal", "stale", "check", "heartbeat"} {
		if !strings.Contains(err.Error(), kind) {
			t.Errorf("error %q does not name legal kind %q", err.Error(), kind)
		}
	}
}

func TestDedupedLastWriteWinsPerKindKey(t *testing.T) {
	dir := t.TempDir()
	if _, err := Append(dir, "signal", "a", "first a"); err != nil {
		t.Fatal(err)
	}
	if _, err := Append(dir, "signal", "b", "only b"); err != nil {
		t.Fatal(err)
	}
	if _, err := Append(dir, "signal", "a", "second a"); err != nil {
		t.Fatal(err)
	}
	records, err := Pending(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := Deduped(records)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Key != "a" || got[0].Detail != "second a" || got[0].Seq != 3 {
		t.Errorf("a bucket = %+v, want the later detail and seq", got[0])
	}
	if got[1].Key != "b" || got[1].Detail != "only b" {
		t.Errorf("b bucket = %+v, want untouched", got[1])
	}
}

func TestDedupedCollapsesHeartbeats(t *testing.T) {
	dir := t.TempDir()
	if _, err := Append(dir, "heartbeat", "h1", "heartbeat"); err != nil {
		t.Fatal(err)
	}
	if _, err := Append(dir, "heartbeat", "h2", "heartbeat"); err != nil {
		t.Fatal(err)
	}
	if _, err := Append(dir, "heartbeat", "h3", "heartbeat"); err != nil {
		t.Fatal(err)
	}
	records, err := Pending(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := Deduped(records)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Key != "h3" {
		t.Errorf("surviving heartbeat = %+v, want the latest (h3)", got[0])
	}
}

func TestDedupedKeepsBothBucketsAcrossCycles(t *testing.T) {
	dir := t.TempDir()
	if _, err := Append(dir, "signal", "a.status", "cycle one"); err != nil {
		t.Fatal(err)
	}
	if _, err := Append(dir, "signal", "b.status", "cycle one"); err != nil {
		t.Fatal(err)
	}
	if _, err := Append(dir, "signal", "a.status", "cycle two"); err != nil {
		t.Fatal(err)
	}
	records, err := Pending(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := Deduped(records)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Key != "a.status" || got[0].Detail != "cycle two" {
		t.Errorf("a.status bucket = %+v, want the later detail", got[0])
	}
	if got[1].Key != "b.status" || got[1].Detail != "cycle one" {
		t.Errorf("b.status bucket = %+v, want untouched", got[1])
	}
}
