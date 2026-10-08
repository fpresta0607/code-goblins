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

	"github.com/fpresta0607/code-goblins/internal/vtscreen"
)

// A viewer that stops reading is dropped once it is further behind than a
// replay reaches, rather than waited on, and the others keep receiving.
func TestHistoryDropsAViewerThatCannotKeepUp(t *testing.T) {
	output := newTestHistory(t, 80, 24)
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
	output := newTestHistory(t, 80, 24)
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

// Only the latest output up to the limit is replayed to late viewers, then
// the screen's repaint, since the replay no longer starts at the terminal's
// start, and the output kept for them is trimmed to its tail once it passes
// twice the limit.
func TestHistoryKeepsOnlyTheLatestOutput(t *testing.T) {
	output := newTestHistory(t, 80, 24)
	for _, fill := range []string{"a", "b", "c"} {
		output.write(bytes.Repeat([]byte(fill), historyLimit))
	}
	output.write([]byte("latest"))

	past, _, _, detach := output.attach()
	defer detach()

	replay, isRepainted := bytes.CutSuffix(past, output.screen.Repaint())
	if len(replay) != historyLimit || replay[0] != 'c' || !bytes.HasSuffix(replay, []byte("latest")) || !isRepainted {
		t.Errorf("history is %d bytes from %q, repainted %v, want the last %d, the third write's then the latest output, then the repaint", len(replay), replay[0], isRepainted, historyLimit)
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
			output := newTestHistory(t, 80, 24)
			output.write([]byte(replay.head + strings.Repeat("b", historyLimit+replay.cut-len(replay.head))))

			past, _, _, detach := output.attach()
			detach()

			past, _ = bytes.CutSuffix(past, output.screen.Repaint())
			if !strings.HasPrefix(string(past), replay.want) || len(past) > historyLimit {
				t.Errorf("the replay starts %q and holds %d bytes, want it to start %q within the %d-byte limit", past[:min(len(past), 24)], len(past), replay.want, historyLimit)
			}
		})
	}
	t.Run("trimming", func(t *testing.T) {
		output := newTestHistory(t, 80, 24)
		// The trim falls five bytes into the colour code.
		output.write([]byte(strings.Repeat("a", historyLimit) + colour + "\r\n"))
		output.write([]byte(strings.Repeat("c", historyLimit+5-len(colour)-2)))

		if kept := string(output.kept); !strings.HasPrefix(kept, "c") {
			t.Errorf("the kept output starts %q, want it trimmed after the line the colour code is on", kept[:min(len(kept), 24)])
		}
	})
}

// newTestHistory is a history of a terminal of cols by rows cells.
func newTestHistory(t *testing.T, cols, rows int) *history {
	t.Helper()
	output, err := newHistory(cols, rows)
	if err != nil {
		t.Fatal(err)
	}
	return output
}

// repaintAfter is the repaint a terminal of cols by rows cells passes on
// when it shows output and is then resized to resized.
func repaintAfter(t *testing.T, cols, rows int, output string, resized [2]int) string {
	t.Helper()
	screen, err := vtscreen.New(cols, rows)
	if err != nil {
		t.Fatal(err)
	}
	screen.Write([]byte(output))
	repaint, err := screen.Resize(resized[0], resized[1])
	if err != nil {
		t.Fatal(err)
	}
	return string(repaint)
}

