package cirunner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// requestLimit is how long a client has to send its one request line,
	// and maxRequest how long the line may be, so no client holds the
	// service by sending nothing, or by sending without end.
	requestLimit = 5 * time.Second
	maxRequest   = 4 << 10
	// pendingLimit bounds the GitHub calls that find a waiting job.
	pendingLimit = 30 * time.Second
)

// ErrNotServing is a pipe no service answers on.
var ErrNotServing = errors.New("cirunner: the CI runner service is not running")

// Serve answers the fleet on the pipe name until ctx ends, then waits for the
// job a client asked for, if one runs, to end and be cleaned up. Only
// SYSTEM, Administrators, the service itself and allowed, a SID, may open
// the pipe. Each client is answered on its own, so a client that asks what
// runs is answered while a job runs, and Service refuses a second run.
func Serve(ctx context.Context, s *Service, name, allowed string) error {
	pipes, err := listen(name, allowed)
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { wake(name) })
	defer stop()
	var clients sync.WaitGroup
	defer clients.Wait()
	for {
		connection, err := pipes.accept()
		if ctx.Err() != nil {
			if connection != nil {
				connection.Close()
			}
			return pipes.close()
		}
		if err != nil {
			// A client that left before it connected costs a pause, never
			// a spin.
			time.Sleep(50 * time.Millisecond)
			continue
		}
		clients.Add(1)
		go func() {
			defer clients.Done()
			s.answer(ctx, connection)
		}()
	}
}

// answer reads a client's one request and answers it. A request that does
// not come within requestLimit, or runs past maxRequest, is dropped
// unanswered.
func (s *Service) answer(ctx context.Context, connection *os.File) {
	defer connection.Close()
	reply := json.NewEncoder(connection)
	_ = connection.SetReadDeadline(time.Now().Add(requestLimit))
	line, err := bufio.NewReaderSize(io.LimitReader(connection, maxRequest), maxRequest).ReadBytes('\n')
	if err != nil {
		return
	}
	_ = connection.SetReadDeadline(time.Time{})
	var request Request
	if err := json.Unmarshal(line, &request); err != nil {
		s.Log("refused a request that is not JSON")
		_ = reply.Encode(Reply{Kind: ReplyError, Error: "the request is not JSON"})
		return
	}
	switch request.Kind {
	case AskPending:
		asking, cancel := context.WithTimeout(ctx, pendingLimit)
		defer cancel()
		pending, err := s.Pending(asking)
		if err != nil {
			_ = reply.Encode(Reply{Kind: ReplyError, Error: err.Error()})
			return
		}
		_ = reply.Encode(Reply{Kind: ReplyPending, Pending: &pending})
	case AskStatus:
		_ = reply.Encode(Reply{Kind: ReplyStatus, Line: s.Status()})
	case AskRun:
		// The client sends nothing after its request, so a read that ends
		// is the client gone, which ends the job, as the end of the turn
		// the client runs in.
		running, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			var rest [1]byte
			_, _ = connection.Read(rest[:])
			cancel()
		}()
		s.Log("a run was asked for")
		outcome, err := s.Run(running, func(line string) {
			s.Log(line)
			_ = reply.Encode(Reply{Kind: ReplyProgress, Line: line})
		})
		done := Reply{Kind: ReplyDone, Outcome: outcome}
		s.Log("the run ended: " + string(outcome))
		if err != nil {
			done.Error = err.Error()
			s.Log("the run's error: " + done.Error)
		}
		_ = reply.Encode(done)
	default:
		refusal := fmt.Sprintf("the service answers pending, run and status, not %q", request.Kind)
		s.Log("refused: " + refusal)
		_ = reply.Encode(Reply{Kind: ReplyError, Error: refusal})
	}
}

