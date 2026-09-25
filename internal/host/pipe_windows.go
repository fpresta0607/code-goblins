package host

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// pipeName is a fresh, unguessable pipe for one host.
func pipeName() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return `\\.\pipe\code-goblins-host-` + hex.EncodeToString(random[:]), nil
}

// userOnly is a security descriptor that lets this Windows user, and no one
// else, open the pipe.
func userOnly() (*windows.SecurityAttributes, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("host: read this user's identity: %w", err)
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return nil, fmt.Errorf("host: build the pipe's security descriptor: %w", err)
	}
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor}, nil
}

// listener serves one pipe name. Every instance is overlapped, so a connected
// client's file reads and writes at once through the runtime's poller.
type listener struct {
	name     *uint16
	security *windows.SecurityAttributes
	waiting  windows.Handle
}

// listen creates the pipe's first instance. FILE_FLAG_FIRST_PIPE_INSTANCE
// fails if another process already made a pipe of this name, so nothing can
// stand in for the host by creating it first.
func listen(name string) (*listener, error) {
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	security, err := userOnly()
	if err != nil {
		return nil, err
	}
	l := &listener{name: path, security: security}
	if l.waiting, err = l.instance(true); err != nil {
		return nil, fmt.Errorf("host: create pipe %s: %w", name, err)
	}
	return l, nil
}

func (l *listener) instance(first bool) (windows.Handle, error) {
	flags := uint32(windows.PIPE_ACCESS_DUPLEX | windows.FILE_FLAG_OVERLAPPED)
	if first {
		flags |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
	}
	mode := uint32(windows.PIPE_TYPE_BYTE | windows.PIPE_READMODE_BYTE | windows.PIPE_WAIT | windows.PIPE_REJECT_REMOTE_CLIENTS)
	return windows.CreateNamedPipe(l.name, flags, mode, windows.PIPE_UNLIMITED_INSTANCES, 64<<10, 64<<10, 0, l.security)
}

// accept waits for the next client and returns its connection, then opens a
// fresh instance for the one after it.
func (l *listener) accept() (*os.File, error) {
	connected := l.waiting
	waitErr := waitForClient(connected)
	next, err := l.instance(false)
	if err != nil {
		windows.CloseHandle(connected)
		return nil, fmt.Errorf("host: open the next pipe instance: %w", err)
	}
	l.waiting = next
	if waitErr != nil {
		windows.CloseHandle(connected)
		return nil, waitErr
	}
	return os.NewFile(uintptr(connected), "host pipe"), nil
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

// dialPipe connects to the host's pipe and proves the host serves it: the
// pipe's server process must be the host the record names. The connection
// grants the host no more than identifying this client.
func dialPipe(name string, hostPID int) (*os.File, error) {
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	flags := uint32(windows.FILE_FLAG_OVERLAPPED | windows.SECURITY_SQOS_PRESENT | windows.SECURITY_IDENTIFICATION)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		handle, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, flags, 0)
		if errors.Is(err, windows.ERROR_PIPE_BUSY) && time.Now().Before(deadline) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("host: connect to %s: %w", name, err)
		}
		var server uint32
		if err := windows.GetNamedPipeServerProcessId(handle, &server); err != nil || int(server) != hostPID {
			windows.CloseHandle(handle)
			return nil, fmt.Errorf("host: %s is not served by host pid %d", name, hostPID)
		}
		return os.NewFile(uintptr(handle), name), nil
	}
}
