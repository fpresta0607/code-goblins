package host

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// A viewer that stops reading is dropped rather than waited on, and the
// others keep receiving.
func TestHistoryDropsAViewerThatCannotKeepUp(t *testing.T) {
	output := newHistory()
	_, stalled, _ := output.attach()
	_, reading, _ := output.attach()
	received := 0
	for i := 0; i < 2000; i++ {
		output.write([]byte("x"))
		for len(reading) > 0 {
			<-reading
			received++
		}
	}

	drained := 0
	for range stalled {
		drained++
	}

	if drained >= 2000 {
		t.Errorf("the stalled viewer got all %d chunks, want it dropped", drained)
	}
	if received != 2000 {
		t.Errorf("the reading viewer got %d chunks, want all 2000", received)
	}
}

// Only the latest output up to the limit is replayed to late viewers, and
// the output kept for them is trimmed to its tail once it passes twice the
// limit.
func TestHistoryKeepsOnlyTheLatestOutput(t *testing.T) {
	output := newHistory()
	for _, fill := range []string{"a", "b", "c"} {
		output.write(bytes.Repeat([]byte(fill), historyLimit))
	}
	output.write([]byte("latest"))

	past, _, detach := output.attach()
	defer detach()

	if len(past) != historyLimit || past[0] != 'c' || !strings.HasSuffix(string(past), "latest") {
		t.Errorf("history is %d bytes from %q to %q, want the last %d, the third write's then the latest output", len(past), past[0], past[len(past)-6:], historyLimit)
	}
	if kept := len(output.kept); kept > 2*historyLimit {
		t.Errorf("the history keeps %d bytes, want at most %d", kept, 2*historyLimit)
	}
}

// A viewer that attaches after the terminal ended gets the history and a
// closed feed.
func TestHistoryAfterTheEndStillReplays(t *testing.T) {
	output := newHistory()
	output.write([]byte("last words"))
	output.end()

	past, feed, _ := output.attach()

	if string(past) != "last words" {
		t.Errorf("history = %q, want the last words", past)
	}
	if _, open := <-feed; open {
		t.Error("the feed of an ended terminal is still open")
	}
}

// A frame claiming more than the limit is refused before its payload is read.
func TestAFrameOverTheLimitIsRefused(t *testing.T) {
	var header [5]byte
	header[0] = frameOutput
	binary.BigEndian.PutUint32(header[1:], maxFrame+1)

	_, _, err := readFrame(bytes.NewReader(header[:]))

	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("readFrame error = %v, want the limit refusal", err)
	}
}
