package supervisor

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/remoteaccess"
)

type remoteCommands struct {
	config string
	slow   bool
}

func (c *remoteCommands) Run(ctx context.Context, r execx.Request) (execx.Result, error) {
	switch strings.Join(r.Args, " ") {
	case "status --json":
		return execx.Result{Stdout: []byte(`{"BackendState":"Running","Self":{"DNSName":"pc.example.ts.net.","UserID":7},"User":{"7":{"LoginName":"owner@example.com"}}}`)}, nil
	case "serve status --json":
		if c.slow {
			select {
			case <-ctx.Done():
				return execx.Result{}, ctx.Err()
			case <-time.After(1200 * time.Millisecond):
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
