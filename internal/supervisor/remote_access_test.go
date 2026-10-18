package supervisor

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/remoteaccess"
)

const boardRoute = `{"TCP":{"4311":{"HTTPS":true}},"Web":{"pc.example.ts.net:4311":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:4310"}}}}}`

type remoteCommands struct {
	config string
	slow   bool
}

func (c *remoteCommands) Run(ctx context.Context, r execx.Request) (execx.Result, error) {
	switch strings.Join(r.Args, " ") {
	case "status --json":
		return execx.Result{Stdout: []byte(`{"BackendState":"Running","Self":{"DNSName":"pc.example.ts.net.","UserID":7},"Peer":{"k1":{"DNSName":"laptop.example.ts.net.","UserID":7,"Online":true}},"User":{"7":{"LoginName":"owner@example.com"}}}`)}, nil
	case "serve status --json":
		if c.slow {
			select {
			case <-ctx.Done():
				return execx.Result{}, ctx.Err()
			case <-time.After(1200 * time.Millisecond): // stall-accepted: the stand-in's slow step, which a stall can only end before the revocation lands, and a cleanup that finishes passes either way.
			}
		}
		if c.config == "" {
			return execx.Result{Stdout: []byte("{}")}, nil
		}
		return execx.Result{Stdout: []byte(c.config)}, nil
	case "serve --bg --https=4311 http://127.0.0.1:4310":
		c.config = `{"TCP":{"4311":{"HTTPS":true}},"Web":{"pc.example.ts.net:4311":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:4310"}}}}}`
	case "serve --https=4311 off":
		c.config = "{}"
	case "serve --https=4312 off":
		c.config = boardRoute
	default:
		return execx.Result{ExitCode: 1}, nil
	}
	return execx.Result{}, nil
}

func TestRemoteBoardPreservesTokenAndPCOnlySettingsChecks(t *testing.T) {
	store, h := testStore(t)
	manager := &remoteaccess.Manager{State: h.State, Target: "http://127.0.0.1:4310", Commands: &remoteCommands{}}
	if _, err := manager.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	service := &Service{Store: store, Instance: "instance", Options: Options{RemoteAccess: manager}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gate := remoteaccess.NewGate(ctx, manager, NewHTTP(service, "127.0.0.1:4310", nil))
	for _, tc := range []struct {
		method, body, token string
		want                int
	}{
		{"GET", "", "", 200},
		{"POST", `{"on":true}`, "", 403},
		{"POST", `{"on":true}`, "instance", 403},
	} {
		r := httptest.NewRequest(tc.method, "https://pc.example.ts.net:4311/api/remote", strings.NewReader(tc.body))
		r.RemoteAddr = "127.0.0.1:55432"
		r.Header.Set("Origin", "https://pc.example.ts.net:4311")
		r.Header.Set("Tailscale-User-Login", "owner@example.com")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CFO-Token", tc.token)
		w := httptest.NewRecorder()
		gate.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s token=%q: %d %s", tc.method, tc.token, w.Code, w.Body.String())
		}
	}
}

