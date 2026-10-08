package host

import (
	"bytes"
	"errors"
	"sync"
	"unicode/utf8"

	"github.com/fpresta0607/code-goblins/internal/vtscreen"
)

// historyLimit bounds the terminal output a host replays to a late viewer.
// The kept output is trimmed back to it in batches, once it doubles, so a
// write does not copy the whole history. A replay that no longer starts at
// the terminal's first output ends with a repaint of the whole screen, so a
// late viewer shows the screen whole however much of it the replay drew.
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
// whoever is watching it live. Its screen is the terminal of record: what
// the program writes goes through it, which answers the program's queries
// and keeps them from viewers, so viewers are passed and replay the output
// without them.
type history struct {
	mu   sync.Mutex
	kept []byte
	// isTrimmed is whether kept has lost the terminal's first output.
	isTrimmed bool
	screen    *vtscreen.Screen
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
func newHistory(cols, rows int) (*history, error) {
	screen, err := vtscreen.New(cols, rows)
	if err != nil {
		return nil, err
	}
	return &history{screen: screen, sizes: []geometry{{Cols: cols, Rows: rows}}, viewers: map[*feed]struct{}{}}, nil
}

// write takes output the terminal's program wrote, keeps and queues for
// every viewer what its screen passes on, and returns what the screen
// answers the program.
func (h *history) write(chunk []byte) (answers []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	forward, answers := h.screen.Write(chunk)
	h.pass(forward)
	return answers
}

// pass keeps output and queues it for every viewer. A viewer is dropped,
// never waited on, once it is further behind than a replay reaches, so the
// terminal never stalls behind a slow window and a viewer holds no more than
// the history does. The bound is in bytes, not writes: ConPTY writes about
// one chunk per line, so an ordinary burst of a few thousand lines is
// thousands of writes while a viewer is still reading through it. The
// caller holds h.mu.
func (h *history) pass(output []byte) {
	if len(output) == 0 {
		return
	}
	h.kept = append(h.kept, output...)
	if len(h.kept) > 2*historyLimit {
		cut := replayStart(h.kept, len(h.kept)-historyLimit)
		h.kept = append(h.kept[:0], h.kept[cut:]...)
		h.sizes = sizesFrom(h.sizes, cut)
		h.isTrimmed = true
	}
	for viewer := range h.viewers {
		if len(viewer.pending)+len(output) > historyLimit {
			h.stop(viewer)
			continue
		}
		viewer.pending = append(viewer.pending, output...)
		viewer.wake()
	}
}

// resize gives the terminal a new size: its screen first, so a query the
// program asks once the pseudo console has resized is answered at the new
// size, with the size marked at its place in the output for the history and
// every viewer and the screen's repaint after it, then the pseudo console,
// with apply. A size apply refuses puts the size before it back.
func (h *history) resize(cols, rows int, apply func() error) error {
	h.sizing.Lock()
	defer h.sizing.Unlock()
	h.mu.Lock()
	previous := h.sizes[len(h.sizes)-1]
	err := h.take(cols, rows)
	h.mu.Unlock()
	if err != nil {
		return err
	}
	if err := apply(); err != nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		return errors.Join(err, h.take(previous.Cols, previous.Rows))
	}
	return nil
}

// take resizes the screen, marks the size at its place in the output for
// the history and every viewer, and passes on the screen's repaint, since a
// pseudo console does not repaint on a resize while each viewer resizes its
// own copy of the screen its own way. The caller holds h.mu.
func (h *history) take(cols, rows int) error {
	repaint, err := h.screen.Resize(cols, rows)
	if err != nil {
		return err
	}
	h.sizes = withSize(h.sizes, geometry{At: len(h.kept), Cols: cols, Rows: rows})
	for viewer := range h.viewers {
		viewer.sizes = withSize(viewer.sizes, geometry{At: len(viewer.pending), Cols: cols, Rows: rows})
		viewer.wake()
	}
	h.pass(repaint)
	return nil
}

// attach returns the output so far with the sizes it was written at, and a
// feed that starts exactly where it ends, which ends when the terminal ends
// or detach is called. Output that no longer starts at the terminal's first
// ends with the screen's repaint.
func (h *history) attach() ([]byte, []geometry, *feed, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	viewer := &feed{output: h, ready: make(chan struct{}, 1)}
	start := replayStart(h.kept, len(h.kept)-historyLimit)
	past, sizes := append([]byte(nil), h.kept[start:]...), sizesFrom(h.sizes, start)
	if start > 0 || h.isTrimmed {
		past = append(past, h.screen.Repaint()...)
	}
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
