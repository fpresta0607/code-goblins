package remoteaccess

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/fpresta0607/code-goblins/internal/execx"
)

func scrawlFixture(t *testing.T, next http.Handler) (*Manager, Config, *atomic.Bool) {
	t.Helper()
	c := testConfig()
	c.Scrawl = true
	c.ScrawlState = t.TempDir()
	unsafe := &atomic.Bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			hosts := []string{"127.0.0.1"}
			if unsafe.Load() {
				hosts = append(hosts, "100.64.1.1")
			}
			_ = json.NewEncoder(w).Encode(scrawlHealth{App: "lavish-axi", StateID: "installation", StateDir: c.ScrawlState, Hosts: []string{"127.0.0.1"}, RequestedHosts: hosts, LinkOrigin: c.ScrawlTarget})
			return
		}
		next.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	c.ScrawlTarget = server.URL
	m := &Manager{State: t.TempDir(), Target: c.Target}
	if err := write(m.State, c); err != nil {
		t.Fatal(err)
	}
	return m, c, unsafe
}

func TestScrawlOriginIsIsolatedAndKeepsItsSandbox(t *testing.T) {
	var upstream atomic.Int32
	var c Config
	m, config, unsafe := scrawlFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.Add(1)
		if r.Host != strings.TrimPrefix(c.ScrawlTarget, "http://") || r.Header.Get("Origin") != c.ScrawlTarget || r.Header.Get("Tailscale-User-Login") != "" || r.Header.Get("X-Forwarded-Host") != "" {
			t.Error("upstream headers not translated", r.Host, r.Header)
		}
		w.Header().Set("Content-Security-Policy", "sandbox allow-scripts")
		w.Header().Set("Location", c.ScrawlTarget+"/session/one?tab=notes#note")
		w.WriteHeader(302)
	}))
	c = config
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var board atomic.Int32
	g := NewGate(ctx, m, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { board.Add(1) }))
	for _, origin := range []string{c.Origin, c.scrawlRoute().Origin} {
		r := remoteRequest("POST", "/api/remote")
		r.Host = strings.TrimPrefix(c.scrawlRoute().Origin, "https://")
		r.Header.Set("Origin", origin)
		r.Header.Set("X-Forwarded-Host", r.Host)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if origin == c.Origin && w.Code != 403 {
			t.Fatal("board origin crossed into Scrawl")
		}
		if origin != c.Origin && (w.Code != 302 || w.Header().Get("Content-Security-Policy") != "sandbox allow-scripts" || w.Header().Get("Location") != c.scrawlRoute().Origin+"/session/one?tab=notes#note") {
			t.Fatal(w.Code, w.Header())
		}
	}
	if board.Load() != 0 || upstream.Load() != 1 {
		t.Fatal("Scrawl reached the board", board.Load(), upstream.Load())
	}
	unsafe.Store(true)
	r := remoteRequest("GET", "/session/one")
	r.Host = strings.TrimPrefix(c.scrawlRoute().Origin, "https://")
	r.Header.Del("Origin")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 503 || upstream.Load() != 1 {
		t.Fatal("unsafe requested listener reached Scrawl", w.Code)
	}
}

func TestScrawlWebsocketDisconnectsOnRemoteOff(t *testing.T) {
	m, c, _ := scrawlFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for {
			kind, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if conn.Write(r.Context(), kind, data) != nil {
				return
			}
		}
	}))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	g := NewGate(ctx, m, http.NotFoundHandler())
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = strings.TrimPrefix(c.scrawlRoute().Origin, "https://")
		r.Header.Set("Tailscale-User-Login", c.Login)
		g.ServeHTTP(w, r)
	}))
	defer proxy.Close()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(proxy.URL, "http")+"/socket", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{c.scrawlRoute().Origin}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageText, []byte("note")); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil || string(data) != "note" {
		t.Fatal(string(data), err)
	}
	c.Enabled = false
	if err := write(m.State, c); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.Read(ctx); err == nil || ctx.Err() != nil {
		t.Fatal("Scrawl socket outlived revocation", err)
	}
}

