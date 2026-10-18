package remoteaccess

import (
	"context"
	"encoding/json"
	"io"
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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

func TestDirectScrawlServeRouteIsNotAdoptedOrCalledPrivate(t *testing.T) {
	m, c, _ := scrawlFixture(t, http.NotFoundHandler())
	var serving serveConfig
	_ = json.Unmarshal([]byte(`{"Web":{"home.example.ts.net:4388":{"Handlers":{"/":{"Proxy":"`+c.ScrawlTarget+`"}}}}}`), &serving)
	for _, raw := range []string{`{"TCP":{"4388":{"TCPForward":"` + strings.TrimPrefix(c.ScrawlTarget, "http://") + `"}}}`, `{"Services":{"svc:test":{"TCP":{"4388":{"TCPForward":"` + strings.TrimPrefix(c.ScrawlTarget, "http://") + `"}}}}}`} {
		var forwarding serveConfig
		_ = json.Unmarshal([]byte(raw), &forwarding)
		if !directScrawlProxy(forwarding, c) {
			t.Fatal("TCP bypass missed")
		}
	}
	if !directScrawlProxy(serving, c) {
		t.Fatal("direct proxy missed")
	}
	ready, issue := m.scrawlStatus(context.Background(), c, serving)
	if ready || !strings.Contains(issue, "bypasses") {
		t.Fatal(ready, issue)
	}
}
