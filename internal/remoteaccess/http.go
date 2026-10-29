package remoteaccess

import (
	"context"
	"mime"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

// Gate validates the private proxy before passing a request to the existing
// board. Host and Origin are translated only after authentication, preserving
// the board's CSRF and WebSocket token checks. Proxy/identity headers stay in
// place so credential entry and other PC-only actions remain PC-only. What it
// admits it marks (Admitted), for what the Overlord chose to allow from his
// private board too: AFK mode's switch and a service grant card's Grant.
type Gate struct {
	manager *Manager
	parent  context.Context
	next    http.Handler
	mu      sync.Mutex
	config  Config
	ctx     context.Context
	cancel  context.CancelFunc
}

// admittedKey marks, in a request's context, that the gate admitted it.
type admittedKey struct{}

// Admitted reports whether the gate admitted r as the owner's, through his
// private connection: Tailscale's identity named his login for this PC's
// private name, from his private origin. Those are headers, which a program
// on this PC can send itself or have a Serve route of its own carry, so a
// caller grants on them only what is safe from anyone, or what the Overlord
// chose to allow knowing that.
func Admitted(r *http.Request) bool {
	isAdmitted, _ := r.Context().Value(admittedKey{}).(bool)
	return isAdmitted
}

func NewGate(ctx context.Context, manager *Manager, next http.Handler) *Gate {
	g := &Gate{manager: manager, next: next, parent: ctx}
	g.refresh()
	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				g.mu.Lock()
				g.cancel()
				g.mu.Unlock()
				return
			case <-tick.C:
				g.refresh()
			}
		}
	}()
	return g
}

func (g *Gate) refresh() {
	c, err := Read(g.manager.State)
	if err != nil || !c.Enabled || c.Target != g.manager.Target {
		c = Config{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ctx == nil || c != g.config {
		if g.cancel != nil {
			g.cancel()
		}
		g.config = c
		g.ctx, g.cancel = context.WithCancel(g.parent)
	}
}

func (g *Gate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	localHost := strings.TrimPrefix(g.manager.Target, "http://")
	if r.Host == localHost {
		// A proxy must never use the local fast path: otherwise a changed
		// Tailscale route that rewrites Host could bypass remote authorization.
		for name := range r.Header {
			name = http.CanonicalHeaderKey(name)
			if strings.HasPrefix(name, "Tailscale-") || strings.HasPrefix(name, "X-Forwarded-") || name == "Forwarded" || name == "Via" || name == "X-Real-Ip" {
				http.Error(w, "Remote proxy must use its configured HTTPS hostname", http.StatusForbidden)
				return
			}
		}
		g.next.ServeHTTP(w, r)
		return
	}
	// Read again at admission. The timer revokes streams that already passed.
	g.refresh()
	g.mu.Lock()
	c, access := g.config, g.ctx
	g.mu.Unlock()
	refuse := func() {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "Remote access is disabled or this Tailscale account is not allowed", http.StatusForbidden)
	}
	remoteOrigin := c.Origin
	scrawl := c.Scrawl && c.Origin != "" && r.Host == strings.TrimPrefix(c.scrawlRoute().Origin, "https://")
	if scrawl {
		remoteOrigin = c.scrawlRoute().Origin
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(peer)
	if err != nil || ip == nil || !ip.IsLoopback() || !c.Enabled || r.Host != strings.TrimPrefix(remoteOrigin, "https://") {
		refuse()
		return
	}
	values := r.Header.Values("Tailscale-User-Login")
	if len(values) != 1 {
		refuse()
		return
	}
	login, err := new(mime.WordDecoder).DecodeHeader(values[0])
	if err != nil || login != c.Login {
		refuse()
		return
	}
	origins := r.Header.Values("Origin")
	opaqueAsset := scrawl && scrawlSandboxResource(r) && (len(origins) == 0 || len(origins) == 1 && origins[0] == "null")
	if len(origins) > 1 || !opaqueAsset && (len(origins) == 1 && origins[0] != remoteOrigin || r.Header.Get("Sec-Fetch-Site") == "cross-site") {
		refuse()
		return
	}
	if (r.Method != "GET" && r.Method != "HEAD" || r.Header.Get("Upgrade") != "") && r.Header.Get("Origin") != remoteOrigin {
		refuse()
		return
	}
	ctx, cancel := context.WithCancel(context.WithValue(r.Context(), admittedKey{}, true))
	stop := context.AfterFunc(access, cancel)
	defer stop()
	defer cancel()
	if access.Err() != nil {
		refuse()
		return
	}
	request := r.Clone(ctx)
	request.Host = localHost
	if len(origins) == 1 {
		request.Header.Set("Origin", g.manager.Target)
	}
	if scrawl {
		g.manager.serveScrawl(w, request, c)
		return
	}
	g.next.ServeHTTP(w, request)
}

// Scrawl's sandboxed artifact and whiteboard frames have opaque origins.
// Their read-only assets need Origin:null/cross-site fetches, never API,
// session, mutation or socket access. Identity is still required above.
func scrawlSandboxResource(r *http.Request) bool {
	if r.Method != "GET" && r.Method != "HEAD" || r.Header.Get("Upgrade") != "" {
		return false
	}
	p := r.URL.Path
	if path.Clean(p) != p || strings.Contains(p, "\\") {
		return false
	}
	if p == "/sdk.js" || p == "/whiteboard-frame" || p == "/assets/goblin-app.png" {
		return true
	}
	for _, prefix := range []string{"/artifact/", "/whiteboard-assets/", "/assets/fonts/", "/design/"} {
		if strings.HasPrefix(p, prefix) && len(p) > len(prefix) {
			return true
		}
	}
	return false
}
