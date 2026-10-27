package remoteaccess

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// routedTailscale is a Tailscale whose Serve keeps a route per port, which
// serve --bg sets and serve off removes, and which can be stopped, have
// Funnel on for port 4311, or refuse serve --bg on one port.
type routedTailscale struct {
	isStopped bool
	isFunnel  bool
	failing   string
	routes    map[string]string
	calls     []string
}

func (f *routedTailscale) Run(_ context.Context, r execx.Request) (execx.Result, error) {
	call := strings.Join(r.Args, " ")
	f.calls = append(f.calls, call)
	if f.isStopped {
		return execx.Result{ExitCode: 1}, nil
	}
	switch {
	case call == "status --json":
		return execx.Result{Stdout: []byte(`{"BackendState":"Running","CertDomains":["home.example.ts.net"],"Self":{"DNSName":"home.example.ts.net.","UserID":123},"User":{"123":{"LoginName":"owner@example.com"}}}`)}, nil
	case call == "serve status --json":
		config := map[string]map[string]any{"TCP": {}, "Web": {}, "AllowFunnel": {"home.example.ts.net:4311": f.isFunnel}}
		for port, target := range f.routes {
			config["TCP"][port] = map[string]bool{"HTTPS": true}
			config["Web"]["home.example.ts.net:"+port] = map[string]any{"Handlers": map[string]any{"/": map[string]string{"Proxy": target}}}
		}
		data, err := json.Marshal(config)
		return execx.Result{Stdout: data}, err
	case strings.HasPrefix(call, "serve --bg --https="):
		port, target, _ := strings.Cut(strings.TrimPrefix(call, "serve --bg --https="), " ")
		if port == f.failing {
			return execx.Result{ExitCode: 1}, nil
		}
		f.routes[port] = target
	case strings.HasPrefix(call, "serve --https=") && strings.HasSuffix(call, " off"):
		delete(f.routes, strings.TrimSuffix(strings.TrimPrefix(call, "serve --https="), " off"))
	default:
		return execx.Result{ExitCode: 1}, nil
	}
	return execx.Result{}, nil
}

func writeSettings(t *testing.T, state string, c Config) {
	t.Helper()
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, configFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A restart that brings the board back on another address, as one that finds
// the usual address taken does, leaves remote access on for the old one. The
// board that came back moves this home's routes, Scrawl's with the board's,
// and the setting shows remote access ready with no "Board moved".
func TestARestartedBoardOnAnotherAddressTakesRemoteAccessWithIt(t *testing.T) {
	for _, test := range []struct {
		name     string
		isScrawl bool
	}{
		{name: "the board alone"},
		{name: "with Scrawl reviews", isScrawl: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			const before, after = "http://127.0.0.1:4310", "http://127.0.0.1:64610"
			state := t.TempDir()
			tailscale := &routedTailscale{routes: map[string]string{"4311": before}}
			settings := Config{Enabled: true, Origin: "https://home.example.ts.net:4311", Login: "owner@example.com", Target: before}
			if test.isScrawl {
				tailscale.routes["4312"] = before
				settings.Scrawl, settings.ScrawlTarget, settings.ScrawlState = true, "http://127.0.0.1:4387", t.TempDir()
			}
			writeSettings(t, state, settings)
			m := &Manager{State: state, Target: after, Commands: tailscale}
			if s := m.Status(context.Background()); s.Attention != "board_moved" {
				t.Fatalf("before the move Status = %+v, want board_moved", s)
			}

			// Act
			moved, err := m.Follow(context.Background())

			// Assert
			if err != nil || !moved {
				t.Fatalf("Follow = %v, %v; want moved", moved, err)
			}
			if tailscale.routes["4311"] != after || test.isScrawl && tailscale.routes["4312"] != after {
				t.Errorf("routes = %v, want each at %s", tailscale.routes, after)
			}
			saved, err := Read(state)
			settings.Target = after
			if err != nil || saved != settings {
				t.Errorf("settings = %+v, %v; want %+v", saved, err, settings)
			}
			if s := m.Status(context.Background()); !s.Ready || s.Issue != "" || s.Attention != "" {
				t.Errorf("after the move Status = %+v, want ready with no issue", s)
			}
		})
	}
}

