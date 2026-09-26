// Package herdrtest stands in for a Herdr session's server in tests.
package herdrtest

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

// Request is one request the socket received.
type Request struct {
	ID     string         `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

// Socket serves a named pipe the way a Herdr session's socket does: it reads
// one request on a connection, answers it and closes the connection. Path is
// what Herdr's status reports as the socket; the pipe is named after it.
type Socket struct {
	Path string
	// Answer is the line sent back for a request; nil answers pane.scroll as
	// Herdr does, with the offset held within History lines, and every other
	// request with an ok result.
	Answer func(Request) string
	// History is how many lines a pane can scroll up.
	History int

	mu       sync.Mutex
	requests []Request
	closed   bool
	done     chan struct{}
}

// NewSocket serves a socket until the test ends.
func NewSocket(t testing.TB) *Socket {
	t.Helper()
	s := &Socket{Path: filepath.Join(t.TempDir(), "herdr.sock"), done: make(chan struct{})}
	name, err := windows.UTF16PtrFromString(`\\.\pipe\` + s.Path)
	if err != nil {
		t.Fatal(err)
	}
	instance := func() (windows.Handle, error) {
		mode := uint32(windows.PIPE_TYPE_BYTE | windows.PIPE_READMODE_BYTE | windows.PIPE_WAIT | windows.PIPE_REJECT_REMOTE_CLIENTS)
		return windows.CreateNamedPipe(name, windows.PIPE_ACCESS_DUPLEX, mode, windows.PIPE_UNLIMITED_INSTANCES, 64<<10, 64<<10, 0, nil)
	}
	first, err := instance()
	if err != nil {
		t.Fatal(err)
	}
	go s.serve(first, instance)
	t.Cleanup(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		// A client connection wakes the listener blocked on the next one.
		if wake, err := os.OpenFile(`\\.\pipe\`+s.Path, os.O_RDWR, 0); err == nil {
			wake.Close()
		}
		<-s.done
	})
	return s
}

func (s *Socket) serve(waiting windows.Handle, instance func() (windows.Handle, error)) {
	defer close(s.done)
	for {
		if err := windows.ConnectNamedPipe(waiting, nil); err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
			windows.CloseHandle(waiting)
			return
		}
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			windows.CloseHandle(waiting)
			return
		}
		next, err := instance()
		conn := os.NewFile(uintptr(waiting), "herdr socket")
		s.answer(conn)
		conn.Close()
		if err != nil {
			return
		}
		waiting = next
	}
}

func (s *Socket) answer(conn *os.File) {
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var request Request
	if err := json.Unmarshal(line, &request); err != nil {
		return
	}
	s.mu.Lock()
	s.requests = append(s.requests, request)
	answer, history := s.Answer, s.History
	s.mu.Unlock()
	reply := `{"id":` + quote(request.ID) + `,"result":{"type":"ok"}}`
	if offset, ok := request.Params["offset_from_bottom"].(float64); ok && request.Method == "pane.scroll" {
		shown := min(int(offset), history)
		reply = fmt.Sprintf(`{"id":%s,"result":{"type":"pane_info","pane":{"pane_id":%s,"scroll":{"offset_from_bottom":%d,"max_offset_from_bottom":%d,"viewport_rows":40}}}}`, quote(request.ID), quote(fmt.Sprint(request.Params["pane_id"])), shown, history)
	}
	if answer != nil {
		reply = answer(request)
	}
	if reply != "" {
		_, _ = conn.Write([]byte(reply + "\n"))
	}
}

// Requests returns every request received so far, in order.
func (s *Socket) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// Status is `herdr status --json` for a running server on this socket.
func (s *Socket) Status() string {
	return `{"client":{"protocol":22},"server":{"status":"running","running":true,"protocol":22,"compatible":true,"socket":` + quote(s.Path) + `}}`
}

func quote(text string) string {
	data, _ := json.Marshal(text)
	return string(data)
}
