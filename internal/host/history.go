package host

import (
	"bytes"
	"sync"
	"unicode/utf8"
)

// historyLimit bounds the terminal output a host replays to a late viewer.
// The kept output is trimmed back to it in batches, once it doubles, so a
// write does not copy the whole history. A viewer repaints its screen with a
// resize once the replay ends, since the pseudo console redraws its whole
// window on every resize, so the replay only has to supply the scrollback.
const historyLimit = 4 << 20

// boundaryReach is how far past a cut the replay looks for a line or an
// escape sequence to start on.
const boundaryReach = 64 << 10

// replayStart is where output cut at cut starts again: the next line or
// escape sequence within reach, so a replay never begins inside a colour code
// or a cursor move, and otherwise the next whole character.
func replayStart(output []byte, cut int) int {
	if cut <= 0 {
		return 0
	}
	if i := bytes.IndexAny(output[cut:min(len(output), cut+boundaryReach)], "\n\x1b"); i >= 0 {
		if output[cut+i] == '\n' {
			return cut + i + 1
		}
		return cut + i
	}
	for cut < len(output) && !utf8.RuneStart(output[cut]) {
		cut++
	}
	return cut
}

// history is the terminal's recent output and whoever is watching it live.
type history struct {
	mu      sync.Mutex
	kept    []byte
	viewers map[*feed]struct{}
	closed  bool
}

// feed is one live viewer's share of the output: what the terminal wrote
// that the viewer has not taken yet, and whether that is all it will get.
type feed struct {
	output *history
	// ready holds a signal while output or the feed's end waits to be taken.
	ready   chan struct{}
	pending []byte
	ended   bool
}

func newHistory() *history {
	return &history{viewers: map[*feed]struct{}{}}
}

// write keeps chunk and queues it for every viewer. A viewer is dropped,
// never waited on, once it is further behind than a replay reaches, so the
// terminal never stalls behind a slow window and a viewer holds no more than
// the history does. The bound is in bytes, not writes: ConPTY writes about
// one chunk per line, so an ordinary burst of a few thousand lines is
// thousands of writes while a viewer is still reading through it.
func (h *history) write(chunk []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.kept = append(h.kept, chunk...)
	if len(h.kept) > 2*historyLimit {
		h.kept = append(h.kept[:0], h.kept[replayStart(h.kept, len(h.kept)-historyLimit):]...)
	}
	for viewer := range h.viewers {
		if len(viewer.pending)+len(chunk) > historyLimit {
			h.stop(viewer)
			continue
		}
		viewer.pending = append(viewer.pending, chunk...)
		viewer.wake()
	}
}

// attach returns the output so far and a feed that starts exactly where it
// ends, which ends when the terminal ends or detach is called.
func (h *history) attach() ([]byte, *feed, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	viewer := &feed{output: h, ready: make(chan struct{}, 1)}
	past := append([]byte(nil), h.kept[replayStart(h.kept, len(h.kept)-historyLimit):]...)
	if h.closed {
		h.stop(viewer)
		return past, viewer, func() {}
	}
	h.viewers[viewer] = struct{}{}
	return past, viewer, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.stop(viewer)
	}
}

// stop ends viewer's feed once it has taken what is already queued. The
// caller holds h.mu.
func (h *history) stop(viewer *feed) {
	delete(h.viewers, viewer)
	viewer.ended = true
	viewer.wake()
}

// end ends every viewer's feed once the terminal has no more output.
func (h *history) end() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for viewer := range h.viewers {
		h.stop(viewer)
	}
}

// wake tells the viewer's reader there is output or an end to take.
func (f *feed) wake() {
	select {
	case f.ready <- struct{}{}:
	default:
	}
}

// next waits for output the viewer has not taken and returns all of it at
// once, or false once the feed has ended and nothing is left.
func (f *feed) next() ([]byte, bool) {
	for {
		f.output.mu.Lock()
		output, ended := f.pending, f.ended
		f.pending = nil
		f.output.mu.Unlock()
		if len(output) > 0 {
			return output, true
		}
		if ended {
			return nil, false
		}
		<-f.ready
	}
}

// ended reports whether the terminal's output has ended.
func (h *history) ended() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed
}
