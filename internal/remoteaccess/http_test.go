package remoteaccess

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func testConfig() Config {
	return Config{Enabled: true, Origin: "https://home.example.ts.net:4311", Login: "owner@example.com", Target: "http://127.0.0.1:4310"}
}

func remoteRequest(method, path string) *http.Request {
	c := testConfig()
	r := httptest.NewRequest(method, c.Origin+path, nil)
	r.RemoteAddr = "127.0.0.1:52345"
	r.Header.Set("Origin", c.Origin)
	r.Header.Set("Tailscale-User-Login", c.Login)
	r.Header.Set("X-Forwarded-For", "100.64.0.7")
	return r
}

func testGate(t *testing.T, next http.Handler) (*Gate, *Manager) {
	t.Helper()
	c := testConfig()
	m := &Manager{State: t.TempDir(), Target: c.Target}
	if err := write(m.State, c); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return NewGate(ctx, m, next), m
}

func TestGateAuthenticatesEveryRequestBeforeTheBoard(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*http.Request)
		allowed bool
	}{
		{"owner", func(r *http.Request) {}, true},
		{"first Safari navigation", func(r *http.Request) { r.Header.Del("Origin") }, true},
		{"wrong owner", func(r *http.Request) { r.Header.Set("Tailscale-User-Login", "guest@example.com") }, false},
		{"missing owner including Funnel", func(r *http.Request) { r.Header.Del("Tailscale-User-Login") }, false},
		{"duplicate owner", func(r *http.Request) { r.Header.Add("Tailscale-User-Login", testConfig().Login) }, false},
		{"non-loopback peer", func(r *http.Request) { r.RemoteAddr = "100.64.0.7:4321" }, false},
		{"wrong host", func(r *http.Request) { r.Host = "other.example.ts.net:4311" }, false},
		{"local host with forwarded identity", func(r *http.Request) { r.Host = "127.0.0.1:4310"; r.Header.Set("Origin", testConfig().Target) }, false},
		{"local host Funnel proxy without identity", func(r *http.Request) {
			r.Host = "127.0.0.1:4310"
			r.Header.Del("Tailscale-User-Login")
			r.Header.Del("Origin")
		}, false},
		{"cross-origin", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, false},
		{"duplicate Origin", func(r *http.Request) { r.Header.Add("Origin", testConfig().Origin) }, false},
		{"cross-site", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, false},
		{"POST needs origin", func(r *http.Request) { r.Method = "POST"; r.Header.Del("Origin") }, false},
		{"websocket needs origin", func(r *http.Request) { r.Header.Set("Upgrade", "websocket"); r.Header.Del("Origin") }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			g, _ := testGate(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if r.Host != "127.0.0.1:4310" {
					t.Error("Host not translated")
				}
				if origin := r.Header.Get("Origin"); origin != "" && origin != testConfig().Target {
					t.Error("Origin not translated")
				}
				if r.Header.Get("Tailscale-User-Login") == "" || r.Header.Get("X-Forwarded-For") == "" {
					t.Error("proxy markers lost: PC-only operations would be exposed")
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			r := remoteRequest("GET", "/api/snapshot")
			tc.mutate(r)
			w := httptest.NewRecorder()
			g.ServeHTTP(w, r)
			if called != tc.allowed {
				t.Fatalf("reached board=%v; status=%d", called, w.Code)
			}
			if !tc.allowed && w.Code != http.StatusForbidden {
				t.Fatal(w.Code)
			}
		})
	}
}

// The gate marks what it admitted as the owner's, through his private
// connection, where no client can reach: in the request's context. The
// board's own page on this PC passes unmarked.
func TestTheGateMarksOnlyWhatItAdmittedAsTheOwners(t *testing.T) {
	for _, tc := range []struct {
		name       string
		request    func() *http.Request
		isAdmitted bool
	}{
		{"the owner through his private connection", func() *http.Request { return remoteRequest("POST", "/api/afk") }, true},
		{"the board's own page on this PC", func() *http.Request {
			r := httptest.NewRequest("POST", testConfig().Target+"/api/afk", nil)
			r.RemoteAddr = "127.0.0.1:52345"
			return r
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			reached, isAdmitted := false, false
			g, _ := testGate(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached, isAdmitted = true, Admitted(r)
			}))

			// Act
			g.ServeHTTP(httptest.NewRecorder(), tc.request())

			// Assert
			if !reached || isAdmitted != tc.isAdmitted {
				t.Errorf("reached the board %t, admitted %t, want it reached and admitted %t", reached, isAdmitted, tc.isAdmitted)
			}
		})
	}
}

