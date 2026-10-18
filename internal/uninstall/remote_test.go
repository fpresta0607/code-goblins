package uninstall

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/remoteaccess"
)

type remoteRoute struct {
	config string
	fail   bool
	calls  []string
}

func (r *remoteRoute) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	call := strings.Join(req.Args, " ")
	r.calls = append(r.calls, call)
	if r.fail {
		return execx.Result{ExitCode: 1}, nil
	}
	if call == "serve status --json" {
		return execx.Result{Stdout: []byte(r.config)}, nil
	}
	if call == "serve --https=4311 off" {
		r.config = "{}"
		return execx.Result{}, nil
	}
	return execx.Result{ExitCode: 1}, nil
}

func TestUninstallRemovesOnlyItsRemoteRouteAndKeepsFailedCleanup(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean", true: "Tailscale unavailable"}[fail], func(t *testing.T) {
			state := t.TempDir()
			file := filepath.Join(state, "remote-access.json")
			if err := os.WriteFile(file, []byte(`{"enabled":true,"origin":"https://pc.example.ts.net:4311","login":"owner@example.com","target":"http://127.0.0.1:4310"}`), 0600); err != nil {
				t.Fatal(err)
			}
			route := &remoteRoute{fail: fail, config: `{"TCP":{"4311":{"HTTPS":true}},"Web":{"pc.example.ts.net:4311":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:4310"}}}}}`}
			m := Machine{Home: home.Home{State: state}, Commands: route}
			items, err := m.shortcuts(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 || items[0].What != "its private Tailscale routes" {
				t.Fatal(items)
			}
			err = items[0].do(context.Background())
			if (err != nil) != fail {
				t.Fatal(err)
			}
			config, err := remoteaccess.Read(state)
			if err != nil || config.Enabled {
				t.Fatal("access not revoked", config, err)
			}
			again, err := m.shortcuts(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if fail && len(again) != 1 {
				t.Fatal("cleanup lost ownership metadata")
			}
			if !fail && len(again) != 0 {
				t.Fatal("successful cleanup still pending")
			}
			for _, call := range route.calls {
				if call != "serve status --json" && call != "serve --https=4311 off" {
					t.Fatal("unrelated routes touched", call)
				}
			}
		})
	}
}
