package supervisor

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/remoteaccess"
)

// switchedTailscale is this PC's Tailscale, which its own client stops and
// starts, which the network going away leaves unconnected, and whose Serve
// keeps a route per port. The board and cfo remote share it, as the one
// Tailscale on the PC.
type switchedTailscale struct {
	mu                   sync.Mutex
	isStopped, isOffline bool
	routes               map[string]string
}

func (f *switchedTailscale) Run(_ context.Context, r execx.Request) (execx.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := strings.Join(r.Args, " ")
	if f.isStopped {
		return execx.Result{ExitCode: 1}, nil
	}
	switch {
	case call == "status --json" && f.isOffline:
		return execx.Result{Stdout: []byte(`{"BackendState":"Starting"}`)}, nil
	case call == "status --json":
		return execx.Result{Stdout: []byte(`{"BackendState":"Running","CertDomains":["pc.example.ts.net"],"Self":{"DNSName":"pc.example.ts.net.","UserID":7},"User":{"7":{"LoginName":"owner@example.com"}}}`)}, nil
	case call == "serve status --json":
		config := map[string]map[string]any{"TCP": {}, "Web": {}}
		for port, target := range f.routes {
			config["TCP"][port] = map[string]bool{"HTTPS": true}
			config["Web"]["pc.example.ts.net:"+port] = map[string]any{"Handlers": map[string]any{"/": map[string]string{"Proxy": target}}}
		}
		data, err := json.Marshal(config)
		return execx.Result{Stdout: data}, err
	case strings.HasPrefix(call, "serve --bg --https="):
		port, target, _ := strings.Cut(strings.TrimPrefix(call, "serve --bg --https="), " ")
		f.routes[port] = target
	case strings.HasPrefix(call, "serve --https=") && strings.HasSuffix(call, " off"):
		delete(f.routes, strings.TrimSuffix(strings.TrimPrefix(call, "serve --https="), " off"))
	default:
		return execx.Result{ExitCode: 1}, nil
	}
	return execx.Result{}, nil
}

func (f *switchedTailscale) set(isStopped, isOffline bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.isStopped, f.isOffline = isStopped, isOffline
}

// The Overlord, 2026-10-27: "Make sure that whatever on-and-off detections
// are detected, that the setting is reflected, and that there's no errors in
// the board." Remote access is turned on and off from every side, with the
// board's setting read the whole time as an open board reads it, and after
// each change the board answers and its setting says what is so: on or off,
// and while on whether Tailscale works, with no issue once it does, a
// restart that brings the board back at another address included.
func TestEveryWayRemoteAccessTurnsOnOrOffShowsInTheBoardsSetting(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	tailscale := &switchedTailscale{routes: map[string]string{}}
	board := func(address string) (*remoteaccess.Manager, *remoteaccess.Gate, context.CancelFunc) {
		manager := &remoteaccess.Manager{State: h.State, Target: "http://" + address, Commands: tailscale}
		service := &Service{Store: store, Instance: "instance", Options: Options{RemoteAccess: manager}, subscribers: map[chan struct{}]struct{}{}}
		ctx, cancel := context.WithCancel(context.Background())
		return manager, remoteaccess.NewGate(ctx, manager, NewHTTP(service, address, nil)), cancel
	}
	// running guards the board that runs now, which a restart replaces.
	var running sync.Mutex
	address := "127.0.0.1:4310"
	manager, gate, stop := board(address)
	t.Cleanup(func() { stop() })
	ask := func(method, body string) (int, remoteaccess.Status) {
		running.Lock()
		address, gate := address, gate
		running.Unlock()
		r := httptest.NewRequest(method, "http://"+address+"/api/remote", strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:50000"
		r.Header.Set("Origin", "http://"+address)
		r.Header.Set("X-CFO-Token", "instance")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		gate.ServeHTTP(w, r)
		var status remoteaccess.Status
		_ = json.Unmarshal(w.Body.Bytes(), &status)
		return w.Code, status
	}
	// Another open board reads the setting throughout, as the setting does
	// every few seconds, so each change lands among reads.
	reading, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-reading:
				return
			default:
				ask("GET", "")
			}
		}
	}()
	cli := &remoteaccess.Manager{State: h.State, Target: "http://" + address, Commands: tailscale}
	for _, step := range []struct {
		name               string
		change             func(t *testing.T)
		isOn, isReady      bool
		attention, routeTo string
	}{
		{name: "the setting's switch on", change: func(t *testing.T) {
			if code, _ := ask("POST", `{"on":true}`); code != 200 {
				t.Fatalf("on answered %d", code)
			}
		}, isOn: true, isReady: true, routeTo: "http://127.0.0.1:4310"},
		{name: "Tailscale stopped by its own client", change: func(*testing.T) { tailscale.set(true, false) }, isOn: true, attention: "stopped", routeTo: "http://127.0.0.1:4310"},
		{name: "Tailscale started again", change: func(*testing.T) { tailscale.set(false, false) }, isOn: true, isReady: true, routeTo: "http://127.0.0.1:4310"},
		{name: "the network going away", change: func(*testing.T) { tailscale.set(false, true) }, isOn: true, attention: "disconnected", routeTo: "http://127.0.0.1:4310"},
		{name: "the network coming back", change: func(*testing.T) { tailscale.set(false, false) }, isOn: true, isReady: true, routeTo: "http://127.0.0.1:4310"},
		{name: "the setting's switch off", change: func(t *testing.T) {
			if code, _ := ask("POST", `{"on":false}`); code != 200 {
				t.Fatalf("off answered %d", code)
			}
		}},
		{name: "cfo remote on", change: func(t *testing.T) {
			if _, err := cli.Enable(context.Background()); err != nil {
				t.Fatal(err)
			}
		}, isOn: true, isReady: true, routeTo: "http://127.0.0.1:4310"},
		{name: "a restart that brings the board back at its address", change: func(*testing.T) {
			running.Lock()
			stop()
			manager, gate, stop = board(address)
			running.Unlock()
		}, isOn: true, isReady: true, routeTo: "http://127.0.0.1:4310"},
		{name: "a restart that brings the board back at another address", change: func(t *testing.T) {
			running.Lock()
			stop()
			address = "127.0.0.1:64610"
			manager, gate, stop = board(address)
			running.Unlock()
			if moved, err := manager.Follow(context.Background()); err != nil || !moved {
				t.Fatalf("Follow = %v, %v", moved, err)
			}
		}, isOn: true, isReady: true, routeTo: "http://127.0.0.1:64610"},
		{name: "cfo remote off", change: func(t *testing.T) {
			cli.Target = "http://127.0.0.1:64610"
			if err := cli.Disable(context.Background()); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		// Act
		step.change(t)

		// Assert
		code, status := ask("GET", "")
		if code != 200 {
			t.Fatalf("after %s the board answered %d", step.name, code)
		}
		if status.Enabled != step.isOn || status.Ready != step.isReady || status.Attention != step.attention {
			t.Errorf("after %s the setting reads %+v; want on %v, ready %v, attention %q", step.name, status, step.isOn, step.isReady, step.attention)
		}
		if step.isReady && status.Issue != "" {
			t.Errorf("after %s the setting names an issue: %s", step.name, status.Issue)
		}
		tailscale.mu.Lock()
		route := tailscale.routes["4311"]
		tailscale.mu.Unlock()
		if route != step.routeTo {
			t.Errorf("after %s Tailscale routes port 4311 to %q, want %q", step.name, route, step.routeTo)
		}
	}
	close(reading)
	<-done
}