// Ask sends request to the service on the pipe name and hands each reply to
// reply, until the last: a run's progress lines come before its done reply.
// ctx's end closes the pipe, which ends a run the service runs for it. A
// refusal is an error, never a reply.
func Ask(ctx context.Context, name string, request Request, reply func(Reply)) error {
	connection, err := dial(ctx, name)
	if err != nil {
		return err
	}
	defer connection.Close()
	stop := context.AfterFunc(ctx, func() { connection.Close() })
	defer stop()
	line, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if _, err := connection.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("cirunner: ask the service: %w", err)
	}
	replies := bufio.NewScanner(connection)
	replies.Buffer(make([]byte, 0, 64<<10), maxAnswer)
	for replies.Scan() {
		var answer Reply
		if err := json.Unmarshal(replies.Bytes(), &answer); err != nil {
			return fmt.Errorf("cirunner: the service answered what is not a reply: %w", err)
		}
		if answer.Kind == ReplyError {
			return fmt.Errorf("cirunner: the service refused: %s", answer.Error)
		}
		reply(answer)
		if answer.Kind != ReplyProgress {
			return nil
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("cirunner: the service closed the pipe before it answered: %v", replies.Err())
}

// dial opens the pipe name, waiting while every instance is busy, for as long
// as ctx lasts. The service learns only who the client is, and cannot act as
// it.
func dial(ctx context.Context, name string) (*os.File, error) {
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	flags := uint32(windows.FILE_FLAG_OVERLAPPED | windows.SECURITY_SQOS_PRESENT | windows.SECURITY_IDENTIFICATION)
	for {
		handle, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, flags, 0)
		switch {
		case err == nil:
			return os.NewFile(uintptr(handle), name), nil
		case errors.Is(err, windows.ERROR_FILE_NOT_FOUND):
			return nil, ErrNotServing
		case !errors.Is(err, windows.ERROR_PIPE_BUSY):
			return nil, fmt.Errorf("cirunner: connect to %s: %w", name, err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// wake connects to the pipe name and leaves at once, so a Serve waiting for
// its next client sees its context ended. It tries for a second, while the
// next instance is made.
func wake(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for ctx.Err() == nil {
		if connection, err := dial(ctx, name); err == nil {
			connection.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// pipeSecurity lets SYSTEM, Administrators and this process's own user,
// which makes each next instance, open the pipe, and allowed read and write
// it. allowed is parsed as a SID, so no text of its own reaches the
// descriptor. The medium integrity label lets the fleet, at medium
// integrity, open a pipe the service made at a higher one.
func pipeSecurity(allowed string) (*windows.SecurityAttributes, error) {
	client, err := windows.StringToSid(allowed)
	if err != nil {
		return nil, fmt.Errorf("cirunner: the allowed user %q is not a SID: %w", allowed, err)
	}
	self, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("cirunner: read the service's identity: %w", err)
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;" + self.User.Sid.String() + ")(A;;GRGW;;;" + client.String() + ")S:(ML;;NW;;;ME)")
	if err != nil {
		return nil, fmt.Errorf("cirunner: build the pipe's security descriptor: %w", err)
	}
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor}, nil
}

// listener serves one pipe name, as the terminal host's does. Every instance
// is overlapped, so a connected client's file reads and writes through the
// runtime's poller, with deadlines.
type listener struct {
	name     *uint16
	security *windows.SecurityAttributes
	waiting  windows.Handle
}

// listen makes the pipe's first instance. FILE_FLAG_FIRST_PIPE_INSTANCE
// fails if another process already made a pipe of this name, so nothing can
// stand in for a running service.
func listen(name, allowed string) (*listener, error) {
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	security, err := pipeSecurity(allowed)
	if err != nil {
		return nil, err
	}
	l := &listener{name: path, security: security}
	if l.waiting, err = l.instance(true); err != nil {
		return nil, fmt.Errorf("cirunner: create pipe %s: %w", name, err)
	}
	return l, nil
}

func (l *listener) instance(isFirst bool) (windows.Handle, error) {
	flags := uint32(windows.PIPE_ACCESS_DUPLEX | windows.FILE_FLAG_OVERLAPPED)
	if isFirst {
		flags |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
	}
	mode := uint32(windows.PIPE_TYPE_BYTE | windows.PIPE_READMODE_BYTE | windows.PIPE_WAIT | windows.PIPE_REJECT_REMOTE_CLIENTS)
	return windows.CreateNamedPipe(l.name, flags, mode, windows.PIPE_UNLIMITED_INSTANCES, 64<<10, 64<<10, 0, l.security)
}

// accept waits for the next client and returns its connection, then makes a
// fresh instance for the one after it. On any failure the waiting instance is
// disconnected and kept to wait on again, so it never names a closed handle.
func (l *listener) accept() (*os.File, error) {
	connected := l.waiting
	if err := waitForClient(connected); err != nil {
		windows.DisconnectNamedPipe(connected)
		return nil, err
	}
	next, err := l.instance(false)
	if err != nil {
		windows.DisconnectNamedPipe(connected)
		return nil, fmt.Errorf("cirunner: make the next pipe instance: %w", err)
	}
	l.waiting = next
	return os.NewFile(uintptr(connected), "ci runner pipe"), nil
}

// close closes the instance that waits for the next client.
func (l *listener) close() error {
	return windows.CloseHandle(l.waiting)
}

// waitForClient blocks until a client connects to pipe.
func waitForClient(pipe windows.Handle) error {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(event)
	overlapped := windows.Overlapped{HEvent: event}
	switch err := windows.ConnectNamedPipe(pipe, &overlapped); {
	case err == nil, errors.Is(err, windows.ERROR_PIPE_CONNECTED):
		return nil
	case errors.Is(err, windows.ERROR_IO_PENDING):
		var transferred uint32
		return windows.GetOverlappedResult(pipe, &overlapped, &transferred, true)
	default:
		return err
	}
}
