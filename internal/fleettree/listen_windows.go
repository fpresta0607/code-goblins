package fleettree

import (
	"errors"
	"syscall"
	"unsafe"
)

var procGetExtendedTCPTable = syscall.NewLazyDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

const (
	// tcpTableOwnerPIDListener asks GetExtendedTcpTable for every listening
	// socket with the process that owns it.
	tcpTableOwnerPIDListener = 3
	errorInsufficientBuffer  = syscall.Errno(122)
	afInet6                  = 23
)

// tcpListenRow is Windows' MIB_TCPROW_OWNER_PID, and tcp6ListenRow its
// MIB_TCP6ROW_OWNER_PID. A port is in network byte order in the first two
// bytes of its field.
type tcpListenRow struct {
	State      uint32
	LocalAddr  [4]byte
	LocalPort  [4]byte
	RemoteAddr [4]byte
	RemotePort [4]byte
	OwningPID  uint32
}

type tcp6ListenRow struct {
	LocalAddr     [16]byte
	LocalScopeID  uint32
	LocalPort     [4]byte
	RemoteAddr    [16]byte
	RemoteScopeID uint32
	RemotePort    [4]byte
	State         uint32
	OwningPID     uint32
}

// Listeners maps each process that listens on a TCP port, IPv4 or IPv6, to
// its ports.
func Listeners() (map[int][]int, error) {
	listens := map[int][]int{}
	add := func(pid uint32, port [4]byte) {
		listens[int(pid)] = append(listens[int(pid)], int(port[0])<<8|int(port[1]))
	}
	table, err := listenTable(syscall.AF_INET)
	if err != nil {
		return nil, err
	}
	v4, err := rows[tcpListenRow](table)
	if err != nil {
		return nil, err
	}
	for _, row := range v4 {
		add(row.OwningPID, row.LocalPort)
	}
	if table, err = listenTable(afInet6); err != nil {
		return nil, err
	}
	v6, err := rows[tcp6ListenRow](table)
	if err != nil {
		return nil, err
	}
	for _, row := range v6 {
		add(row.OwningPID, row.LocalPort)
	}
	return listens, nil
}

// listenTable reads one address family's listening table. The table grows
// while it is read, so the size Windows asks for is tried again a few times.
func listenTable(family uint32) ([]byte, error) {
	var table []byte
	var size uint32
	for attempt := 0; ; attempt++ {
		var buffer unsafe.Pointer
		if len(table) > 0 {
			buffer = unsafe.Pointer(&table[0])
		}
		status, _, _ := procGetExtendedTCPTable.Call(uintptr(buffer), uintptr(unsafe.Pointer(&size)), 0, uintptr(family), tcpTableOwnerPIDListener, 0)
		if status == 0 && len(table) > 0 {
			return table, nil
		}
		if status != 0 && syscall.Errno(status) != errorInsufficientBuffer || attempt == 4 {
			return nil, errors.New("Windows did not list this machine's listening ports")
		}
		table = make([]byte, max(size, uint32(unsafe.Sizeof(uint32(0)))))
	}
}

// rows reads a table's rows of type T after its leading count.
func rows[T any](table []byte) ([]T, error) {
	count := *(*uint32)(unsafe.Pointer(&table[0]))
	first := unsafe.Sizeof(count)
	var row T
	if count == 0 {
		return nil, nil
	}
	if uintptr(len(table)) < first+uintptr(count)*unsafe.Sizeof(row) {
		return nil, errors.New("Windows listed this machine's listening ports short")
	}
	return unsafe.Slice((*T)(unsafe.Pointer(&table[first])), count), nil
}
