package localsocket

import (
	"net"

	"golang.org/x/sys/unix"
)

// PeerProcess is the ID of the process at the other end of conn, as the
// kernel recorded it when that process connected, or listened for the
// connection (SO_PEERCRED).
func PeerProcess(conn *net.UnixConn) (int, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var credentials *unix.Ucred
	var readErr error
	if err := raw.Control(func(fd uintptr) {
		credentials, readErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if readErr != nil {
		return 0, readErr
	}
	return int(credentials.Pid), nil
}
