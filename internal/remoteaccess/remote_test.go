package remoteaccess

import (
	"cmp"
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
	programs      []string
	fail          bool
	wrongReadback bool
	// backend is the state tailscale status reports, Running when empty.
	backend string
	// serveWaits stands for a serve that waits for Serve or HTTPS to be
	// approved for the tailnet until its context runs out.
	serveWaits bool
	// isWithoutHTTPS stands for a tailnet that has not approved HTTPS, which
	// gives the PC no certificate domains.
	isWithoutHTTPS bool
	// statusWaits stands for a tailscale status that never answers.
	statusWaits bool
}

const ownProxy = `{"TCP":{"4311":{"HTTPS":true}},"Web":{"home.example.ts.net:4311":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:4310"}}}}}`

func (f *fakeTailscale) Run(ctx context.Context, r execx.Request) (execx.Result, error) {
	call := strings.Join(r.Args, " ")
	f.calls = append(f.calls, call)
	f.programs = append(f.programs, r.Name)
	if !strings.EqualFold(strings.TrimSuffix(filepath.Base(r.Name), ".exe"), "tailscale") {
		return execx.Result{}, fmt.Errorf("unexpected program %s", r.Name)
	}
	if f.fail {
		return execx.Result{ExitCode: 1, Stderr: []byte("https://login.secret.example/consent?token=private")}, nil
	}
	switch call {
	case "status --json":
		if f.statusWaits {
			return execx.Result{}, context.DeadlineExceeded
		}
		certDomains := `["home.example.ts.net"]`
		if f.isWithoutHTTPS {
			certDomains = `null`
		}
		return execx.Result{Stdout: []byte(`{"BackendState":"` + cmp.Or(f.backend, "Running") + `","CertDomains":` + certDomains + `,"Self":{"DNSName":"home.example.ts.net.","UserID":123,"TailscaleIPs":["100.64.5.6","fd7a:115c:a1e0::123"]},"User":{"123":{"LoginName":"owner@example.com"}}}`)}, nil
	case "serve status --json":
		if f.config == "" {
			return execx.Result{Stdout: []byte("{}")}, nil
		}
		return execx.Result{Stdout: []byte(f.config)}, nil
	case "serve --bg --https=4311 http://127.0.0.1:4310":
		if f.serveWaits {
			return execx.Result{Stdout: []byte("Serve is not enabled on your tailnet.\nTo enable, visit:\n\n  https://login.secret.example/f/serve?node=private\n")}, context.DeadlineExceeded
		}
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

// A new user without Tailscale is told it is missing, by the real command
// runner, and nothing is saved, so remote access stays off.
func TestWithoutTailscaleEnableAndStatusSayItIsNotInstalled(t *testing.T) {
	// Arrange
	t.Setenv("PATH", t.TempDir())
	t.Setenv("ProgramFiles", t.TempDir())
	m := &Manager{State: t.TempDir(), Target: testConfig().Target}

	// Act
	_, err := m.Enable(context.Background())

	// Assert
	if err == nil || !strings.Contains(err.Error(), "Tailscale is not installed on this PC") {
		t.Fatalf("enable without Tailscale: %v", err)
	}
	if c, err := Read(m.State); err != nil || c != (Config{}) {
		t.Fatal("a failed enable saved settings", c, err)
	}
	if s := m.Status(context.Background()); s.Enabled || s.Issue != "" {
		t.Fatal("an off home reports a problem", s)
	}

	// A home that turned it on before Tailscale was removed says why it is
	// not ready.
	if err := write(m.State, testConfig()); err != nil {
		t.Fatal(err)
	}
	if s := m.Status(context.Background()); s.Ready || !strings.Contains(s.Issue, "Tailscale is not installed on this PC") {
		t.Fatal(s)
	}
}

// A board keeps the PATH it started with. Tailscale's installer adds its
// folder in Program Files to the machine's PATH, so a board started before
// the install finds the CLI there.
func TestTailscaleInstalledAfterTheBoardStartedIsFound(t *testing.T) {
	// Arrange
	programFiles := t.TempDir()
	installed := filepath.Join(programFiles, "Tailscale", "tailscale.exe")
	if err := os.MkdirAll(filepath.Dir(installed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, []byte("stand-in"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("ProgramFiles", programFiles)
	f := &fakeTailscale{}
	m := &Manager{State: t.TempDir(), Target: testConfig().Target, Commands: f}

	// Act
	s, err := m.Enable(context.Background())

	// Assert
	if err != nil || !s.Ready {
		t.Fatal(s, err)
	}
	for _, program := range f.programs {
		if program != installed {
			t.Fatalf("ran %s, want the installed %s", program, installed)
		}
	}
}

// A first serve on a tailnet that has not approved Serve or HTTPS waits for
// that approval and prints the link to approve it. The wait running out is
// not Tailscale missing, and the link is never repeated.
func TestServeWaitingForApprovalSaysSoAndNamesTheCommand(t *testing.T) {
	// Arrange
	f := &fakeTailscale{serveWaits: true}
	m := &Manager{State: t.TempDir(), Target: testConfig().Target, Commands: f}

	// Act
	_, err := m.Enable(context.Background())

	// Assert
	if err == nil {
		t.Fatal("enabled without a route")
	}
	for _, want := range []string{"waiting for Serve or HTTPS to be approved", "tailscale serve --bg --https=4311 http://127.0.0.1:4310"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not say %q", err, want)
		}
	}
	for _, wrong := range []string{"not installed", "install and connect", "secret"} {
		if strings.Contains(err.Error(), wrong) {
			t.Errorf("%q says %q", err, wrong)
		}
	}
	if c, err := Read(m.State); err != nil || c.Enabled {
		t.Fatal(c, err)
	}
}

// Tailscale installed but stopped, signed out or disconnected is named as
// that, when turning it on and in the status of a home that has it on.
func TestTailscaleNotConnectedSaysWhatIsMissing(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    *fakeTailscale
		want string
	}{
		{"service stopped", &fakeTailscale{fail: true}, "Tailscale is not running on this PC"},
		{"signed out", &fakeTailscale{backend: "NeedsLogin"}, "Tailscale is signed out on this PC"},
		{"disconnected", &fakeTailscale{backend: "Stopped"}, "Tailscale is not connected on this PC"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			m := &Manager{State: t.TempDir(), Target: testConfig().Target, Commands: tc.f}

			// Act
			_, enableErr := m.Enable(context.Background())
			if err := write(m.State, testConfig()); err != nil {
				t.Fatal(err)
			}
			s := m.Status(context.Background())

			// Assert
			if enableErr == nil || !strings.Contains(enableErr.Error(), tc.want) {
				t.Errorf("enable: %v, want %q", enableErr, tc.want)
			}
			if s.Ready || !strings.Contains(s.Issue, tc.want) {
				t.Errorf("status: %+v, want %q", s, tc.want)
			}
		})
	}
}

// With remote access off, the board reads by itself what this PC's
// Tailscale is, so its switch can be offered or say what is missing, with the
// command that mends it where one does. It never reports that as a problem
// of remote access, and Status alone, which cfo doctor reads, asks
// Tailscale nothing while access is off.
func TestOffDetectReadsWhatTailscaleIsForTurningOn(t *testing.T) {
	const serve = "tailscale serve --bg --https=4311 http://127.0.0.1:4310"
	for _, tc := range []struct {
		name      string
		commands  *fakeTailscale
		reserved  bool
		want      string
		wantIssue string
		wantFix   string
	}{
		{"not installed", nil, false, "missing", "Tailscale is not installed on this PC", "winget install --exact --id Tailscale.Tailscale"},
		{"service stopped", &fakeTailscale{fail: true}, false, "stopped", "Tailscale is not running on this PC", ""},
		{"signed out", &fakeTailscale{backend: "NeedsLogin"}, false, "signed_out", "Tailscale is signed out on this PC", ""},
		{"disconnected", &fakeTailscale{backend: "Stopped"}, false, "disconnected", "Tailscale is not connected on this PC", ""},
		{"HTTPS not approved", &fakeTailscale{isWithoutHTTPS: true}, false, "no_https", "HTTPS is not approved for your tailnet", serve},
		{"signed in with HTTPS", &fakeTailscale{}, false, "ready", "", ""},
		{"a route a turn-on reserved while it waited on approval", &fakeTailscale{}, true, "ready", "", serve},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			t.Setenv("PATH", t.TempDir())
			t.Setenv("ProgramFiles", t.TempDir())
			m := &Manager{State: t.TempDir(), Target: testConfig().Target}
			if tc.commands != nil {
				m.Commands = tc.commands
			}
			if tc.reserved {
				reserved := testConfig()
				reserved.Enabled = false
				if err := write(m.State, reserved); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			plain := m.Status(context.Background())
			s := m.Detect(context.Background(), plain)

			// Assert
			if tc.commands != nil && len(tc.commands.calls) != 1 {
				t.Fatalf("Status and Detect asked Tailscale %q, want only Detect's one status", tc.commands.calls)
			}
			if plain.Tailscale != "" || s.Enabled || s.Issue != "" || s.Attention != "" || s.Tailscale != tc.want || !strings.Contains(s.TailscaleIssue, tc.wantIssue) || tc.wantIssue == "" && s.TailscaleIssue != "" || s.Fix != tc.wantFix {
				t.Fatalf("status %+v, want Tailscale %q saying %q with fix %q, and no issue", s, tc.want, tc.wantIssue, tc.wantFix)
			}
		})
	}
}

// The board reads these names; a rename here would leave its switches dark.
func TestStatusKeepsTheNamesTheBoardReads(t *testing.T) {
	// Arrange
	s := Status{Enabled: true, Ready: true, URL: "u", Login: "l", Issue: "i", Attention: "a", Tailscale: "t", TailscaleIssue: "ti", Fix: "f", Scrawl: true, ScrawlState: "s", ScrawlReady: true, ScrawlIssue: "si", ScrawlFix: "sf"}

	// Act
	data, err := json.Marshal(s)
	var names map[string]any
	if err == nil {
		err = json.Unmarshal(data, &names)
	}

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"enabled", "ready", "url", "issue", "attention", "tailscale", "tailscale_issue", "fix", "scrawl", "scrawl_state", "scrawl_issue", "scrawl_fix"} {
		if _, ok := names[name]; !ok {
			t.Errorf("Status has no %q for the board", name)
		}
	}
}

