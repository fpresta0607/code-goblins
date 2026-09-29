package host

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// A viewer that stops reading is dropped once it is further behind than a
// replay reaches, rather than waited on, and the others keep receiving.
func TestHistoryDropsAViewerThatCannotKeepUp(t *testing.T) {
	output := newHistory(80, 24)
	_, _, stalled, _ := output.attach()
	_, _, reading, _ := output.attach()
	chunk := []byte(strings.Repeat("x", 64<<10))
	received := 0
	for i := 0; i < 2*historyLimit/len(chunk); i++ {
		output.write(chunk)
		taken, _, _ := reading.next()
		received += len(taken)
	}

	output.mu.Lock()
	dropped, held := stalled.ended, len(stalled.pending)
	output.mu.Unlock()

	if !dropped || held > historyLimit {
		t.Errorf("the stalled viewer is dropped %v holding %d bytes, want it dropped holding at most %d", dropped, held, historyLimit)
	}
	if received != 2*historyLimit {
		t.Errorf("the reading viewer got %d bytes, want all %d", received, 2*historyLimit)
	}
}

// A viewer still reading through a burst of output is not dropped, however
// many writes the burst came in: ConPTY writes about one chunk per line.
func TestHistoryKeepsAViewerThatFallsBehindDuringABurst(t *testing.T) {
	output := newHistory(80, 24)
	_, _, viewer, _ := output.attach()
	line := []byte(strings.Repeat("s", 100) + "\r\n")
	for i := 0; i < 2000; i++ {
		output.write(line)
	}
	output.end()

	received := 0
	for taken, _, open := viewer.next(); open; taken, _, open = viewer.next() {
		received += len(taken)
	}

	if received != 2000*len(line) {
		t.Errorf("the viewer received %d of %d bytes, want the whole burst", received, 2000*len(line))
	}
}

// Only the latest output up to the limit is replayed to late viewers, and
// the output kept for them is trimmed to its tail once it passes twice the
// limit.
func TestHistoryKeepsOnlyTheLatestOutput(t *testing.T) {
	output := newHistory(80, 24)
	for _, fill := range []string{"a", "b", "c"} {
		output.write(bytes.Repeat([]byte(fill), historyLimit))
	}
	output.write([]byte("latest"))

	past, _, _, detach := output.attach()
	defer detach()

	if len(past) != historyLimit || past[0] != 'c' || !strings.HasSuffix(string(past), "latest") {
		t.Errorf("history is %d bytes from %q to %q, want the last %d, the third write's then the latest output", len(past), past[0], past[len(past)-6:], historyLimit)
	}
	if kept := len(output.kept); kept > 2*historyLimit {
		t.Errorf("the history keeps %d bytes, want at most %d", kept, 2*historyLimit)
	}
}

// A replay cut down to the limit starts at the next line or escape sequence
// rather than inside one, so a late viewer never begins on half a colour code
// or half a character; trimming the kept output cuts the same way.
func TestHistoryReplayStartsOnABoundary(t *testing.T) {
	colour := "\x1b[38;2;110;231;183m"
	for name, replay := range map[string]struct {
		head string
		// cut is where the limit falls inside head.
		cut  int
		want string
	}{
		"after a line":        {colour + "\r\n", 5, "bb"},
		"at an escape":        {colour + "é\x1b[K", 5, "\x1b[Kbb"},
		"at a character":      {"ééé", 3, "éb"},
		"on the limit itself": {"a\x1b[K", 1, "\x1b[Kbb"},
	} {
		t.Run(name, func(t *testing.T) {
			output := newHistory(80, 24)
			output.write([]byte(replay.head + strings.Repeat("b", historyLimit+replay.cut-len(replay.head))))

			past, _, _, detach := output.attach()
			detach()

			if !strings.HasPrefix(string(past), replay.want) || len(past) > historyLimit {
				t.Errorf("the replay starts %q and holds %d bytes, want it to start %q within the %d-byte limit", past[:min(len(past), 24)], len(past), replay.want, historyLimit)
			}
		})
	}
	t.Run("trimming", func(t *testing.T) {
		output := newHistory(80, 24)
		// The trim falls five bytes into the colour code.
		output.write([]byte(strings.Repeat("a", historyLimit) + colour + "\r\n"))
		output.write([]byte(strings.Repeat("c", historyLimit+5-len(colour)-2)))

		if kept := string(output.kept); !strings.HasPrefix(kept, "c") {
			t.Errorf("the kept output starts %q, want it trimmed after the line the colour code is on", kept[:min(len(kept), 24)])
		}
	})
}