type scrawlCommands struct {
	fakeTailscale
	unsafe  *atomic.Bool
	starts  int
	stops   int
	failOff bool
	// isOffIgnored stands for a Tailscale whose route outlives its off.
	isOffIgnored bool
	// offStarted, when set, hears Scrawl's off begin, which then waits for
	// offRelease.
	offStarted, offRelease chan struct{}
}

func (f *scrawlCommands) Run(ctx context.Context, r execx.Request) (execx.Result, error) {
	call := strings.Join(r.Args, " ")
	if r.Name == "lavish-axi" && call == "stop" {
		f.stops++
		return execx.Result{}, nil
	}
	if strings.Contains(call, "--https=4312") || call == "serve --https=4311 off" {
		if f.failOff && call == "serve --https=4312 off" {
			return execx.Result{ExitCode: 1}, nil
		}
		if f.offStarted != nil && call == "serve --https=4312 off" {
			close(f.offStarted)
			<-f.offRelease
		}
		if f.isOffIgnored && strings.HasSuffix(call, " off") {
			return execx.Result{}, nil
		}
		var config map[string]map[string]any
		_ = json.Unmarshal([]byte(f.config), &config)
		if config == nil {
			config = map[string]map[string]any{}
		}
		if config["TCP"] == nil {
			config["TCP"] = map[string]any{}
		}
		if config["Web"] == nil {
			config["Web"] = map[string]any{}
		}
		if strings.HasSuffix(call, " off") {
			port := "4312"
			if strings.Contains(call, "4311") {
				port = "4311"
			}
			delete(config["TCP"], port)
			delete(config["Web"], "home.example.ts.net:"+port)
		} else {
			config["TCP"]["4312"] = map[string]any{"HTTPS": true}
			config["Web"]["home.example.ts.net:4312"] = map[string]any{"Handlers": map[string]any{"/": map[string]any{"Proxy": "http://127.0.0.1:4310"}}}
		}
		data, _ := json.Marshal(config)
		f.config = string(data)
		return execx.Result{}, nil
	}
	return f.fakeTailscale.Run(ctx, r)
}
func (f *scrawlCommands) Start(ctx context.Context, r execx.Request) error {
	f.starts++
	if r.Name != "lavish-axi" || strings.Join(r.Args, " ") != "server" || !slices.Contains(r.Env, "LAVISH_AXI_HOST=127.0.0.1") {
		panic("unexpected Scrawl start")
	}
	f.unsafe.Store(false)
	return nil
}

func TestScrawlSetupPreservesInstallationAndCleansBothOwnedRoutes(t *testing.T) {
	m, c, unsafe := scrawlFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "review") }))
	u, _ := url.Parse(c.ScrawlTarget)
	t.Setenv("LAVISH_AXI_PORT", u.Port())
	t.Setenv("LAVISH_AXI_STATE_DIR", c.ScrawlState)
	unsafe.Store(true)
	f := &scrawlCommands{fakeTailscale: fakeTailscale{config: ownProxy}, unsafe: unsafe}
	m.Commands, m.Starter = f, f
	s, err := m.EnableScrawl(context.Background())
	if err != nil || !s.Ready || !s.ScrawlReady || f.stops != 1 || f.starts != 1 {
		t.Fatal(s, err, f.stops, f.starts)
	}
	if _, err := m.EnableScrawl(context.Background()); err != nil || f.starts != 1 {
		t.Fatal("repeat setup restarted healthy sessions", err)
	}
	f.failOff = true
	if err := m.Disable(context.Background()); err == nil {
		t.Fatal("partial removal not reported")
	}
	saved, _ := Read(m.State)
	if saved.Enabled || saved.ScrawlTarget == "" {
		t.Fatal("partial cleanup lost ownership", saved)
	}
	f.failOff = false
	if err := m.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if saved, _ := Read(m.State); saved != (Config{}) {
		t.Fatal("cleanup did not finish", saved)
	}
}

