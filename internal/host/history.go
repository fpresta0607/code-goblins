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

// geometry is the terminal's size in cells from byte At of an output on.
// Output is drawn for the grid it was written at, so a viewer replaying or
// following it takes each size at its place.
type geometry struct {
	At, Cols, Rows int
}

// sizesFrom rebases sizes on output cut at cut: the size in effect there
// starts it, and the later ones move with the output.
func sizesFrom(sizes []geometry, cut int) []geometry {
	first := 0
	for i, size := range sizes {
		if size.At <= cut {
			first = i
		}
	}
	rebased := make([]geometry, 0, len(sizes)-first)
	for _, size := range sizes[first:] {
		rebased = append(rebased, geometry{At: max(0, size.At-cut), Cols: size.Cols, Rows: size.Rows})
	}
	return rebased
}

// withSize adds size to sizes at its place, in place of a size at the same
// place, since no output was drawn at that one.
func withSize(sizes []geometry, size geometry) []geometry {
	if last := len(sizes) - 1; last >= 0 && sizes[last].At == size.At {
		sizes[last] = size
		return sizes
	}
	return append(sizes, size)
}

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

// history is the terminal's recent output, the sizes it was written at, and
// whoever is watching it live.
type history struct {
	mu   sync.Mutex
	kept []byte
	// sizes are kept's sizes in order; the first, at 0, is the size kept
	// starts at.
	sizes   []geometry
	viewers map[*feed]struct{}
	closed  bool
	// sizing keeps resizes in the order they reach the terminal. It is apart
	// from mu so output keeps flowing while the pseudo console resizes.
	sizing sync.Mutex
}

// feed is one live viewer's share of the output: what the terminal wrote
// that the viewer has not taken yet, and whether that is all it will get.
type feed struct {
	output *history
	// ready holds a signal while output or the feed's end waits to be taken.
	ready   chan struct{}
	pending []byte
	// sizes are the resizes among pending, at their places in it.
	sizes []geometry
	ended bool
}

// newHistory starts the history of a terminal of cols by rows cells.
func newHistory(cols, rows int) *history {
	return &history{sizes: []geometry{{Cols: cols, Rows: rows}}, viewers: map[*feed]struct{}{}}
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
		cut := replayStart(h.kept, len(h.kept)-historyLimit)
		h.kept = append(h.kept[:0], h.kept[cut:]...)
		h.sizes = sizesFrom(h.sizes, cut)
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

// resize applies a resize with apply and, once it took, marks the new size
// at its place in the output, for the history and every viewer.
func (h *history) resize(cols, rows int, apply func() error) error {
	h.sizing.Lock()
	defer h.sizing.Unlock()
	if err := apply(); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sizes = withSize(h.sizes, geometry{At: len(h.kept), Cols: cols, Rows: rows})
	for viewer := range h.viewers {
		viewer.sizes = withSize(viewer.sizes, geometry{At: len(viewer.pending), Cols: cols, Rows: rows})
		viewer.wake()
	}
	return nil
}

// attach returns the output so far with the sizes it was written at, and a
// feed that starts exactly where it ends, which ends when the terminal ends
// or detach is called.
func (h *history) attach() ([]byte, []geometry, *feed, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	viewer := &feed{output: h, ready: make(chan struct{}, 1)}
	start := replayStart(h.kept, len(h.kept)-historyLimit)
	past, sizes := append([]byte(nil), h.kept[start:]...), sizesFrom(h.sizes, start)
	if h.closed {
		h.stop(viewer)
		return past, sizes, viewer, func() {}
	}
	h.viewers[viewer] = struct{}{}
	return past, sizes, viewer, func() {
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

// next waits for output or a resize the viewer has not taken and returns all
// of it at once, the sizes at their places in the output, or false once the
// feed has ended and nothing is left.
func (f *feed) next() ([]byte, []geometry, bool) {
	for {
		f.output.mu.Lock()
		output, sizes, ended := f.pending, f.sizes, f.ended
		f.pending, f.sizes = nil, nil
		f.output.mu.Unlock()
		if len(output) > 0 || len(sizes) > 0 {
			return output, sizes, true
		}
		if ended {
			return nil, nil, false
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