// Follow moves only what is this home's, and nothing unless it can move all
// of it: remote access that is off, already at this board, signed in to
// another account, routed by someone else, Scrawl's route routed by someone
// else, public through Funnel, or waiting on a stopped Tailscale is left as
// it is, for Status to name.
func TestFollowMovesNoRouteThatIsNotThisHomesToMove(t *testing.T) {
	const before, after = "http://127.0.0.1:4310", "http://127.0.0.1:64610"
	on := Config{Enabled: true, Origin: "https://home.example.ts.net:4311", Login: "owner@example.com", Target: before}
	withScrawl := on
	withScrawl.Scrawl, withScrawl.ScrawlTarget, withScrawl.ScrawlState = true, "http://127.0.0.1:4387", t.TempDir()
	for _, test := range []struct {
		name      string
		settings  Config
		routes    map[string]string
		isStopped bool
		isFunnel  bool
		isError   bool
	}{
		{name: "remote access off", settings: Config{Origin: on.Origin, Login: on.Login, Target: before}, routes: map[string]string{"4311": before}},
		{name: "already at this board", settings: Config{Enabled: true, Origin: on.Origin, Login: on.Login, Target: after}, routes: map[string]string{"4311": after}},
		{name: "another account", settings: Config{Enabled: true, Origin: on.Origin, Login: "someone@example.com", Target: before}, routes: map[string]string{"4311": before}},
		{name: "a route someone else changed", settings: on, routes: map[string]string{"4311": "http://127.0.0.1:9999"}},
		{name: "Scrawl's route someone else changed", settings: withScrawl, routes: map[string]string{"4311": before, "4312": "http://127.0.0.1:4387"}},
		{name: "Funnel on", settings: on, routes: map[string]string{"4311": before}, isFunnel: true},
		{name: "a stopped Tailscale", settings: on, routes: map[string]string{"4311": before}, isStopped: true, isError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			state := t.TempDir()
			writeSettings(t, state, test.settings)
			tailscale := &routedTailscale{routes: test.routes, isStopped: test.isStopped, isFunnel: test.isFunnel}
			routes := map[string]string{}
			for port, target := range test.routes {
				routes[port] = target
			}

			// Act
			moved, err := (&Manager{State: state, Target: after, Commands: tailscale}).Follow(context.Background())

			// Assert
			if moved || (err != nil) != test.isError {
				t.Fatalf("Follow = %v, %v; want unmoved, error %v", moved, err, test.isError)
			}
			for _, call := range tailscale.calls {
				if strings.HasPrefix(call, "serve --") {
					t.Errorf("Follow changed a route: %s", call)
				}
			}
			if saved, err := Read(state); err != nil || saved != test.settings {
				t.Errorf("settings = %+v, %v; want unchanged %+v", saved, err, test.settings)
			}
		})
	}
}

// A move Follow could not finish, because Tailscale refused Scrawl's route
// after the board's had moved, leaves the settings at the old address. The
// board's off switch still removes both routes and clears the settings, so
// turning remote access on again works.
func TestRemoteAccessOffRemovesRoutesAMoveLeftHalfway(t *testing.T) {
	// Arrange
	const before, after = "http://127.0.0.1:4310", "http://127.0.0.1:64610"
	state := t.TempDir()
	settings := Config{Enabled: true, Origin: "https://home.example.ts.net:4311", Login: "owner@example.com", Target: before, Scrawl: true, ScrawlTarget: "http://127.0.0.1:4387", ScrawlState: t.TempDir()}
	writeSettings(t, state, settings)
	tailscale := &routedTailscale{routes: map[string]string{"4311": before, "4312": before}, failing: "4312"}
	m := &Manager{State: state, Target: after, Commands: tailscale}
	if moved, err := m.Follow(context.Background()); moved || err == nil || tailscale.routes["4311"] != after {
		t.Fatalf("Follow = %v, %v with routes %v; want the board's route moved and Scrawl's refused", moved, err, tailscale.routes)
	}

	// Act
	err := m.Disable(context.Background())

	// Assert
	if err != nil || len(tailscale.routes) != 0 {
		t.Errorf("Disable = %v with routes %v; want both routes removed", err, tailscale.routes)
	}
	if saved, err := Read(state); err != nil || saved != (Config{}) {
		t.Errorf("settings = %+v, %v; want cleared", saved, err)
	}
}
