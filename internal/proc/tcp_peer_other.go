//go:build !windows

package proc

import "net/netip"

// TCPPeerProcess refuses: naming the process at the other end of a
// connection is not ported yet.
func TCPPeerProcess(peer, local netip.AddrPort) (int, error) {
	return 0, notPorted("naming a connection's peer process")
}