func TestRemoteOffFinishesProxyCleanupAfterItsRequestIsRevoked(t *testing.T) {
	store, h := testStore(t)
	commands := &remoteCommands{}
	manager := &remoteaccess.Manager{State: h.State, Target: "http://127.0.0.1:4310", Commands: commands}
	if _, err := manager.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	commands.slow = true
	service := &Service{Store: store, Instance: "instance", Options: Options{RemoteAccess: manager}, subscribers: map[chan struct{}]struct{}{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gate := remoteaccess.NewGate(ctx, manager, NewHTTP(service, "127.0.0.1:4310", nil))
	r := httptest.NewRequest("POST", "https://pc.example.ts.net:4311/api/remote", strings.NewReader(`{"on":false}`))
	r.RemoteAddr = "127.0.0.1:55432"
	r.Header.Set("Origin", "https://pc.example.ts.net:4311")
	r.Header.Set("Tailscale-User-Login", "owner@example.com")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CFO-Token", "instance")
	w := httptest.NewRecorder()
	gate.ServeHTTP(w, r)
	if w.Code != 200 || commands.config != "{}" {
		t.Fatalf("cleanup interrupted by revocation: %d %s config=%s", w.Code, w.Body.String(), commands.config)
	}
}

// Scrawl reviews have their own switch: a phone may turn them off, as it may
// turn remote access off, but only the board on this PC turns them on, and a
// request names one switch.
func TestScrawlSwitchTurnsOffFromAPhoneAndOnOnlyOnThisPC(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	manager := &remoteaccess.Manager{State: h.State, Target: "http://127.0.0.1:4310", Commands: &remoteCommands{}}
	if _, err := manager.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	service := &Service{Store: store, Instance: "instance", Options: Options{RemoteAccess: manager}, subscribers: map[chan struct{}]struct{}{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gate := remoteaccess.NewGate(ctx, manager, NewHTTP(service, "127.0.0.1:4310", nil))
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"scrawl":true}`, 403},
		{`{"scrawl":false}`, 200},
		{`{"on":true,"scrawl":false}`, 400},
		{`{}`, 400},
	} {
		r := httptest.NewRequest("POST", "https://pc.example.ts.net:4311/api/remote", strings.NewReader(tc.body))
		r.RemoteAddr = "127.0.0.1:55432"
		r.Header.Set("Origin", "https://pc.example.ts.net:4311")
		r.Header.Set("Tailscale-User-Login", "owner@example.com")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CFO-Token", "instance")
		w := httptest.NewRecorder()

		// Act
		gate.ServeHTTP(w, r)

		// Assert
		if w.Code != tc.want {
			t.Fatalf("%s from a phone: %d %s, want %d", tc.body, w.Code, w.Body.String(), tc.want)
		}
	}
}

// Scrawl off from a phone revokes that phone's own request with the rest,
// and still finishes removing Scrawl's route after it, as remote access off
// does.
func TestScrawlOffFromAPhoneFinishesAfterItsRequestIsRevoked(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	commands := &remoteCommands{config: `{"TCP":{"4311":{"HTTPS":true},"4312":{"HTTPS":true}},"Web":{"pc.example.ts.net:4311":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:4310"}}},"pc.example.ts.net:4312":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:4310"}}}}}`}
	manager := &remoteaccess.Manager{State: h.State, Target: "http://127.0.0.1:4310", Commands: commands}
	settings := `{"enabled":true,"origin":"https://pc.example.ts.net:4311","login":"owner@example.com","target":"http://127.0.0.1:4310","scrawl":true,"scrawl_target":"http://127.0.0.1:4387","scrawl_state":` + strconv.Quote(t.TempDir()) + `}`
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.State, "remote-access.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	commands.slow = true
	service := &Service{Store: store, Instance: "instance", Options: Options{RemoteAccess: manager}, subscribers: map[chan struct{}]struct{}{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gate := remoteaccess.NewGate(ctx, manager, NewHTTP(service, "127.0.0.1:4310", nil))
	r := httptest.NewRequest("POST", "https://pc.example.ts.net:4311/api/remote", strings.NewReader(`{"scrawl":false}`))
	r.RemoteAddr = "127.0.0.1:55432"
	r.Header.Set("Origin", "https://pc.example.ts.net:4311")
	r.Header.Set("Tailscale-User-Login", "owner@example.com")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CFO-Token", "instance")
	w := httptest.NewRecorder()

	// Act
	gate.ServeHTTP(w, r)

	// Assert
	if w.Code != 200 || commands.config != boardRoute {
		t.Fatalf("Scrawl off interrupted by its own revocation: %d %s config=%s", w.Code, w.Body.String(), commands.config)
	}
	if saved, err := remoteaccess.Read(h.State); err != nil || !saved.Enabled || saved.Scrawl || saved.ScrawlTarget != "" {
		t.Fatal("Scrawl's record not released or the board's lost", saved, err)
	}
}

// The setting lists the machines whose boards answer on the owner's tailnet,
// this PC first, as a phone reads it too; reading it changes nothing.
func TestThePhoneReadsTheMachinesItCanConnectTo(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	manager := &remoteaccess.Manager{State: h.State, Target: "http://127.0.0.1:4310", Commands: &remoteCommands{}, Answers: func(_ context.Context, origin string) bool { return origin == "https://laptop.example.ts.net:4311" }}
	if _, err := manager.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	service := &Service{Store: store, Instance: "instance", Options: Options{RemoteAccess: manager}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gate := remoteaccess.NewGate(ctx, manager, NewHTTP(service, "127.0.0.1:4310", nil))
	r := httptest.NewRequest("GET", "https://pc.example.ts.net:4311/api/remote/machines", nil)
	r.RemoteAddr = "127.0.0.1:55432"
	r.Header.Set("Tailscale-User-Login", "owner@example.com")
	w := httptest.NewRecorder()

	// Act
	gate.ServeHTTP(w, r)

	// Assert
	want := `{"machines":[{"name":"pc","url":"https://pc.example.ts.net:4311","is_this":true},{"name":"laptop","url":"https://laptop.example.ts.net:4311","is_this":false}]}`
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != want {
		t.Errorf("GET /api/remote/machines = %d %s, want %s", w.Code, w.Body, want)
	}
}