// A resize is marked at its place in the output, for a late viewer's replay
// and for every live viewer, with the screen's repaint after it, and a size the
// terminal cannot take is not.
func TestHistoryMarksEachResizeAtItsPlace(t *testing.T) {
	output := newTestHistory(t, 80, 24)
	_, _, live, detach := output.attach()
	defer detach()
	output.write([]byte("before"))
	if err := output.resize(100, 30, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := output.resize(1, 1, func() error { return nil }); err == nil {
		t.Fatal("a size no terminal can take reported success")
	}
	output.write([]byte("after"))

	past, sizes, _, detachLate := output.attach()
	detachLate()
	taken, told, _ := live.next()

	want := "before" + repaintAfter(t, 80, 24, "before", [2]int{100, 30}) + "after"
	if wantSizes := []geometry{{0, 80, 24}, {6, 100, 30}}; string(past) != want || !reflect.DeepEqual(sizes, wantSizes) {
		t.Errorf("the replay is %q at %v, want %q at %v", past, sizes, want, wantSizes)
	}
	if wantSizes := []geometry{{6, 100, 30}}; string(taken) != want || !reflect.DeepEqual(told, wantSizes) {
		t.Errorf("the live viewer took %q with %v, want %q with %v", taken, told, want, wantSizes)
	}
}

// A size the pseudo console refuses, as one that has closed does, leaves
// the terminal at the size before it, for the history and its screen alike.
func TestHistoryPutsBackTheSizeBeforeOneThePseudoConsoleRefused(t *testing.T) {
	output := newTestHistory(t, 80, 24)
	output.write([]byte("before"))

	err := output.resize(100, 30, func() error { return errors.New("refused") })
	output.write([]byte("\x1b[99;99H"))
	_, sizes, _, detach := output.attach()
	detach()
	answer := output.write([]byte("\x1b[6n"))

	if err == nil {
		t.Fatal("a refused resize reported success")
	}
	if last := sizes[len(sizes)-1]; last.Cols != 80 || last.Rows != 24 {
		t.Errorf("the replay's sizes are %v, want them to end at 80x24", sizes)
	}
	if string(answer) != "\x1b[24;80R" {
		t.Errorf("the screen answers %q, want the cursor at the corner of 80x24", answer)
	}
}

// A resize to the size the terminal has repaints its screen too, as the
// system conhost does, since a viewer sends its size as it connects and
// counts on the repaint to show the screen whole.
func TestHistoryRepaintsOnAResizeToTheSameSize(t *testing.T) {
	output := newTestHistory(t, 80, 24)
	_, _, live, detach := output.attach()
	defer detach()
	output.write([]byte("x"))

	if err := output.resize(80, 24, func() error { return nil }); err != nil {
		t.Fatal(err)
	}

	past, sizes, _, detachLate := output.attach()
	detachLate()
	taken, told, _ := live.next()
	want := "x" + repaintAfter(t, 80, 24, "x", [2]int{80, 24})
	if wantSizes := []geometry{{0, 80, 24}, {1, 80, 24}}; string(past) != want || !reflect.DeepEqual(sizes, wantSizes) {
		t.Errorf("the replay is %q at %v, want %q at %v", past, sizes, want, wantSizes)
	}
	if wantSizes := []geometry{{1, 80, 24}}; string(taken) != want || !reflect.DeepEqual(told, wantSizes) {
		t.Errorf("the live viewer took %q with %v, want %q with %v", taken, told, want, wantSizes)
	}
}

// Output keeps flowing while the pseudo console applies a resize, which can
// need its output drained to finish. The size and the screen's repaint come
// first, since the screen takes the size before the pseudo console does, so
// a query the program asks once the pseudo console has resized is answered
// at the new size, and what the terminal writes meanwhile is kept after them.
func TestHistoryKeepsOutputWrittenWhileAResizeApplies(t *testing.T) {
	output := newTestHistory(t, 80, 24)
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
	want := "before" + repaintAfter(t, 80, 24, "before", [2]int{100, 30}) + "duringafter"
	if wantSizes := []geometry{{0, 80, 24}, {6, 100, 30}}; string(past) != want || !reflect.DeepEqual(sizes, wantSizes) {
		t.Errorf("the replay is %q at %v, want %q at %v", past, sizes, want, wantSizes)
	}
	if wantSizes := []geometry{{6, 100, 30}}; string(taken) != want || !reflect.DeepEqual(told, wantSizes) {
		t.Errorf("the live viewer took %q with %v, want %q with %v", taken, told, want, wantSizes)
	}
}

// Output written while other goroutines resize the terminal is all kept, in
// order, and each size lands between two writes, never inside one, in the
// order the resizes were made, for a live viewer and a late replay alike.
func TestHistoryPlacesConcurrentResizesBetweenWrites(t *testing.T) {
	output := newTestHistory(t, 80, 24)
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

	var taken []byte
	var marks []geometry
	for !bytes.HasSuffix(taken, []byte("<end>")) {
		chunk, sizes, open := live.next()
		if !open {
			t.Fatal("the live viewer was dropped")
		}
		for _, size := range sizes {
			marks = append(marks, geometry{At: len(taken) + size.At, Cols: size.Cols, Rows: size.Rows})
		}
		taken = append(taken, chunk...)
	}

	program, marks := withoutRepaints(taken, marks)
	if program != written.String() {
		t.Fatalf("the live viewer took %d bytes of the program's that differ from the %d written", len(program), written.Len())
	}
	past, replayed, _, detachLate := output.attach()
	detachLate()
	if replayed[0] != (geometry{At: 0, Cols: 80, Rows: 24}) {
		t.Fatalf("the replay starts at size %v, want the initial 80x24 at byte 0", replayed[0])
	}
	_, replayed = withoutRepaints(past, replayed)
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

// repaintStart and repaintEnd open and close every repaint of a screen
// whose program is not in the middle of synchronized output.
var repaintStart, repaintEnd = []byte("\x1b[?2026h"), []byte("\x1b[?2026l")

// withoutRepaints is output with the screen's repaints taken out, and sizes
// moved with the output that is left.
func withoutRepaints(output []byte, sizes []geometry) (string, []geometry) {
	var left []byte
	moved := make([]int, len(output)+1)
	for i := 0; i < len(output); {
		if bytes.HasPrefix(output[i:], repaintStart) {
			end := i + bytes.Index(output[i:], repaintEnd) + len(repaintEnd)
			for ; i < end; i++ {
				moved[i] = len(left)
			}
			continue
		}
		moved[i] = len(left)
		left = append(left, output[i])
		i++
	}
	moved[len(output)] = len(left)
	movedSizes := make([]geometry, len(sizes))
	for i, size := range sizes {
		movedSizes[i] = geometry{At: moved[size.At], Cols: size.Cols, Rows: size.Rows}
	}
	return string(left), movedSizes
}

// Trimming the kept output keeps the size its new start was written at, and
// moves the later sizes with the output, and the replay of output that lost its
// start ends with the screen's repaint.
func TestHistoryTrimKeepsTheSizeItsStartWasWrittenAt(t *testing.T) {
	output := newTestHistory(t, 80, 24)
	output.write(bytes.Repeat([]byte("a"), historyLimit))
	_ = output.resize(100, 30, func() error { return nil })
	output.write(bytes.Repeat([]byte("b"), historyLimit))
	_ = output.resize(120, 40, func() error { return nil })
	output.write(bytes.Repeat([]byte("c"), 10))

	past, sizes, _, detach := output.attach()
	detach()

	second := bytes.Index(past, repaintStart)
	if want := []geometry{{0, 100, 30}, {second, 120, 40}}; past[0] != 'b' || !reflect.DeepEqual(sizes, want) {
		t.Errorf("the replay starts %q at %v, want the b's at %v", past[0], sizes, want)
	}
	if !bytes.HasSuffix(past, output.screen.Repaint()) {
		t.Error("the replay does not end with the screen's repaint")
	}
}

// A viewer that attaches after the terminal ended gets the history and an
// ended feed.
func TestHistoryAfterTheEndStillReplays(t *testing.T) {
	output := newTestHistory(t, 80, 24)
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