func TestScrawlOnlyRewritesVerifiedSessionLinks(t *testing.T) {
	m, c, _ := scrawlFixture(t, http.NotFoundHandler())
	m.Commands = &fakeTailscale{}
	u, _ := url.Parse(c.ScrawlTarget)
	for _, host := range []string{"[::1]", "127.0.0.2", "100.64.5.6", "[fd7a:115c:a1e0::123]"} {
		if link, err := m.ScrawlLink(context.Background(), "http://"+host+":"+u.Port()+"/session/one"); err != nil || link != c.scrawlRoute().Origin+"/session/one" {
			t.Fatal(host, link, err)
		}
	}
	if _, err := m.ScrawlLink(context.Background(), "http://100.64.5.7:"+u.Port()+"/session/one"); err == nil {
		t.Fatal("foreign tailnet IP adopted")
	}
	link, err := m.ScrawlLink(context.Background(), c.ScrawlTarget+"/session/existing?mode=notes#answer")
	if err != nil || link != c.scrawlRoute().Origin+"/session/existing?mode=notes#answer" {
		t.Fatal(link, err)
	}
	for _, raw := range []string{"http://example.com:4387/session/x", c.ScrawlTarget + "/api/remote", "http://user@" + strings.TrimPrefix(c.ScrawlTarget, "http://") + "/session/x", "http://127.0.0.1:1/session/x"} {
		if _, err := m.ScrawlLink(context.Background(), raw); err == nil {
			t.Fatal("rewrote unverified link", raw)
		}
	}
}

func TestScrawlEnvironmentKeepsNetworkLocalWithoutGlobalChanges(t *testing.T) {
	got := LocalScrawlEnvironment([]string{"Path=keep", "LAVISH_AXI_HOST=100.64.1.1", "lavish_axi_link_url=https://elsewhere.example", "LAVISH_AXI_ALLOWED_HOSTS=*", "LAVISH_AXI_STATE_DIR=kept"})
	if !slices.Equal(got, []string{"Path=keep", "LAVISH_AXI_STATE_DIR=kept", "LAVISH_AXI_HOST=127.0.0.1"}) {
		t.Fatal(got)
	}
}

func TestScrawlOpaqueSandboxAssetsKeepIdentityAndAPIBoundaries(t *testing.T) {
	m, c, _ := scrawlFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g := NewGate(ctx, m, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("opaque request reached board") }))
	for _, tc := range []struct {
		method, path, origin       string
		board, noIdentity, upgrade bool
		want                       int
	}{
		{"GET", "/assets/fonts/board.woff2", "null", false, false, false, 204},
		{"GET", "/sdk.js", "", false, false, false, 204},
		{"HEAD", "/artifact/key/image.png", "", false, false, false, 204},
		{"GET", "/whiteboard-assets/editor.js", "null", false, false, false, 204},
		{"GET", "/api/poll", "null", false, false, false, 403},
		{"GET", "/session/key", "null", false, false, false, 403},
		{"POST", "/artifact/key/index.html", "null", false, false, false, 403},
		{"GET", "/assets/fonts/a", "null", true, false, false, 403},
		{"GET", "/assets/fonts/a", "null", false, true, false, 403},
		{"GET", "/sdk.js", "null", false, false, true, 403},
		{"GET", "/artifact/../api/poll", "null", false, false, false, 403},
		{"GET", "/sdk.js", "https://foreign.example", false, false, false, 403},
	} {
		r := remoteRequest(tc.method, tc.path)
		if !tc.board {
			r.Host = strings.TrimPrefix(c.scrawlRoute().Origin, "https://")
		}
		if tc.origin == "" {
			r.Header.Del("Origin")
		} else {
			r.Header.Set("Origin", tc.origin)
		}
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		if tc.noIdentity {
			r.Header.Del("Tailscale-User-Login")
		}
		if tc.upgrade {
			r.Header.Set("Upgrade", "websocket")
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("%+v: got %d", tc, w.Code)
		}
	}
}

