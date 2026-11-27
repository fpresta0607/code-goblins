//go:build linux || darwin

package host

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/fpresta0607/code-goblins/internal/localsocket"
	"github.com/fpresta0607/code-goblins/internal/priority"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/pty"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// Run hosts spec's terminal until it ends or a viewer closes it: it records
// itself under stateDir, serves viewers over its socket, and removes its
// record on the way out. The terminal's program leads a session of its own
// in a pseudo terminal, and Close ends it and everything it started.
func Run(stateDir string, spec Spec) error {
	defer priority.AboveTheWork()()
	if err := state.ValidTaskID(spec.ID); err != nil {
		return err
	}
	// One live host per terminal: a second would take over the record that
	// finds the first, and leave the first unreachable.
	if running, err := ReadRecord(stateDir, spec.ID); err == nil && isServing(running) {
		return fmt.Errorf("host: terminal %s already runs in host pid %d", spec.ID, running.HostPID)
	}
	if spec.IsStream {
		return fmt.Errorf("host: terminal %s: %w", spec.ID, errStreamNotPorted)
	}
	var proof [32]byte
	if _, err := rand.Read(proof[:]); err != nil {
		return err
	}
	output, err := newHistory(spec.Cols, spec.Rows)
	if err != nil {
		return err
	}
	args, processors := terminalStart(spec)
	started, err := pty.Start(pty.Spec{Args: args, Dir: spec.Dir, Env: terminalEnvironment(spec.ID, hex.EncodeToString(proof[:])), Cols: spec.Cols, Rows: spec.Rows, Processors: processors})
	if err != nil {
		return err
	}
	record, socket, err := announce(stateDir, spec.ID, started.PID(), started.StartTime(), hex.EncodeToString(proof[:]))
	if err != nil {
		_ = started.Close()
		return err
	}
	defer socket.Close()
	defer removeRecord(stateDir, spec.ID, record.HostPID)
	accept := func() (hostConnection, error) {
		connection, err := socket.AcceptUnix()
		if err != nil {
			return nil, err
		}
		return connection, nil
	}
	return runTerminal(stateDir, spec, record, emulatedTerminal{Terminal: started, output: output}, output, accept)
}

// terminalEnvironment is this host's environment with IDVariable naming
// terminal id and ProofVariable carrying its proof, in place of any value
// inherited from a terminal the host was launched in.
func terminalEnvironment(id, proof string) []string {
	env := slices.DeleteFunc(os.Environ(), func(entry string) bool {
		return strings.HasPrefix(entry, IDVariable+"=") || strings.HasPrefix(entry, ProofVariable+"=")
	})
	return append(env, IDVariable+"="+id, ProofVariable+"="+proof)
}

// announce opens the host's socket, in the folder only this user may enter
// that holds the home's sockets, and records where to find it, with when the
// terminal's program started and the digest of the proof value its terminal
// carries.
func announce(stateDir, id string, childPID int, childStart time.Time, proof string) (Record, *net.UnixListener, error) {
	folder := localsocket.Folder(stateDir)
	if err := os.Mkdir(folder, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return Record{}, nil, fmt.Errorf("host: make the socket folder: %w", err)
	}
	if err := localsocket.CheckFolder(folder); err != nil {
		return Record{}, nil, fmt.Errorf("host: %w", err)
	}
	machineStart, err := machineStarted()
	if err != nil {
		return Record{}, nil, err
	}
	instance, err := proc.Instance(os.Getpid())
	if err != nil {
		return Record{}, nil, fmt.Errorf("host: read this host's instance: %w", err)
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Record{}, nil, err
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return Record{}, nil, err
	}
	path := filepath.Join(folder, "host-"+hex.EncodeToString(random[:])+".sock")
	socket, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return Record{}, nil, fmt.Errorf("host: listen on %s: %w", path, err)
	}
	record := Record{ID: id, Pipe: path, Token: hex.EncodeToString(secret[:]), Version: Version, HostPID: os.Getpid(), HostInstance: instance, ChildPID: childPID, ChildStart: childStart, Started: time.Now().UTC(), ProofSum: ProofSum(proof)}
	if err := writeRecord(stateDir, record); err != nil {
		socket.Close()
		return Record{}, nil, fmt.Errorf("host: record the host: %w", err)
	}
	// The record goes when this host does and is replaced by the next one's.
	// The terminal's proofs outlast both, for what it leaves running.
	if err := keepProof(stateDir, id, record.ProofSum, record.Started, machineStart); err != nil {
		removeRecord(stateDir, id, record.HostPID)
		socket.Close()
		return Record{}, nil, fmt.Errorf("host: keep the terminal's proof: %w", err)
	}
	return record, socket, nil
}

// dialPipe connects to the host's socket and proves the host serves it: the
// socket must sit in a folder only this user may enter, and its server must
// be the host the record names.
func dialPipe(name string, hostPID int) (hostConnection, error) {
	if err := localsocket.CheckFolder(filepath.Dir(name)); err != nil {
		return nil, fmt.Errorf("host: connect to %s: %w", name, err)
	}
	connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: name, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("host: connect to %s: %w", name, err)
	}
	if server, err := localsocket.PeerProcess(connection); err != nil || server != hostPID {
		connection.Close()
		return nil, fmt.Errorf("host: %s is not served by host pid %d", name, hostPID)
	}
	return connection, nil
}

// pipeClosing reports whether err is a write to a socket whose host end is
// closed or closing: EPIPE, ECONNRESET or ENOTCONN.
func pipeClosing(err error) bool {
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ENOTCONN)
}

// Running reports whether the host record names may still run: only a pid
// that names no process, or one that has exited, or a process that is
// another run of a program than the host's instance, does not. A process
// that cannot be read may still be the host.
func Running(record Record) bool {
	instance, err := readInstance(record.HostPID)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	return err != nil || instance == record.HostInstance
}

// readInstance reads the instance Running compares; a test stands in for it
// to fail the read the way a process this one may not read does.
var readInstance = proc.Instance