// While remote access is on, what needs him is named in a word beside the
// sentence, so the board shows a few words and never a cut sentence.
func TestOnStatusNamesWhatNeedsHimInAWord(t *testing.T) {
	funnel := `{"TCP":{"4311":{"HTTPS":true}},"Web":{"home.example.ts.net:4311":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:4310"}}}},"AllowFunnel":{"home.example.ts.net:4311":true}}`
	for _, tc := range []struct {
		name     string
		target   string
		settings string
		commands *fakeTailscale
		want     string
	}{
		{"settings unreadable", testConfig().Target, "{", &fakeTailscale{}, "settings"},
		{"board moved", "http://127.0.0.1:4399", "", &fakeTailscale{config: ownProxy}, "board_moved"},
		{"board moved while Tailscale is stopped", "http://127.0.0.1:4399", "", &fakeTailscale{fail: true}, "stopped"},
		{"Tailscale stopped", testConfig().Target, "", &fakeTailscale{fail: true}, "stopped"},
		{"Funnel on", testConfig().Target, "", &fakeTailscale{config: funnel}, "funnel"},
		{"route gone", testConfig().Target, "", &fakeTailscale{}, "route_missing"},
		{"ready", testConfig().Target, "", &fakeTailscale{config: ownProxy}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			m := &Manager{State: t.TempDir(), Target: tc.target, Commands: tc.commands}
			if err := write(m.State, testConfig()); err != nil {
				t.Fatal(err)
			}
			if tc.settings != "" {
				if err := os.WriteFile(filepath.Join(m.State, configFile), []byte(tc.settings), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			s := m.Status(context.Background())

			// Assert
			if s.Attention != tc.want || (tc.want == "") != (s.Issue == "") {
				t.Fatalf("status %+v, want attention %q with its sentence", s, tc.want)
			}
		})
	}
}

// The first serve run by hand to get Serve and HTTPS approved leaves exactly
// this route with no record of it; turning remote access on takes it as its
// own rather than refusing a port that only it serves.
func TestTurningOnAdoptsTheExactRouteTheFirstServeLeft(t *testing.T) {
	// Arrange
	f := &fakeTailscale{config: ownProxy}
	m := &Manager{State: t.TempDir(), Target: testConfig().Target, Commands: f}

	// Act
	s, err := m.Enable(context.Background())

	// Assert
	if err != nil || !s.Ready {
		t.Fatal(s, err)
	}
	if c, err := Read(m.State); err != nil || !c.Enabled {
		t.Fatal("the route was not taken as its own", c, err)
	}
}

// A timeout names the command that ran out of time, and only a serve that
// starts a route can be waiting for Serve or HTTPS to be approved.
func TestATimeoutNamesItsCommandAndOnlyServeHintsAtApproval(t *testing.T) {
	// Arrange
	f := &fakeTailscale{statusWaits: true}
	m := &Manager{State: t.TempDir(), Target: testConfig().Target, Commands: f}

	// Act
	_, err := m.Enable(context.Background())

	// Assert
	if err == nil || !strings.Contains(err.Error(), "tailscale status --json did not answer within 15 seconds") {
		t.Fatalf("enable with status timing out: %v", err)
	}
	if strings.Contains(err.Error(), "approved") {
		t.Errorf("%q blames an approval a status never waits for", err)
	}
}

// Turning remote access off with the Tailscale service stopped revokes it
// and says Tailscale is not running, never that HTTPS needs enabling.
func TestOffWithTailscaleStoppedSaysItIsNotRunning(t *testing.T) {
	// Arrange
	f := &fakeTailscale{fail: true}
	m := &Manager{State: t.TempDir(), Target: testConfig().Target, Commands: f}
	if err := write(m.State, testConfig()); err != nil {
		t.Fatal(err)
	}

	// Act
	err := m.Disable(context.Background())

	// Assert
	if err == nil || !strings.Contains(err.Error(), "Tailscale is not running on this PC") || !strings.Contains(err.Error(), "run cfo remote off again") {
		t.Fatalf("off with Tailscale stopped: %v", err)
	}
	if strings.Contains(err.Error(), "enable HTTPS") || strings.Contains(err.Error(), "secret") {
		t.Errorf("%q misnames the problem or echoes the CLI", err)
	}
	if c, err := Read(m.State); err != nil || c.Enabled || c.Origin == "" {
		t.Fatal("off did not revoke or lost the route it owns", c, err)
	}
}