// A route of the owner's own straight to Scrawl is found however it is
// written, never adopted, and named with the command that removes it, and
// only it, where one does: none for a route inside a Tailscale service or at
// a mount no command can name as it is.
func TestDirectScrawlServeRouteIsNotAdoptedOrCalledPrivate(t *testing.T) {
	m, c, _ := scrawlFixture(t, http.NotFoundHandler())
	forward := strings.TrimPrefix(c.ScrawlTarget, "http://")
	for _, tc := range []struct{ name, config, fix string }{
		{"an HTTPS proxy", `{"TCP":{"4388":{"HTTPS":true}},"Web":{"home.example.ts.net:4388":{"Handlers":{"/":{"Proxy":"` + c.ScrawlTarget + `"}}}}}`, "tailscale serve --https=4388 off"},
		{"an HTTPS proxy at a path", `{"TCP":{"4388":{"HTTPS":true}},"Web":{"home.example.ts.net:4388":{"Handlers":{"/notes":{"Path":"/srv/notes"},"/scrawl/":{"Proxy":"` + c.ScrawlTarget + `"}}}}}`, "tailscale serve --https=4388 --set-path=/scrawl/ off"},
		{"an HTTPS proxy at a path no command can name", `{"TCP":{"4388":{"HTTPS":true}},"Web":{"home.example.ts.net:4388":{"Handlers":{"/a b;exit":{"Proxy":"` + c.ScrawlTarget + `"}}}}}`, ""},
		{"an HTTP proxy", `{"TCP":{"4389":{"HTTP":true}},"Web":{"home.example.ts.net:4389":{"Handlers":{"/":{"Proxy":"` + c.ScrawlTarget + `"}}}}}`, "tailscale serve --http=4389 off"},
		{"a TCP forward", `{"TCP":{"4388":{"TCPForward":"` + forward + `"}}}`, "tailscale serve --tcp=4388 off"},
		{"a TCP forward inside a service", `{"Services":{"svc:test":{"TCP":{"4388":{"TCPForward":"` + forward + `"}}}}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var serving serveConfig
			if err := json.Unmarshal([]byte(tc.config), &serving); err != nil {
				t.Fatal(err)
			}
			if isDirect, fix := directScrawlRoute(serving, c); !isDirect || fix != tc.fix {
				t.Fatalf("direct route %v, fix %q; want found, fix %q", isDirect, fix, tc.fix)
			}
			state, issue, fix := m.scrawlStatus(context.Background(), c, serving)
			if state != "bypassed" || !strings.Contains(issue, "without the owner check") || fix != tc.fix {
				t.Fatal(state, issue, fix)
			}
		})
	}
}

// Scrawl reviews show their state in one word: on, or what is wrong. A
// stopped Scrawl is on, since the next review starts it again, and a route
// of the owner's own straight to Scrawl shows even while reviews are off,
// set up or never set up.
func TestScrawlStateIsOneWordOnOrWhatIsWrong(t *testing.T) {
	const own = `{"TCP":{"4312":{"HTTPS":true}},"Web":{"home.example.ts.net:4312":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:4310"}}}}}`
	direct := func(target string) string {
		return `{"Web":{"home.example.ts.net:4388":{"Handlers":{"/":{"Proxy":"` + target + `"}}}}}`
	}
	for _, tc := range []struct {
		name    string
		arrange func(t *testing.T, c *Config, unsafe *atomic.Bool) string
		want    string
	}{
		{"healthy", func(*testing.T, *Config, *atomic.Bool) string { return own }, "on"},
		{"stopped", func(t *testing.T, c *Config, _ *atomic.Bool) string {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			c.ScrawlTarget = "http://" + listener.Addr().String()
			_ = listener.Close()
			return own
		}, "on"},
		{"another server", func(t *testing.T, c *Config, _ *atomic.Bool) string { c.ScrawlState = t.TempDir(); return own }, "foreign"},
		{"a listener that does not answer as Scrawl", func(t *testing.T, c *Config, _ *atomic.Bool) string {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			go func() {
				for {
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					_ = conn.Close()
				}
			}()
			c.ScrawlTarget = "http://" + listener.Addr().String()
			return own
		}, "foreign"},
		{"listening beyond this PC", func(_ *testing.T, _ *Config, unsafe *atomic.Bool) string { unsafe.Store(true); return own }, "exposed"},
		{"its route missing", func(*testing.T, *Config, *atomic.Bool) string { return "{}" }, "unrouted"},
		{"off", func(_ *testing.T, c *Config, _ *atomic.Bool) string { c.Scrawl = false; return "{}" }, ""},
		{"off beside the owner's direct route", func(_ *testing.T, c *Config, _ *atomic.Bool) string { c.Scrawl = false; return direct(c.ScrawlTarget) }, "bypassed"},
		{"never set up beside the owner's direct route", func(t *testing.T, c *Config, _ *atomic.Bool) string {
			u, _ := url.Parse(c.ScrawlTarget)
			t.Setenv("LAVISH_AXI_PORT", u.Port())
			target := c.ScrawlTarget
			c.Scrawl, c.ScrawlTarget, c.ScrawlState = false, "", ""
			return direct(target)
		}, "bypassed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			m, c, unsafe := scrawlFixture(t, http.NotFoundHandler())
			var serving serveConfig
			if err := json.Unmarshal([]byte(tc.arrange(t, &c, unsafe)), &serving); err != nil {
				t.Fatal(err)
			}

			// Act
			state, issue, _ := m.scrawlStatus(context.Background(), c, serving)

			// Assert
			if state != tc.want || (state == "on") == (issue != "") {
				t.Fatalf("state %q (%q), want %q, with a reason unless it is on", state, issue, tc.want)
			}
		})
	}
}

// Turning Scrawl reviews off removes only Scrawl's own route, keeps the
// board's, and the gate stops passing Scrawl's host at once.
func TestScrawlOffRemovesOnlyItsRouteAndKeepsTheBoards(t *testing.T) {
	// Arrange
	m, c, _ := scrawlFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "review") }))
	u, _ := url.Parse(c.ScrawlTarget)
	t.Setenv("LAVISH_AXI_PORT", u.Port())
	t.Setenv("LAVISH_AXI_STATE_DIR", c.ScrawlState)
	f := &scrawlCommands{fakeTailscale: fakeTailscale{config: ownProxy}, unsafe: &atomic.Bool{}}
	m.Commands, m.Starter = f, f
	if _, err := m.EnableScrawl(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Act
	s, err := m.DisableScrawl(context.Background())

	// Assert
	if err != nil || !s.Enabled || !s.Ready || s.Scrawl || s.ScrawlReady {
		t.Fatal(s, err)
	}
	if saved, err := Read(m.State); err != nil || !saved.Enabled || saved.Scrawl || saved.ScrawlTarget != "" || saved.ScrawlState != "" {
		t.Fatal("Scrawl's ownership not released or the board's lost", saved, err)
	}
	var serving serveConfig
	_ = json.Unmarshal([]byte(f.config), &serving)
	if portOwner(serving, c) != "own" || portOwner(serving, c.scrawlRoute()) != "" {
		t.Fatal("routes after Scrawl off", f.config)
	}
	gate := NewGate(context.Background(), m, http.NotFoundHandler())
	r := httptest.NewRequest("GET", c.scrawlRoute().Origin+"/session/abc", nil)
	r.RemoteAddr = "127.0.0.1:52345"
	r.Header.Set("Tailscale-User-Login", c.Login)
	w := httptest.NewRecorder()
	gate.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("Scrawl's host still passes after Scrawl off: %d", w.Code)
	}
}

