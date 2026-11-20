//go:build !windows

package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/fpresta0607/code-goblins/internal/awake"
	"github.com/fpresta0607/code-goblins/internal/localsocket"
	"github.com/fpresta0607/code-goblins/internal/lock"
)

// runSocketPath is the supervisor's socket for run requests, one per home.
func runSocketPath(stateDir string) string {
	return filepath.Join(localsocket.Folder(stateDir), "run-requests.sock")
}

// serveRunRequests takes run items over the supervisor's socket until ctx
// ends. Every client is proven by its own process: only one that runs under
// the registered primary CEO gets an item onto the board. The socket sits in
// a folder only this user may enter, and a socket another process still
// serves there is left to it: run requests are then not served at all.
func (s *Service) serveRunRequests(ctx context.Context) {
	path := runSocketPath(s.Store.Home.State)
	if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		s.publish(fmt.Errorf("run requests: %w", err))
		return
	}
	if err := localsocket.CheckFolder(filepath.Dir(path)); err != nil {
		s.refuseRunRequests(fmt.Errorf("run requests: %w, so run requests are not served", err))
		return
	}
	// A supervisor that just stopped lets its socket go within moments; one
	// that still answers after firstInstanceWait is another process's.
	freeing, cancelFreeing := awake.WithTimeout(context.Background(), firstInstanceWait)
	defer cancelFreeing()
	for {
		held, err := net.Dial("unix", path)
		if err != nil {
			break
		}
		_ = held.Close()
		if freeing.Err() != nil {
			s.refuseRunRequests(fmt.Errorf("run requests: another process already serves the socket %s, so run requests are not served; stop it and restart goblins serve", path))
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Nothing answers there, so a socket file left there is a stopped
	// supervisor's.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.publish(fmt.Errorf("run requests: %w", err))
		return
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		s.publish(fmt.Errorf("run requests: the socket could not be made: %w", err))
		return
	}
	defer listener.Close()
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	for {
		conn, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() == nil {
				s.publish(fmt.Errorf("run requests: %w", err))
			}
			return
		}
		go s.handleRunClient(ctx, conn, time.Now())
	}
}

// handleRunClient answers one run request with the reason it was refused, or
// nothing when the item is on the board. A client that has not sent its
// request within runReadTimeout is disconnected unanswered.
func (s *Service) handleRunClient(ctx context.Context, conn *net.UnixConn, connected time.Time) {
	defer conn.Close()
	pid, identified := localsocket.PeerProcess(conn)
	lift := awake.Deadline(conn.SetReadDeadline, runReadTimeout)
	line, err := bufio.NewReader(io.LimitReader(conn, maxRunRequest)).ReadBytes('\n')
	lift()
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return
	}
	reply := s.answerRunRequest(ctx, pid, identified, connected, line, err)
	data, _ := json.Marshal(reply)
	// A written reply stays in the socket for its client to read after this
	// side closes. Only a reply larger than the socket's buffer waits in Write
	// for its reader, so the write is bounded against a client that never
	// reads, and against the supervisor stopping.
	lift = awake.Deadline(conn.SetWriteDeadline, runReplyTimeout)
	defer lift()
	stop := context.AfterFunc(ctx, func() { _ = conn.SetWriteDeadline(time.Now()) })
	defer stop()
	_, _ = conn.Write(append(data, '\n'))
}

// redialSleep is time.Sleep, which a test replaces to learn that a client is
// waiting for the supervisor's socket.
var redialSleep = time.Sleep

// dialRunSocket connects to this home's supervisor, waiting up to wait of the
// time the machine ran for a socket, or its folder, that is not there or not
// answering yet, as a supervisor that is starting leaves them.
func dialRunSocket(stateDir string, wait time.Duration) (*net.UnixConn, error) {
	path := runSocketPath(stateDir)
	opening, cancelOpening := awake.WithTimeout(context.Background(), wait)
	defer cancelOpening()
	for {
		err := localsocket.CheckFolder(filepath.Dir(path))
		if err == nil {
			var conn *net.UnixConn
			if conn, err = net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"}); err == nil {
				return conn, nil
			}
		}
		if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ECONNREFUSED) || opening.Err() != nil {
			return nil, err
		}
		redialSleep(25 * time.Millisecond)
	}
}

// Reported tells the supervisor of this home, if one runs, that a goblin
// just reported, so the board shows the report at once instead of at its
// next refresh. It waits for nothing: with no supervisor, or one that is
// busy, the report shows at the next refresh as before.
func Reported(stateDir string) {
	conn, err := dialRunSocket(stateDir, 0)
	if err != nil {
		return
	}
	defer conn.Close()
	if server, err := localsocket.PeerProcess(conn); err != nil || !lock.HeldByNamed(stateDir, ".watch.lock", server) {
		return
	}
	_, _ = conn.Write([]byte(`{"kind":"reported"}` + "\n"))
}

// askPipe hands one request to the supervisor and returns its answer, or the
// reason it was refused.
func askPipe(stateDir string, req runPipeRequest) (pipeReply, error) {
	// The supervisor has 2 s of time the machine ran to make its socket
	// (awake), since a pause of the whole machine stops it too.
	conn, err := dialRunSocket(stateDir, 2*time.Second)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
		return pipeReply{}, errors.New("the supervisor is not running, so the board cannot take this; start goblins serve")
	}
	if err != nil {
		return pipeReply{}, fmt.Errorf("the run request socket could not be reached: %w", err)
	}
	defer conn.Close()
	// The command goes only to the supervisor of this home: the process that
	// holds its watch lock. A socket served by anything else never sees it.
	server, err := localsocket.PeerProcess(conn)
	if err != nil {
		return pipeReply{}, fmt.Errorf("the run request socket's server could not be identified: %w", err)
	}
	if !lock.HeldByNamed(stateDir, ".watch.lock", server) {
		return pipeReply{}, fmt.Errorf("the run request socket is served by pid %d, which is not this home's supervisor, so nothing was sent", server)
	}
	return exchangeRunRequest(conn, req)
}