// A resize is marked at its place in the output, for a late viewer's replay
// and for every live viewer, once it took; a resize that failed is not.
func TestHistoryMarksEachResizeAtItsPlace(t *testing.T) {
	output := newHistory(80, 24)
	_, _, live, detach := output.attach()
	defer detach()
	output.write([]byte("before"))
	if err := output.resize(100, 30, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := output.resize(1, 1, func() error { return errors.New("refused") }); err == nil {
		t.Fatal("a failed resize reported success")
	}
	output.write([]byte("after"))

	past, sizes, _, detachLate := output.attach()
	detachLate()
	taken, told, _ := live.next()

	if want := []geometry{{0, 80, 24}, {6, 100, 30}}; string(past) != "beforeafter" || !reflect.DeepEqual(sizes, want) {
		t.Errorf("the replay is %q at %v, want %q at %v", past, sizes, "beforeafter", want)
	}
	if want := []geometry{{6, 100, 30}}; string(taken) != "beforeafter" || !reflect.DeepEqual(told, want) {
		t.Errorf("the live viewer took %q with %v, want %q with %v", taken, told, "beforeafter", want)
	}
}

// Of resizes with no output between them only the last is kept, since
// nothing was drawn at the others, so resizing a window keeps no more sizes
// than the output it draws.
func TestHistoryKeepsTheLastOfResizesWithNoOutputBetween(t *testing.T) {
	output := newHistory(80, 24)
	_, _, live, detach := output.attach()
	defer detach()
	output.write([]byte("x"))
	for cols := 90; cols <= 120; cols += 10 {
		if err := output.resize(cols, 30, func() error { return nil }); err != nil {
			t.Fatal(err)
		}
	}

	_, sizes, _, detachLate := output.attach()
	detachLate()
	_, told, _ := live.next()

	if want := []geometry{{0, 80, 24}, {1, 120, 30}}; !reflect.DeepEqual(sizes, want) {
		t.Errorf("the replay's sizes are %v, want %v", sizes, want)
	}
	if want := []geometry{{1, 120, 30}}; !reflect.DeepEqual(told, want) {
		t.Errorf("the live viewer was told %v, want %v", told, want)
	}
}

// Output keeps flowing while the pseudo console applies a resize, which can
// need its output drained to finish: what the terminal writes meanwhile is
// kept, before the size, which is marked once the resize is known applied.
func TestHistoryKeepsOutputWrittenWhileAResizeApplies(t *testing.T) {
	output := newHistory(80, 24)
	_, _, live, detach := output.attach()
	defer detach()
	output.write([]byte("before"))

	err := output.resize(100, 30, func() error {
		// The host's output reader writes on a goroutine of its own.
		written := make(chan struct{})
		go func() {
			output.write([]byte("during"))
			close(written)
		}()
		select {
		case <-written:
			return nil
		case <-time.After(5 * time.Second):
			return errors.New("no output could be written while the resize applied")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	output.write([]byte("after"))

	past, sizes, _, detachLate := output.attach()
	detachLate()
	taken, told, _ := live.next()
	if want := []geometry{{0, 80, 24}, {12, 100, 30}}; string(past) != "beforeduringafter" || !reflect.DeepEqual(sizes, want) {
		t.Errorf("the replay is %q at %v, want %q at %v", past, sizes, "beforeduringafter", want)
	}
	if want := []geometry{{12, 100, 30}}; string(taken) != "beforeduringafter" || !reflect.DeepEqual(told, want) {
		t.Errorf("the live viewer took %q with %v, want %q with %v", taken, told, "beforeduringafter", want)
	}
}

// Output written while other goroutines resize the terminal is all kept, in
// order, and each size lands between two writes, never inside one, in the
// order the resizes were made, for a live viewer and a late replay alike.
func TestHistoryPlacesConcurrentResizesBetweenWrites(t *testing.T) {
	output := newHistory(80, 24)
	_, _, live, detach := output.attach()
	defer detach()
	var mu sync.Mutex
	output.write([]byte("<start>"))
	var written strings.Builder
	written.WriteString("<start>")
	boundaries := map[int]bool{written.Len(): true}
	var working sync.WaitGroup
	working.Add(2)
	go func() {
		defer working.Done()
		for i := range 2000 {
			chunk := fmt.Sprintf("<%d>", i)
			mu.Lock()
			output.write([]byte(chunk))
			written.WriteString(chunk)
			boundaries[written.Len()] = true
			mu.Unlock()
		}
	}()
	go func() {
		defer working.Done()
		for i := range 200 {
			_ = output.resize(20+i, 5+i%40, func() error {
				runtime.Gosched()
				return nil
			})
		}
	}()
	working.Wait()
	output.write([]byte("<end>"))
	written.WriteString("<end>")

	var taken strings.Builder
	var marks []geometry
	for !strings.HasSuffix(taken.String(), "<end>") {
		chunk, sizes, open := live.next()
		if !open {
			t.Fatal("the live viewer was dropped")
		}
		for _, size := range sizes {
			marks = append(marks, geometry{At: taken.Len() + size.At, Cols: size.Cols, Rows: size.Rows})
		}
		taken.Write(chunk)
	}

	if taken.String() != written.String() {
		t.Fatalf("the live viewer took %d bytes that differ from the %d written", taken.Len(), written.Len())
	}
	_, replayed, _, detachLate := output.attach()
	detachLate()
	if replayed[0] != (geometry{At: 0, Cols: 80, Rows: 24}) {
		t.Fatalf("the replay starts at size %v, want the initial 80x24 at byte 0", replayed[0])
	}
	for name, sizes := range map[string][]geometry{"live": marks, "replayed": replayed[1:]} {
		if len(sizes) == 0 || sizes[len(sizes)-1].Cols != 219 {
			t.Errorf("%s sizes end %v, want the last resize, 219 columns", name, sizes[max(0, len(sizes)-1):])
		}
		for i, size := range sizes {
			if !boundaries[size.At] {
				t.Errorf("%s size %v falls inside a write", name, size)
			}
			if i > 0 && (size.Cols <= sizes[i-1].Cols || size.At < sizes[i-1].At) {
				t.Errorf("%s size %v comes after %v, out of the order the resizes were made", name, size, sizes[i-1])
			}
		}
	}
}

// Trimming the kept output keeps the size its new start was written at, and
// moves the later sizes with the output.
func TestHistoryTrimKeepsTheSizeItsStartWasWrittenAt(t *testing.T) {
	output := newHistory(80, 24)
	output.write(bytes.Repeat([]byte("a"), historyLimit))
	_ = output.resize(100, 30, func() error { return nil })
	output.write(bytes.Repeat([]byte("b"), historyLimit))
	_ = output.resize(120, 40, func() error { return nil })
	// The trim falls ten bytes into the b's, which have no line or escape
	// sequence to start on.
	output.write(bytes.Repeat([]byte("c"), 10))

	past, sizes, _, detach := output.attach()
	detach()

	if want := []geometry{{0, 100, 30}, {historyLimit - 10, 120, 40}}; past[0] != 'b' || !reflect.DeepEqual(sizes, want) {
		t.Errorf("the replay starts %q at %v, want the b's at %v", past[0], sizes, want)
	}
}

// A viewer that attaches after the terminal ended gets the history and an
// ended feed.
func TestHistoryAfterTheEndStillReplays(t *testing.T) {
	output := newHistory(80, 24)
	output.write([]byte("last words"))
	output.end()

	past, _, feed, _ := output.attach()

	if string(past) != "last words" {
		t.Errorf("history = %q, want the last words", past)
	}
	if _, _, open := feed.next(); open {
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
