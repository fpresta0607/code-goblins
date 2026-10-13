package browserlogin

import (
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

// ownerOnlyDevTools proves the client socket's Windows user before either
// metadata or a WebSocket can reach Chrome. Loopback alone is not identity.
func ownerOnlyDevTools(owner string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked := time.Now()
		refuse := func() { http.Error(w, "browser access is limited to its Windows user", http.StatusForbidden) }
		peer, err := netip.ParseAddrPort(r.RemoteAddr)
		if err != nil || !peer.Addr().IsLoopback() || owner == "" {
			refuse()
			return
		}
		address, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
		if !ok {
			refuse()
			return
		}
		local, err := netip.ParseAddrPort(address.String())
		if err != nil || !local.Addr().IsLoopback() {
			refuse()
			return
		}
		pid, err := proc.TCPPeerProcess(peer, local)
		if err != nil {
			refuse()
			return
		}
		started, ok := proc.StartTime(pid)
		if !ok || started.After(asked) {
			refuse()
			return
		}
		user, err := proc.OwnerOf(pid, started)
		if err != nil || user.User != owner {
			refuse()
			return
		}
		next.ServeHTTP(w, r)
	})
}
