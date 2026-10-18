package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/remoteaccess"
)

// Remote access is off until the Overlord turns it on, so a goblin's and a
// gate agent's terminal can read its status but never turn it on or off.
func TestRemoteOnAndOffAreRefusedInAGoblinsOrAGateAgentsTerminal(t *testing.T) {
	for _, variable := range [][2]string{{harness.RoleVariable, harness.RoleGoblin}, {gateAgentVariable, "1"}} {
		t.Run(variable[0], func(t *testing.T) {
			// Arrange
			asTheCFO(t)
			t.Setenv(variable[0], variable[1])
			h := testHome(t)
			deps := testCommandRuntimeForHome(h)

			for _, change := range []string{"on", "off"} {
				var stdout, stderr bytes.Buffer

				// Act
				exit := runWithRuntime([]string{"remote", change}, &stdout, &stderr, deps)

				// Assert
				if exit != 2 || !strings.Contains(stderr.String(), "never a goblin or a gate agent") {
					t.Fatalf("remote %s: exit %d, stderr %q; want it refused", change, exit, stderr.String())
				}
			}
			if _, err := os.Stat(h.State); !os.IsNotExist(err) {
				t.Fatalf("a refused change touched the home's state: %v", err)
			}
			var stdout bytes.Buffer
			if exit := runWithRuntime([]string{"remote", "status"}, &stdout, &bytes.Buffer{}, deps); exit != 0 || !strings.Contains(stdout.String(), "Remote board access is off") {
				t.Fatalf("status: exit %d, stdout %q; want it allowed", exit, stdout.String())
			}
		})
	}
}

// doctorTailscale stands in for the Tailscale CLI: connected as the owner,
// serving the home's route, or not running at all.
type doctorTailscale struct {
	isStopped bool
	serve     string
}

func (d doctorTailscale) Run(_ context.Context, r execx.Request) (execx.Result, error) {
	if d.isStopped {
		return execx.Result{ExitCode: 1}, nil
	}
	switch strings.Join(r.Args, " ") {
	case "status --json":
		return execx.Result{Stdout: []byte(`{"BackendState":"Running","Self":{"DNSName":"pc.example.ts.net.","UserID":7},"User":{"7":{"LoginName":"owner@example.com"}}}`)}, nil
	case "serve status --json":
		return execx.Result{Stdout: []byte(d.serve)}, nil
	}
	return execx.Result{ExitCode: 1}, nil
}

// cfo doctor names remote access's state and what it needs, and never the
// tailnet's name or the owner's account, since agents quote its lines.
func TestDoctorReportsRemoteAccessWithoutNamingTheTailnet(t *testing.T) {
	const route = `{"TCP":{"4311":{"HTTPS":true}},"Web":{"pc.example.ts.net:4311":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:4310"}}}}}`
	const on = `{"enabled":true,"origin":"https://pc.example.ts.net:4311","login":"owner@example.com","target":"http://127.0.0.1:4310"}`
	const reserved = `{"enabled":false,"origin":"https://pc.example.ts.net:4311","login":"owner@example.com","target":"http://127.0.0.1:4310"}`
	for _, tc := range []struct {
		name           string
		settings       string
		commands       doctorTailscale
		isBoardRunning bool
		want           string
	}{
		{"never turned on", "", doctorTailscale{isStopped: true}, true, "off; to use the board from a phone"},
		{"on and ready", on, doctorTailscale{serve: route}, true, "on and ready; cfo remote status shows its link"},
		{"on with Tailscale stopped", on, doctorTailscale{isStopped: true}, true, "on, but not ready: Tailscale is not running on this PC"},
		{"on with the board stopped", on, doctorTailscale{serve: route}, false, "on, but the board is stopped; start Code Goblins on this PC"},
		{"off with its route reserved", reserved, doctorTailscale{serve: route}, true, "an earlier change left its Tailscale route reserved: run cfo remote on to finish turning it on, or cfo remote off to remove it"},
		{"settings unreadable", "{", doctorTailscale{}, true, "settings unreadable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			state := t.TempDir()
			if tc.settings != "" {
				if err := os.WriteFile(filepath.Join(state, "remote-access.json"), []byte(tc.settings), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			m := &remoteaccess.Manager{State: state, Target: "http://127.0.0.1:4310", Commands: tc.commands}

			// Act
			line := remoteAccessLine(context.Background(), m, tc.isBoardRunning)

			// Assert
			if !strings.Contains(line, tc.want) {
				t.Errorf("line %q, want %q", line, tc.want)
			}
			for _, private := range []string{"pc.example.ts.net", "owner@example.com"} {
				if strings.Contains(line, private) {
					t.Errorf("line %q names %s", line, private)
				}
			}
		})
	}
}
