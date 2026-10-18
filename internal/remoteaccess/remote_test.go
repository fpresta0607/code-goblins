package remoteaccess

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type fakeTailscale struct {
	config        string
	calls         []string
	fail          bool
	wrongReadback bool
}

const ownProxy = `{"TCP":{"4311":{"HTTPS":true}},"Web":{"home.example.ts.net:4311":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:4310"}}}}}`

func (f *fakeTailscale) Run(ctx context.Context, r execx.Request) (execx.Result, error) {
	call := strings.Join(r.Args, " ")
	f.calls = append(f.calls, call)
	if r.Name != "tailscale" {
		return execx.Result{}, fmt.Errorf("unexpected program %s", r.Name)
	}
	if f.fail {
		return execx.Result{ExitCode: 1, Stderr: []byte("https://login.secret.example/consent?token=private")}, nil
	}
	switch call {
	case "status --json":
		return execx.Result{Stdout: []byte(`{"BackendState":"Running","Self":{"DNSName":"home.example.ts.net.","UserID":123,"TailscaleIPs":["100.64.5.6","fd7a:115c:a1e0::123"]},"User":{"123":{"LoginName":"owner@example.com"}}}`)}, nil
	case "serve status --json":
		if f.config == "" {
			return execx.Result{Stdout: []byte("{}")}, nil
		}
		return execx.Result{Stdout: []byte(f.config)}, nil
	case "serve --bg --https=4311 http://127.0.0.1:4310":
		if !f.wrongReadback {
			f.config = ownProxy
		}
		return execx.Result{}, nil
	case "serve --https=4311 off":
		f.config = "{}"
		return execx.Result{}, nil
	default:
		return execx.Result{}, fmt.Errorf("unexpected command %q", call)
	}
}

func TestRemoteOnPersistsVerifiedOwnerAndRestoresAfterRestart(t *testing.T) {
	f := &fakeTailscale{}
	started := false
	m := &Manager{State: t.TempDir(), Target: testConfig().Target, Commands: f, StartAtLogin: func() error { started = true; return nil }}
	s, err := m.Enable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !s.Ready || !s.Enabled || s.Login != testConfig().Login || s.URL != testConfig().Origin || !started {
		t.Fatal(s, started)
	}
	copy := &Manager{State: m.State, Target: m.Target, Commands: f}
	if s := copy.Status(context.Background()); !s.Ready {
		t.Fatal("restart lost configuration", s)
	}
	if _, err := copy.Enable(context.Background()); err != nil {
		t.Fatal("second enable", err)
	}
	if err := copy.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := copy.Status(context.Background()); s.Enabled || s.Ready {
		t.Fatal(s)
	}
	for _, call := range f.calls {
		if strings.Contains(call, "reset") || strings.Contains(call, "funnel") {
			t.Fatal("unrelated route command", call)
		}
	}
}

func TestForeignTailscaleRoutesAreNeverReplacedOrRemoved(t *testing.T) {
	for _, config := range []string{
		`{"TCP":{"4311":{"HTTPS":true}},"Web":{"home.example.ts.net:4311":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:9999"}}}}}`,
		`{"TCP":{"4311":{"TCPForward":"127.0.0.1:4310"}}}`,
		`{"TCP":{"4311":{"HTTPS":true}},"Web":{"home.example.ts.net:4311":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:4310"},"/other":{"Text":"keep"}}}}}`,
		`{"Foreground":{"other-process":{"TCP":{"4311":{"HTTPS":true}},"Web":{"home.example.ts.net:4311":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:4310"}}}}}}}`,
	} {
		t.Run(config, func(t *testing.T) {
			f := &fakeTailscale{config: config}
			m := &Manager{State: t.TempDir(), Target: testConfig().Target, Commands: f}
			_ = write(m.State, testConfig())
			if _, err := m.Enable(context.Background()); err == nil {
				t.Fatal("foreign route overwritten")
			}
			if err := m.Disable(context.Background()); err == nil {
				t.Fatal("foreign route silently removed")
			}
			if c, err := Read(m.State); err != nil || c.Enabled {
				t.Fatal("failed cleanup did not revoke", c, err)
			}
			if f.config != config {
				t.Fatal("foreign config changed")
			}
			for _, call := range f.calls {
				if call != "status --json" && call != "serve status --json" {
					t.Fatal("mutated another route", call)
				}
			}
		})
	}
}

func TestUnusedPortCanCoexistWithOtherServePorts(t *testing.T) {
	f := &fakeTailscale{config: `{"TCP":{"443":{"HTTPS":true}},"Web":{"home.example.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:9999"}}}},"AllowFunnel":{"home.example.ts.net:443":true}}`}
	m := &Manager{State: t.TempDir(), Target: testConfig().Target, Commands: f}
	if _, err := m.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.calls, "serve --bg --https=4311 http://127.0.0.1:4310") {
		t.Fatal(f.calls)
	}
}

func TestServeMustReadBackBeforeRemoteAccessIsAuthorized(t *testing.T) {
	f := &fakeTailscale{wrongReadback: true}
	m := &Manager{State: t.TempDir(), Target: testConfig().Target, Commands: f}
	if _, err := m.Enable(context.Background()); err == nil {
		t.Fatal("unverified configuration enabled")
	}
	c, err := Read(m.State)
	if err != nil || c.Enabled {
		t.Fatal(c, err)
	}
}

func TestOffRevokesEvenWhenTailscaleFailsOrSettingsAreCorrupt(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			f := &fakeTailscale{fail: true}
			m := &Manager{State: t.TempDir(), Target: testConfig().Target, Commands: f}
			if corrupt {
				_ = os.WriteFile(filepath.Join(m.State, configFile), []byte("{"), 0600)
			} else {
				_ = write(m.State, testConfig())
			}
			if err := m.Disable(context.Background()); err == nil {
				t.Fatal("cleanup failure not reported")
			} else if strings.Contains(err.Error(), "private") {
				t.Fatal("CLI secret echoed", err)
			}
			c, err := Read(m.State)
			if (err != nil) != corrupt || c.Enabled {
				t.Fatal(c, err)
			}
		})
	}
}

func TestConfigRejectsNonPrivateOriginsAndTargets(t *testing.T) {
	for _, origin := range []string{"http://home.example.ts.net:4311", "https://home.example.ts.net.evil:4311", "https://home.example.ts.net:443", "https://owner:secret@home.example.ts.net:4311", "https://home.example.ts.net:4311/", "https://home.example.ts.net:4311?x=1", "https://home.example.ts.net:4311#secret", "https://*.example.ts.net:4311"} {
		c := testConfig()
		c.Origin = origin
		if c.validate() == nil {
			t.Error("accepted", origin)
		}
	}
	for _, target := range []string{"http://0.0.0.0:4310", "http://100.64.0.7:4310", "http://localhost:4310", "https://127.0.0.1:4310", "http://127.0.0.1:0", "http://127.0.0.1:4310/path"} {
		c := testConfig()
		c.Target = target
		if c.validate() == nil {
			t.Error("accepted", target)
		}
	}
}

func TestFunnelIsNeverReportedReady(t *testing.T) {
	f := &fakeTailscale{}
	var config map[string]any
	_ = json.Unmarshal([]byte(ownProxy), &config)
	config["AllowFunnel"] = map[string]bool{"home.example.ts.net:4311": true}
	data, _ := json.Marshal(config)
	f.config = string(data)
	m := &Manager{State: t.TempDir(), Target: testConfig().Target, Commands: f}
	_ = write(m.State, testConfig())
	if s := m.Status(context.Background()); s.Ready || s.Issue == "" {
		t.Fatal(s)
	}
}
