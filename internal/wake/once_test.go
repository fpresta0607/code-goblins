package wake

import "testing"

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
