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
	"sync/atomic"
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
	// maxClients bounds the clients answered at once. One past them is
	// dropped as it connects.
	maxClients = 16
	// fleetAccess is what the fleet's user may do with the pipe: read and
	// write it, FILE_GENERIC_READ and FILE_GENERIC_WRITE, less
	// FILE_CREATE_PIPE_INSTANCE, which on a pipe is FILE_APPEND_DATA's bit,
	// so the fleet can make no instance of its own to answer in the
	// service's place.
	fleetAccess = "0x12019b"
)

// Begin takes the pipe name, then puts back what a stop mid-job left
// (Service.Start), so the name is the service's from its first moment, and
// no process can make the pipe while the service starts and answer the
// fleet in its place.
func Begin(ctx context.Context, s *Service, name, allowed string) (*Pipe, error) {
	pipe, err := Listen(name, allowed)
	if err != nil {
		return nil, err
	}
	if err := s.Start(ctx); err != nil {
		return nil, errors.Join(err, pipe.close())
	}
	return pipe, nil
}

// Serve answers the fleet on the pipe until ctx ends, then waits for the
// job a client asked for, if one runs, to end and be cleaned up. Each client
// is answered on its own, so a client that asks what runs is answered while
// a job runs, and Service refuses a second run.
func (p *Pipe) Serve(ctx context.Context, s *Service) error {
	stop := context.AfterFunc(ctx, func() { wake(p.text) })
	defer stop()
	var clients sync.WaitGroup
	defer clients.Wait()
	answering := make(chan struct{}, maxClients)
	for {
		connection, err := p.accept()
		if ctx.Err() != nil {
			if connection != nil {
				connection.Close()
			}
			return p.close()
		}
		if err != nil {
			// A client that left before it connected costs a pause, never
			// a spin.
			time.Sleep(50 * time.Millisecond)
			continue
		}
		select {
		case answering <- struct{}{}:
		default:
			connection.Close()
			s.refuse(fmt.Sprintf("a client past the %d the service answers at once", maxClients))
			continue
		}
		clients.Add(1)
		go func() {
			defer clients.Done()
			defer func() { <-answering }()
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
		s.refuse("a request that is not JSON")
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
		// A run that started something says so, and is logged whole. One
		// that started nothing, as no job waited, is tallied, so runs
		// asked for without end cannot push a job's lines out of the log.
		var isStarted atomic.Bool
		outcome, err := s.Run(running, func(line string) {
			isStarted.Store(true)
			s.Log(line)
			_ = reply.Encode(Reply{Kind: ReplyProgress, Line: line})
		})
		done := Reply{Kind: ReplyDone, Outcome: outcome}
		ending := string(outcome)
		if err != nil {
			done.Error = err.Error()
			ending += ": " + done.Error
		}
		if isStarted.Load() {
			s.Log("the run ended: " + string(outcome))
			if err != nil {
				s.Log("the run's error: " + done.Error)
			}
		} else {
			s.note(&s.idle, func(count int) string {
				return fmt.Sprintf("%d run(s) asked for started nothing, the last ending %s", count, ending)
			})
		}
		_ = reply.Encode(done)
	default:
		s.refuse("a request of a kind it does not answer")
		_ = reply.Encode(Reply{Kind: ReplyError, Error: fmt.Sprintf("the service answers pending, run and status, not %q", request.Kind)})
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
// it (fleetAccess). allowed is parsed as a SID, so no text of its own
// reaches the descriptor, and must name one user's account, never a group,
// which would let in every member. The medium integrity label lets the
// fleet, at medium integrity, open a pipe the service made at a higher one.
func pipeSecurity(allowed string) (*windows.SecurityAttributes, error) {
	client, err := windows.StringToSid(allowed)
	if err != nil {
		return nil, fmt.Errorf("cirunner: the allowed user %q is not a SID: %w", allowed, err)
	}
	if _, _, use, err := client.LookupAccount(""); err != nil || use != windows.SidTypeUser {
		return nil, fmt.Errorf("cirunner: the allowed SID %s names no one user's account (%v)", client, err)
	}
	self, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("cirunner: read the service's identity: %w", err)
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;" + self.User.Sid.String() + ")(A;;" + fleetAccess + ";;;" + client.String() + ")S:(ML;;NW;;;ME)")
	if err != nil {
		return nil, fmt.Errorf("cirunner: build the pipe's security descriptor: %w", err)
	}
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor}, nil
}

// Pipe serves one pipe name, as the terminal host's does. Every instance is
// overlapped, so a connected client's file reads and writes through the
// runtime's poller, with deadlines.
type Pipe struct {
	text     string
	name     *uint16
	security *windows.SecurityAttributes
	waiting  windows.Handle
}

// Listen makes the pipe's first instance, which only SYSTEM,
// Administrators, the service itself and allowed, one user's SID, may open.
// FILE_FLAG_FIRST_PIPE_INSTANCE fails if another process already made a pipe
// of this name, and the fleet's user may make no instance (fleetAccess), so
// nothing can stand in for the service.
func Listen(name, allowed string) (*Pipe, error) {
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	security, err := pipeSecurity(allowed)
	if err != nil {
		return nil, err
	}
	p := &Pipe{text: name, name: path, security: security}
	if p.waiting, err = p.instance(true); err != nil {
		return nil, fmt.Errorf("cirunner: create pipe %s: %w", name, err)
	}
	return p, nil
}

func (p *Pipe) instance(isFirst bool) (windows.Handle, error) {
	flags := uint32(windows.PIPE_ACCESS_DUPLEX | windows.FILE_FLAG_OVERLAPPED)
	if isFirst {
		flags |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
	}
	mode := uint32(windows.PIPE_TYPE_BYTE | windows.PIPE_READMODE_BYTE | windows.PIPE_WAIT | windows.PIPE_REJECT_REMOTE_CLIENTS)
	return windows.CreateNamedPipe(p.name, flags, mode, windows.PIPE_UNLIMITED_INSTANCES, 64<<10, 64<<10, 0, p.security)
}

// accept waits for the next client and returns its connection, then makes a
// fresh instance for the one after it. On any failure the waiting instance is
// disconnected and kept to wait on again, so it never names a closed handle.
func (p *Pipe) accept() (*os.File, error) {
	connected := p.waiting
	if err := waitForClient(connected); err != nil {
		windows.DisconnectNamedPipe(connected)
		return nil, err
	}
	next, err := p.instance(false)
	if err != nil {
		windows.DisconnectNamedPipe(connected)
		return nil, fmt.Errorf("cirunner: make the next pipe instance: %w", err)
	}
	p.waiting = next
	return os.NewFile(uintptr(connected), "ci runner pipe"), nil
}

// close closes the instance that waits for the next client.
func (p *Pipe) close() error {
	return windows.CloseHandle(p.waiting)
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
