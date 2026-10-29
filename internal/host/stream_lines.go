package host

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sync"
)

// StreamLine is one line of a stream session: a JSON object its program
// wrote, or one written to its program, numbered in the order the host saw
// them, so a viewer that comes back knows which it has already.
type StreamLine struct {
	Seq       uint64          `json:"seq"`
	IsWritten bool            `json:"written,omitempty"`
	Line      json.RawMessage `json:"line"`
}

// maxStreamLine bounds one line, so its frame fits under maxFrame with room
// for its number. A program's line over it is left out of the session.
const maxStreamLine = maxFrame - 1<<10

// maxStreamHistory bounds what a stream host keeps of its session's lines,
// and how far a viewer may fall behind before it is let go to come back.
var maxStreamHistory = 16 << 20

// lineHistory is a stream session's lines, the newest maxStreamHistory bytes
// of them, and the feed of each viewer.
type lineHistory struct {
	mu      sync.Mutex
	kept    []StreamLine
	size    int
	next    uint64
	feeds   map[*lineFeed]struct{}
	isEnded bool
}

// lineFeed is the lines one viewer has yet to be sent.
type lineFeed struct {
	history *lineHistory
	pending []StreamLine
	size    int
	wake    chan struct{}
	// isLetGo is set for a viewer that left, or fell maxStreamHistory behind.
	isLetGo bool
}

func newLineHistory() *lineHistory {
	return &lineHistory{next: 1, feeds: map[*lineFeed]struct{}{}}
}

// add numbers line, keeps it and hands it to every viewer.
func (h *lineHistory) add(line json.RawMessage, isWritten bool) StreamLine {
	h.mu.Lock()
	defer h.mu.Unlock()
	entry := StreamLine{Seq: h.next, IsWritten: isWritten, Line: line}
	h.next++
	h.kept = append(h.kept, entry)
	h.size += len(line)
	for len(h.kept) > 1 && h.size > maxStreamHistory {
		h.size -= len(h.kept[0].Line)
		h.kept = h.kept[1:]
	}
	for feed := range h.feeds {
		feed.pending = append(feed.pending, entry)
		feed.size += len(line)
		if feed.size > maxStreamHistory {
			feed.isLetGo = true
			delete(h.feeds, feed)
		}
		feed.signal()
	}
	return entry
}

// end says the program wrote its last line.
func (h *lineHistory) end() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.isEnded = true
	for feed := range h.feeds {
		feed.signal()
	}
}

// ended reports whether the program wrote its last line.
func (h *lineHistory) ended() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.isEnded
}

// attach returns the lines kept so far and a feed of every later one, and
// the function that lets the feed go.
func (h *lineHistory) attach() ([]StreamLine, *lineFeed, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	feed := &lineFeed{history: h, wake: make(chan struct{}, 1)}
	h.feeds[feed] = struct{}{}
	detach := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		feed.isLetGo = true
		delete(h.feeds, feed)
		feed.signal()
	}
	return append([]StreamLine(nil), h.kept...), feed, detach
}

func (f *lineFeed) signal() {
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// take waits for lines the viewer has yet to be sent and returns them. It
// returns false once there are none and none will come: the program wrote its
// last line, or the viewer was let go.
func (f *lineFeed) take() ([]StreamLine, bool) {
	for {
		f.history.mu.Lock()
		lines := f.pending
		isOver := f.isLetGo || f.history.isEnded
		if len(lines) > 0 && !f.isLetGo {
			f.pending, f.size = nil, 0
			f.history.mu.Unlock()
			return lines, true
		}
		f.history.mu.Unlock()
		if isOver {
			return nil, false
		}
		<-f.wake
	}
}

// errLineTooLong is a line over maxStreamLine, which was read past.
var errLineTooLong = errors.New("host: a line over the bound")

// readStreamLine reads the next line from r, without its end. A line over
// maxStreamLine is read past and returned as errLineTooLong with its length.
func readStreamLine(r *bufio.Reader) ([]byte, int, error) {
	var line []byte
	length := 0
	for {
		part, err := r.ReadSlice('\n')
		length += len(part)
		if length <= maxStreamLine+1 {
			line = append(line, part...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil && (!errors.Is(err, io.EOF) || length == 0) {
			return nil, 0, err
		}
		line = bytes.TrimRight(line, "\r\n")
		if length > maxStreamLine+1 {
			return nil, length, errLineTooLong
		}
		return line, length, nil
	}
}

// streamObject is line as one compact JSON object, or why it is not one.
func streamObject(line []byte) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return nil, errors.New("host: a stream line is one JSON object")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, trimmed); err != nil {
		return nil, err
	}
	return compact.Bytes(), nil
}