// A route on port 4312 that is no longer Scrawl's own is never removed;
// reviews are still off, and Scrawl keeps its ownership record, so the
// uninstall still knows the port it once owned.
func TestScrawlOffLeavesAnotherRouteOnItsPort(t *testing.T) {
	// Arrange
	m, c, _ := scrawlFixture(t, http.NotFoundHandler())
	foreign := `{"TCP":{"4311":{"HTTPS":true},"4312":{"HTTPS":true}},"Web":{"home.example.ts.net:4311":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:4310"}}},"home.example.ts.net:4312":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:9999"}}}}}`
	f := &scrawlCommands{fakeTailscale: fakeTailscale{config: foreign}, unsafe: &atomic.Bool{}}
	m.Commands = f

	// Act
	_, err := m.DisableScrawl(context.Background())

	// Assert
	if err == nil || !strings.Contains(err.Error(), "left unchanged") {
		t.Fatalf("another route on 4312: %v", err)
	}
	if f.config != foreign {
		t.Fatal("another route was changed", f.config)
	}
	if saved, _ := Read(m.State); saved.Scrawl || saved.ScrawlTarget != c.ScrawlTarget {
		t.Fatal("reviews not off or ownership lost", saved)
	}
}

// A Scrawl off that cannot finish leaves reviews off for phones and keeps
// Scrawl's ownership record, so a later off or the uninstall can still
// remove exactly its route.
func TestScrawlOffThatCannotFinishKeepsItsRecord(t *testing.T) {
	const both = `{"TCP":{"4311":{"HTTPS":true},"4312":{"HTTPS":true}},"Web":{"home.example.ts.net:4311":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:4310"}}},"home.example.ts.net:4312":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:4310"}}}}}`
	for _, tc := range []struct {
		name     string
		commands *scrawlCommands
		want     string
	}{
		{"Tailscale unreadable", &scrawlCommands{fakeTailscale: fakeTailscale{config: both, fail: true}}, "needs Tailscale"},
		{"its off refused", &scrawlCommands{fakeTailscale: fakeTailscale{config: both}, failOff: true}, "could not be removed"},
		{"its route still there after its off", &scrawlCommands{fakeTailscale: fakeTailscale{config: both}, isOffIgnored: true}, "still reports port 4312"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			m, c, _ := scrawlFixture(t, http.NotFoundHandler())
			tc.commands.unsafe = &atomic.Bool{}
			m.Commands = tc.commands

			// Act
			_, err := m.DisableScrawl(context.Background())

			// Assert
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("Scrawl off: %v, want it to say %q", err, tc.want)
			}
			if saved, err := Read(m.State); err != nil || saved.Scrawl || !saved.Enabled || saved.ScrawlTarget != c.ScrawlTarget || saved.ScrawlState != c.ScrawlState {
				t.Fatal("reviews not off, the board's access lost, or Scrawl's record lost", saved, err)
			}
		})
	}
}

