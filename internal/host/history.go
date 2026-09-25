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
	viewers map[chan []byte]struct{}
	closed  bool
}

func newHistory() *history {
	return &history{viewers: map[chan []byte]struct{}{}}
}

// write keeps chunk and hands it to every viewer; the caller never changes
// it afterwards. A viewer that cannot keep up is dropped, never waited on, so
// the terminal never stalls behind a slow window.
func (h *history) write(chunk []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.kept = append(h.kept, chunk...)
	if len(h.kept) > 2*historyLimit {
		h.kept = append(h.kept[:0], h.kept[replayStart(h.kept, len(h.kept)-historyLimit):]...)
	}
	for viewer := range h.viewers {
		select {
		case viewer <- chunk:
		default:
			delete(h.viewers, viewer)
			close(viewer)
		}
	}
}

// attach returns the output so far and a feed that starts exactly where it
// ends, closed when the terminal ends or detach is called.
func (h *history) attach() ([]byte, <-chan []byte, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	feed := make(chan []byte, 1024)
	past := append([]byte(nil), h.kept[replayStart(h.kept, len(h.kept)-historyLimit):]...)
	if h.closed {
		close(feed)
		return past, feed, func() {}
	}
	h.viewers[feed] = struct{}{}
	return past, feed, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.viewers[feed]; ok {
			delete(h.viewers, feed)
			close(feed)
		}
	}
}

// end closes every viewer's feed once the terminal has no more output.
func (h *history) end() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for viewer := range h.viewers {
		delete(h.viewers, viewer)
		close(viewer)
	}
}

// ended reports whether the terminal's output has ended.
func (h *history) ended() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed
}
