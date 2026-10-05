//go:build windows

package supervisor

import (
	"errors"
	"net/netip"
	"syscall"
	"unsafe"
)

var procGetExtendedTcpTable = syscall.NewLazyDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

const (
	// tcpTableOwnerPIDConnections asks GetExtendedTcpTable for every
	// connection with the process that owns its socket.
	tcpTableOwnerPIDConnections = 4
	errorInsufficientBuffer     = syscall.Errno(122)
)

// tcpOwnerRow is Windows's MIB_TCPROW_OWNER_PID: one IPv4 connection as one of
// its two sockets sees it, and the process that owns that socket. Addresses
// and ports are in network byte order, a port in the first two bytes of its
// field.
type tcpOwnerRow struct {
	State      uint32
	LocalAddr  [4]byte
	LocalPort  [4]byte
	RemoteAddr [4]byte
	RemotePort [4]byte
	OwningPID  uint32
}

func (row tcpOwnerRow) local() netip.AddrPort {
	return netip.AddrPortFrom(netip.AddrFrom4(row.LocalAddr), uint16(row.LocalPort[0])<<8|uint16(row.LocalPort[1]))
}

func (row tcpOwnerRow) remote() netip.AddrPort {
	return netip.AddrPortFrom(netip.AddrFrom4(row.RemoteAddr), uint16(row.RemotePort[0])<<8|uint16(row.RemotePort[1]))
}

// tcpPeerProcess names the process at the other end of a connection to the
// board on this machine: the one that owns the socket the connection came
// from. Windows lists every IPv4 connection once for each of its sockets; the
// peer's is the one whose own end is peer and whose far end is board.
func tcpPeerProcess(peer, board netip.AddrPort) (int, error) {
	peer, board = netip.AddrPortFrom(peer.Addr().Unmap(), peer.Port()), netip.AddrPortFrom(board.Addr().Unmap(), board.Port())
	if !peer.Addr().Is4() || !board.Addr().Is4() {
		return 0, errors.New("the connection is not an IPv4 connection")
	}
	// The table grows while it is read, so the size Windows asks for is tried
	// again a few times.
	var table []byte
	var size uint32
	for attempt := 0; ; attempt++ {
		var buffer unsafe.Pointer
		if len(table) > 0 {
			buffer = unsafe.Pointer(&table[0])
		}
		status, _, _ := procGetExtendedTcpTable.Call(uintptr(buffer), uintptr(unsafe.Pointer(&size)), 0, syscall.AF_INET, tcpTableOwnerPIDConnections, 0)
		if status == 0 && len(table) > 0 {
			break
		}
		if status != 0 && syscall.Errno(status) != errorInsufficientBuffer || attempt == 4 {
			return 0, errors.New("Windows did not list this machine's connections")
		}
		table = make([]byte, max(size, uint32(unsafe.Sizeof(uint32(0)))))
	}
	count := *(*uint32)(unsafe.Pointer(&table[0]))
	first := unsafe.Sizeof(count)
	if uintptr(len(table)) < first+uintptr(count)*unsafe.Sizeof(tcpOwnerRow{}) {
		return 0, errors.New("Windows listed this machine's connections short")
	}
	if count > 0 {
		for _, row := range unsafe.Slice((*tcpOwnerRow)(unsafe.Pointer(&table[first])), count) {
			if row.local() == peer && row.remote() == board {
				return int(row.OwningPID), nil
			}
		}
	}
	return 0, errors.New("Windows lists no connection from " + peer.String() + " to the board")
}