// With no Scrawl ever set up, turning reviews off asks Tailscale nothing.
func TestScrawlOffWithNothingSetUpChangesNothing(t *testing.T) {
	// Arrange
	f := &fakeTailscale{config: ownProxy}
	m := &Manager{State: t.TempDir(), Target: testConfig().Target, Commands: f}
	if err := write(m.State, testConfig()); err != nil {
		t.Fatal(err)
	}

	// Act
	s, err := m.DisableScrawl(context.Background())

	// Assert
	if err != nil || !s.Enabled || s.Scrawl {
		t.Fatal(s, err)
	}
	if slices.ContainsFunc(f.calls, func(call string) bool { return strings.HasSuffix(call, " off") }) {
		t.Fatal("removed a route while nothing was set up", f.calls)
	}
}

// A phone loses Scrawl reviews the moment they turn off, before their route
// is gone: the gate reads the record at each request.
func TestScrawlOffRefusesPhonesBeforeItsRouteIsGone(t *testing.T) {
	// Arrange
	m, c, _ := scrawlFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "review") }))
	const both = `{"TCP":{"4311":{"HTTPS":true},"4312":{"HTTPS":true}},"Web":{"home.example.ts.net:4311":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:4310"}}},"home.example.ts.net:4312":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:4310"}}}}}`
	f := &scrawlCommands{fakeTailscale: fakeTailscale{config: both}, unsafe: &atomic.Bool{}, offStarted: make(chan struct{}), offRelease: make(chan struct{})}
	m.Commands = f
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gate := NewGate(ctx, m, http.NotFoundHandler())
	review := func() int {
		r := httptest.NewRequest("GET", c.scrawlRoute().Origin+"/session/abc", nil)
		r.RemoteAddr = "127.0.0.1:52345"
		r.Header.Set("Tailscale-User-Login", c.Login)
		w := httptest.NewRecorder()
		gate.ServeHTTP(w, r)
		return w.Code
	}
	if code := review(); code != http.StatusOK {
		t.Fatalf("a review before Scrawl off: %d", code)
	}
	done := make(chan error, 1)

	// Act
	go func() { _, err := m.DisableScrawl(context.Background()); done <- err }()
	<-f.offStarted
	during := review()
	close(f.offRelease)

	// Assert
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if during != http.StatusForbidden {
		t.Fatalf("a review while Scrawl's route was still up: %d, want it refused", during)
	}
}
