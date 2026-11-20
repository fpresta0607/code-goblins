package localsocket

import (
	"net"

	"golang.org/x/sys/unix"
)

// PeerProcess is the ID of the process at the other end of conn, as the
// kernel recorded it when that process connected, or listened for the
// connection (LOCAL_PEERPID).
func PeerProcess(conn *net.UnixConn) (int, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var pid int
	var readErr error
	if err := raw.Control(func(fd uintptr) {
		pid, readErr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	}); err != nil {
		return 0, err
	}
	if readErr != nil {
		return 0, readErr
	}
	return pid, nil
}