func TestGateKeepsMutationTokensAndRechecksPersistentRevocation(t *testing.T) {
	g, m := testGate(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The real board still performs this check after translation.
		if r.Header.Get("X-CFO-Token") != "instance" {
			http.Error(w, "token", 403)
			return
		}
		w.WriteHeader(204)
	}))
	for _, token := range []string{"", "instance"} {
		r := remoteRequest("POST", "/api/actions")
		r.Header.Set("X-CFO-Token", token)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if (w.Code == 204) != (token == "instance") {
			t.Fatalf("token %q status %d", token, w.Code)
		}
	}
	c := testConfig()
	c.Enabled = false
	if err := write(m.State, c); err != nil {
		t.Fatal(err)
	}
	r := remoteRequest("GET", "/")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("disabled gate still serves", w.Code)
	}
	if err := os.WriteFile(filepath.Join(m.State, configFile), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("unreadable settings still serve", w.Code)
	}
}

func TestOffCancelsRemoteStreamsButNeverLocalRequests(t *testing.T) {
	remoteStarted, remoteEnded := make(chan struct{}), make(chan struct{})
	localStarted, localEnded := make(chan struct{}), make(chan struct{})
	g, m := testGate(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Tailscale-User-Login") != "" {
			close(remoteStarted)
			<-r.Context().Done()
			close(remoteEnded)
		} else {
			close(localStarted)
			<-r.Context().Done()
			close(localEnded)
		}
	}))
	go g.ServeHTTP(httptest.NewRecorder(), remoteRequest("GET", "/api/events"))
	local, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("GET", testConfig().Target+"/api/events", nil).WithContext(local)
	go g.ServeHTTP(httptest.NewRecorder(), r)
	<-remoteStarted
	<-localStarted
	c := testConfig()
	c.Enabled = false
	if err := write(m.State, c); err != nil {
		t.Fatal(err)
	}
	select {
	case <-remoteEnded:
	case <-time.After(time.Minute):
		t.Fatal("remote stream kept running")
	}
	select {
	case <-localEnded:
		t.Fatal("local stream was canceled")
	default:
	}
	cancel()
	<-localEnded
}

func TestARequestThatEnablesAccessDoesNotOwnLaterSessions(t *testing.T) {
	var canceled atomic.Bool
	g, m := testGate(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Err() != nil {
			canceled.Store(true)
		}
		w.WriteHeader(204)
	}))
	c := testConfig()
	c.Enabled = false
	_ = write(m.State, c)
	g.refresh()
	c.Enabled = true
	_ = write(m.State, c)
	ctx, cancel := context.WithCancel(context.Background())
	g.ServeHTTP(httptest.NewRecorder(), remoteRequest("GET", "/").WithContext(ctx))
	cancel()
	w := httptest.NewRecorder()
	g.ServeHTTP(w, remoteRequest("GET", "/api/snapshot"))
	if canceled.Load() || w.Code != 204 {
		t.Fatal("first request ending revoked later remote requests", w.Code)
	}
}

func TestAuthenticatedWebsocketRoundTripAndRevocation(t *testing.T) {
	const token = "instance-secret"
	g, m := testGate(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != testConfig().Target || r.URL.Query().Get("token") != token {
			http.Error(w, "forbidden", 403)
			return
		}
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
			if err := conn.Write(r.Context(), kind, data); err != nil {
				return
			}
		}
	}))
	// Stand-in for Tailscale Serve: it supplies identity, preserves the public
	// Host, and originates the backend TCP connection on loopback.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = strings.TrimPrefix(testConfig().Origin, "https://")
		r.Header.Set("Tailscale-User-Login", testConfig().Login)
		g.ServeHTTP(w, r)
	}))
	defer proxy.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	dial := func(token string) (*websocket.Conn, *http.Response, error) {
		return websocket.Dial(ctx, "ws"+strings.TrimPrefix(proxy.URL, "http")+"/api/terminal/native?token="+token, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{testConfig().Origin}}})
	}
	bad, response, err := dial("bad")
	if bad != nil {
		bad.CloseNow()
	}
	if err == nil || response == nil || response.StatusCode != 403 {
		t.Fatal("missing mutation token upgraded")
	}
	conn, _, err := dial(token)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("continue\r")); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil || string(data) != "continue\r" {
		t.Fatalf("terminal input round trip: %q %v", data, err)
	}
	c := testConfig()
	c.Enabled = false
	_ = write(m.State, c)
	_, _, err = conn.Read(ctx)
	if err == nil || ctx.Err() != nil {
		t.Fatal("remote off did not close websocket before timeout", err)
	}
}

func TestGateDisabledByDefaultDoesNotTouchLocalBoard(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &Manager{State: t.TempDir(), Target: testConfig().Target}
	g := NewGate(ctx, m, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "local") }))
	local := httptest.NewRecorder()
	g.ServeHTTP(local, httptest.NewRequest("GET", m.Target+"/", nil))
	remote := httptest.NewRecorder()
	g.ServeHTTP(remote, remoteRequest("GET", "/"))
	if local.Body.String() != "local" || remote.Code != 403 {
		t.Fatal("default boundaries failed")
	}
}
