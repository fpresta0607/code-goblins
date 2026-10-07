package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/release"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/update"
)

// releaseServer stands in for GitHub: the latest release's record at
// /latest, and each of its files under /download/, as the release workflow
// publishes them.
type releaseServer struct {
	*httptest.Server
	tag       string
	files     map[string][]byte
	downloads atomic.Int32
}

func newReleaseServer(t *testing.T, tag string, programs map[string][]byte) *releaseServer {
	t.Helper()
	s := &releaseServer{tag: tag, files: map[string][]byte{}}
	var sums []string
	for name, data := range programs {
		s.files[name] = data
		digest := sha256.Sum256(data)
		sums = append(sums, hex.EncodeToString(digest[:])+"  "+name)
	}
	s.files["SHA256SUMS"] = []byte(strings.Join(sums, "\n") + "\n")
	s.files["install.ps1"] = []byte("& {\n    $releasePublisher = \"\"\n}\n")
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest" {
			var assets []map[string]string
			for name := range s.files {
				assets = append(assets, map[string]string{"name": name, "browser_download_url": s.URL + "/download/" + name})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": s.tag, "html_url": s.URL + "/tag/" + s.tag, "published_at": "2026-10-06T14:02:00Z",
				"body": "## What's Changed\n* feat(board): an update arrives in the Command Center by @fpresta0607 in https://example.invalid/pull/1\n", "assets": assets})
			return
		}
		if name, ok := strings.CutPrefix(r.URL.Path, "/download/"); ok && s.files[name] != nil {
			s.downloads.Add(1)
			_, _ = w.Write(s.files[name])
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

// buildBytes is the stand-in build named build, as a release would ship it.
func buildBytes(t *testing.T, build string) []byte {
	t.Helper()
	return writeBuild(t, filepath.Join(t.TempDir(), "cfo.exe"), build)
}

// updateFromRelease runs the home's installed cfo.exe update as the version
// installed, against the release server, on a machine of the test's own:
// its user environment names this home, and its profile folders are scratch.
func (u *updateHome) updateFromRelease(server *releaseServer, installed string, seams []string, arguments ...string) (int, string) {
	u.t.Helper()
	machine := u.t.TempDir()
	environment := filepath.Join(machine, "user-env.json")
	values, err := json.Marshal(map[string]string{"CFO_HOME": u.root, "Path": `C:\Windows;` + u.bin})
	if err != nil {
		u.t.Fatal(err)
	}
	if err := os.WriteFile(environment, values, 0o600); err != nil {
		u.t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(u.bin, "cfo.exe"), append([]string{"update"}, arguments...)...)
	cmd.Dir = u.root
	cmd.Env = append(os.Environ(), "CFO_TEST_UPDATE_ROOT="+u.root, "CFO_TEST_UPDATE_SERVE_WAIT=8s", "CFO_TEST_HANDOVER_WAIT=2s",
		"CFO_TEST_VERSION="+installed, release.APIVariable+"="+server.URL+"/latest",
		install.UserEnvFileVariable+"="+environment,
		"LOCALAPPDATA="+filepath.Join(machine, "Local"), "APPDATA="+filepath.Join(machine, "Roaming"),
		"USERPROFILE="+filepath.Join(machine, "profile"), "HOME="+filepath.Join(machine, "profile"),
		"CLAUDE_CONFIG_DIR="+filepath.Join(machine, "profile", ".claude"),
		"CODEX_HOME="+filepath.Join(machine, "profile", ".codex"),
		"PI_CODING_AGENT_DIR="+filepath.Join(machine, "profile", ".pi", "agent"))
	cmd.Env = append(cmd.Env, seams...)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	err = cmd.Run()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		u.t.Fatal(err)
	}
	return code, output.String()
}

// What Update in the Command Center runs: the home's installed build reads
// the newest release, keeps its programs once they match its SHA256SUMS, and
// lets the downloaded build install itself. The board restarts once, on the
// new build, the CFO's terminal keeps running, the desktop window follows,
// and the download is gone afterwards.
func TestAnUpdateFromAReleaseInstallsItAndRestartsOnlyTheBoard(t *testing.T) {
	// Arrange
	u := newUpdateHome(t, "previous", "candidate")
	oldSupervisor, cfoHost := u.serving()
	candidate := buildBytes(t, "candidate")
	window := []byte("the v0.5.0 window")
	server := newReleaseServer(t, "v0.5.0", map[string][]byte{"cfo.exe": candidate, "goblins-window.exe": window})

	// Act
	code, output := u.updateFromRelease(server, "v0.4.2", nil)

	// Assert
	if code != updateInstalled {
		t.Fatalf("update exited %d:\n%s", code, output)
	}
	for _, want := range []string{"[1/4] Download Code Goblins v0.5.0", "[2/4] Check the download", "cfo.exe matches the release's SHA256SUMS", "The release is unsigned", "[3/4] Install Code Goblins v0.5.0", "[4/4] Bring the home up to date", "already runs this build", "Updated: Code Goblins v0.5.0 runs."} {
		if !strings.Contains(output, want) {
			t.Errorf("the update did not say %q:\n%s", want, output)
		}
	}
	u.aliasesAre(candidate, "released")
	record := u.awaitBoard()
	if err := boardAlive(context.Background(), record); err != nil {
		t.Fatalf("the released build's supervisor does not serve (%v):\n%s", err, output)
	}
	if u.running(oldSupervisor) {
		t.Error("the previous supervisor still runs")
	}
	if !u.running(cfoHost) {
		t.Error("the CFO's terminal host was ended")
	}
	if got, err := os.ReadFile(filepath.Join(u.bin, release.Window)); err != nil || !bytes.Equal(got, window) {
		t.Errorf("bin's desktop window is %q (%v), want the release's", got, err)
	}
	if _, err := os.Stat(filepath.Join(update.Dir(u.state), "release", "v0.5.0")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the download is still in the home (%v)", err)
	}
	if contract, err := os.ReadFile(filepath.Join(u.root, "AGENTS.md")); err != nil || !bytes.Contains(contract, []byte("Chief Fuckaround Officer")) {
		t.Errorf("the home's contract was not brought up to date (%v)", err)
	}
}

// A download that does not match the release's sums is never run: the update
// stops before anything changes, says why in one line, and the board keeps
// serving the build it served.
func TestAnUpdateFromAReleaseRefusesADownloadThatFailsItsChecksum(t *testing.T) {
	// Arrange
	u := newUpdateHome(t, "previous", "candidate")
	oldSupervisor, _ := u.serving()
	server := newReleaseServer(t, "v0.5.0", map[string][]byte{"cfo.exe": buildBytes(t, "candidate")})
	server.files["cfo.exe"] = buildBytes(t, "tampered")

	// Act
	code, output := u.updateFromRelease(server, "v0.4.2", nil)

	// Assert
	if code != 1 || !strings.Contains(output, "Failed: the downloaded cfo.exe does not match the release's SHA256SUMS") {
		t.Fatalf("update exited %d, want 1 and the checksum named:\n%s", code, output)
	}
	if !strings.Contains(output, "[2/4] Check the download") || strings.Contains(output, "[3/4]") {
		t.Fatalf("the checksum failure was not reported in the check step:\n%s", output)
	}
	u.aliasesAre(u.previous, "previous")
	if !u.running(oldSupervisor) {
		t.Error("the supervisor was stopped for a download that was refused")
	}
	if _, err := update.ReadJournal(u.state); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused download started an update (journal: %v)", err)
	}
	if _, err := os.Stat(filepath.Join(u.state, "test-tampered-ran")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the download that failed its checksum ran")
	}
}

// A released build that does not serve is rolled back by its own update, and
// the update from the release says so in one line.
func TestAnUpdateFromAReleaseRollsBackABuildThatDoesNotServe(t *testing.T) {
	// Arrange
	u := newUpdateHome(t, "previous", "candidate")
	u.serving()
	server := newReleaseServer(t, "v0.5.0", map[string][]byte{"cfo.exe": buildBytes(t, "crash")})

	// Act
	code, output := u.updateFromRelease(server, "v0.4.2", nil)

	// Assert
	if code != updateRolledBack || !strings.Contains(output, "Rolled back: Code Goblins v0.4.2 serves again, and v0.5.0 was not installed") {
		t.Fatalf("update exited %d, want %d and the rollback said:\n%s", code, updateRolledBack, output)
	}
	u.previousServes()
}

func TestAnUpdateFromAReleaseReportsAnIncompleteHomeRefresh(t *testing.T) {
	cases := []struct {
		name             string
		isUnreadableHome bool
		isOtherHome      bool
		code             int
		reason           string
	}{
		{name: "malformed Claude settings", code: updateHomeIncomplete, reason: "settings.json"},
		{name: "unreadable machine home", isUnreadableHome: true, code: updateHomeIncomplete, reason: "which home this machine's install names could not be read"},
		{name: "another home's refresh is skipped", isOtherHome: true, code: updateInstalled, reason: "not this one"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			u := newUpdateHome(t, "previous", "candidate")
			oldSupervisor, cfoHost := u.serving()
			candidate := buildBytes(t, "candidate")
			server := newReleaseServer(t, "v0.5.0", map[string][]byte{"cfo.exe": candidate})
			machine := t.TempDir()
			settings := filepath.Join(machine, "settings.json")
			if err := os.WriteFile(settings, []byte("invalid JSON"), 0o600); err != nil {
				t.Fatal(err)
			}
			seams := []string{"CLAUDE_CONFIG_DIR=" + machine}
			if test.isUnreadableHome || test.isOtherHome {
				environment := filepath.Join(machine, "user-env.json")
				values := []byte("invalid JSON")
				if test.isOtherHome {
					otherHome := filepath.Join(machine, "other-home")
					if err := os.MkdirAll(filepath.Join(otherHome, "state"), 0o700); err != nil {
						t.Fatal(err)
					}
					var err error
					values, err = json.Marshal(map[string]string{"CFO_HOME": otherHome})
					if err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(environment, values, 0o600); err != nil {
					t.Fatal(err)
				}
				seams = append(seams, install.UserEnvFileVariable+"="+environment)
			}

			code, output := u.updateFromRelease(server, "v0.4.2", seams)

			if code != test.code || !strings.Contains(output, test.reason) {
				t.Fatalf("update exited %d, want %d saying %q:\n%s", code, test.code, test.reason, output)
			}
			want := "Updated: Code Goblins v0.5.0 runs."
			if test.code == updateHomeIncomplete {
				want = "Updated: Code Goblins v0.5.0 runs, but its install did not bring the home's contract, skills and hooks up to date (exit code 1); run goblins install to finish."
			}
			if !strings.HasSuffix(strings.TrimSpace(output), want) {
				t.Fatalf("the update did not end on %q:\n%s", want, output)
			}
			u.aliasesAre(candidate, "released")
			if err := boardAlive(context.Background(), u.awaitBoard()); err != nil {
				t.Fatalf("the released build does not serve: %v", err)
			}
			if u.running(oldSupervisor) || !u.running(cfoHost) {
				t.Fatal("the update did not restart only the supervisor")
			}
		})
	}
}

// releaseHome is a scratch home for an update run in this process, against
// server, as installed.
func releaseHome(t *testing.T, server *releaseServer, installed string) home.Home {
	t.Helper()
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state")}
	if err := os.MkdirAll(update.Dir(h.State), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(release.APIVariable, server.URL+"/latest")
	before := version
	version = installed
	t.Cleanup(func() { version = before })
	return h
}

// --check says how this build stands against the newest release and changes
// nothing, whatever that is: newer, the same, older, a build from a clone, or
// a release that cannot be read.
func TestUpdateCheckSaysHowThisBuildStands(t *testing.T) {
	cases := []struct {
		name, installed string
		offline         bool
		code            int
		says            string
	}{
		{"a newer release", "v0.4.2", false, 0, "Code Goblins v0.4.2 runs here; v0.5.0 was published"},
		{"the same release", "v0.5.0", false, 0, "Code Goblins v0.5.0 runs here, the newest release; nothing to update."},
		{"an older release", "v0.6.0", false, 0, "newer than the newest release, v0.5.0; nothing to update."},
		{"a build from a clone", "dev", false, 0, "built from a clone, so it updates from that clone"},
		{"no release to read", "v0.4.2", true, 1, "Failed: the newest release of Code Goblins could not be read"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			server := newReleaseServer(t, "v0.5.0", map[string][]byte{"cfo.exe": []byte("v0.5.0")})
			h := releaseHome(t, server, c.installed)
			if c.offline {
				server.Close()
			}
			var output bytes.Buffer

			// Act
			code := releaseUpdate(h, true, "", "", &output, &output)

			// Assert
			if code != c.code || !strings.Contains(output.String(), c.says) {
				t.Fatalf("update --check exited %d, want %d saying %q:\n%s", code, c.code, c.says, output.String())
			}
			if server.downloads.Load() != 0 {
				t.Fatalf("update --check downloaded %d files", server.downloads.Load())
			}
		})
	}
}

func TestUpdateCheckNamesWhatIsNew(t *testing.T) {
	server := newReleaseServer(t, "v0.5.0", map[string][]byte{"cfo.exe": []byte("v0.5.0")})
	h := releaseHome(t, server, "v0.4.2")
	var output bytes.Buffer

	releaseUpdate(h, true, "", "", &output, &output)

	if !strings.Contains(output.String(), "What's new:\n  - An update arrives in the Command Center\n") {
		t.Fatalf("update --check did not list what is new:\n%s", output.String())
	}
}

// The release the Overlord pressed Update on is the one installed: when a
// newer one was published since, nothing is downloaded and he is told.
func TestAnUpdateToAReleaseNoLongerTheNewestChangesNothing(t *testing.T) {
	before := updaterRefusal
	updaterRefusal = func(home.Home) error { return nil }
	t.Cleanup(func() { updaterRefusal = before })
	cases := []struct{ name, installed string }{
		{"an available update", "v0.4.2"},
		{"already current", "v0.5.1"},
		{"ahead of the release", "v0.6.0"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := newReleaseServer(t, "v0.5.1", map[string][]byte{"cfo.exe": []byte("v0.5.1")})
			h := releaseHome(t, server, test.installed)
			var output bytes.Buffer

			code := releaseUpdate(h, false, "v0.5.0", "", &output, &output)

			if code != 1 || !strings.Contains(output.String(), "the newest release is now v0.5.1, not v0.5.0, so nothing was changed") {
				t.Fatalf("update --to v0.5.0 exited %d:\n%s", code, output.String())
			}
			if server.downloads.Load() != 0 {
				t.Fatalf("it downloaded %d files", server.downloads.Load())
			}
		})
	}
}

func TestAnUpdateFromAReleaseReportsVerificationFailuresAtTheCheck(t *testing.T) {
	cases := []struct{ name, publisher, reason string }{
		{"checksum", "", "does not match the release's SHA256SUMS"},
		{"signature", "SIQstack LLC", "not validly signed by SIQstack LLC"},
	}
	beforeRefusal, beforeSignature := updaterRefusal, readSignature
	updaterRefusal = func(home.Home) error { return nil }
	readSignature = func(context.Context, string) (string, string, error) { return "HashMismatch", "SIQstack LLC", nil }
	t.Cleanup(func() { updaterRefusal, readSignature = beforeRefusal, beforeSignature })
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := newReleaseServer(t, "v0.5.0", map[string][]byte{release.Program: []byte("new build"), release.Window: []byte("new window")})
			if test.publisher == "" {
				server.files[release.Program] = []byte("tampered build")
			} else {
				server.files["install.ps1"] = []byte("$releasePublisher = \"" + test.publisher + "\"\n")
			}
			h := releaseHome(t, server, "v0.4.2")
			var output bytes.Buffer

			code := releaseUpdate(h, false, "", "", &output, &output)

			lines := strings.Split(strings.TrimSpace(output.String()), "\n")
			if code != 1 || len(lines) != 4 || lines[1] != "[1/4] Download Code Goblins v0.5.0" || lines[2] != "[2/4] Check the download" || !strings.HasPrefix(lines[3], "Failed:") || !strings.Contains(lines[3], test.reason) {
				t.Fatalf("update exited %d and did not report its %s failure at the check:\n%s", code, test.name, output.String())
			}
			if _, err := os.Stat(filepath.Join(update.Dir(h.State), "release", "v0.5.0")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("the refused download was kept: %v", err)
			}
		})
	}
}

// An update from a release is the Overlord's alone: run by an agent, which
// this test's own process is to the proof (an agent's harness above it, or
// parents that never reach the desktop on a CI runner), it is refused before
// anything is read or downloaded.
func TestAnUpdateFromAReleaseIsRefusedToAnAgent(t *testing.T) {
	server := newReleaseServer(t, "v0.5.0", map[string][]byte{"cfo.exe": []byte("v0.5.0")})
	h := releaseHome(t, server, "v0.4.2")
	t.Setenv("CLAUDECODE", "1")
	var output bytes.Buffer

	code := releaseUpdate(h, false, "", "", &output, &output)

	if code != 1 || !strings.Contains(output.String(), "updating Code Goblins is the Supreme Overlord's alone") {
		t.Fatalf("an agent's update exited %d:\n%s", code, output.String())
	}
	if server.downloads.Load() != 0 {
		t.Fatalf("it downloaded %d files", server.downloads.Load())
	}
}

// A build that is not the home's own installs itself, as before; one of the
// home's own updates from a release.
func TestTheHomesOwnProgramsAreItsInstalledBuild(t *testing.T) {
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state")}
	cases := map[string]bool{
		filepath.Join(h.Bin(), "cfo.exe"):                   true,
		filepath.Join(h.Bin(), "GOBLINS.EXE"):               true,
		filepath.Join(root, "goblins.exe"):                  true,
		filepath.Join(root, "cfo.exe.held-candidate"):       false,
		filepath.Join(h.Bin(), "goblins-window.exe"):        false,
		filepath.Join(t.TempDir(), "cfo.exe"):               false,
		filepath.Join(update.Dir(h.State), "candidate.exe"): false,
	}
	for program, want := range cases {
		if got := installedBuild(h, program); got != want {
			t.Errorf("installedBuild(%s) = %v, want %v", program, got, want)
		}
	}
}

// The Update item the Overlord pressed runs goblins update with --run: the
// grant the board wrote for his click stands for him in place of a terminal
// of his own, once, for that item and release alone; with no grant for it,
// nothing is read or downloaded.
func TestAnUpdatePressedInTheCommandCenterRunsOnItsGrant(t *testing.T) {
	cases := []struct {
		name  string
		grant *supervisor.UpdateGrant
		code  int
		says  string
	}{
		{"no grant", nil, 1, "Failed: no Update was pressed for this item in the Command Center. Nothing was changed."},
		{"a grant for another release", &supervisor.UpdateGrant{Run: "update-v0.5.0-1", Tag: "v0.4.9", GrantedAt: time.Now()}, 1, "not this one"},
		{"its own grant", &supervisor.UpdateGrant{Run: "update-v0.5.0-1", Tag: "v0.5.0", GrantedAt: time.Now()}, 0, "Code Goblins v0.5.0 runs here, the newest release; nothing to update."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange: an agent's environment, which the terminal proof
			// refuses, so only the grant lets the command through.
			server := newReleaseServer(t, "v0.5.0", map[string][]byte{"cfo.exe": []byte("v0.5.0")})
			h := releaseHome(t, server, "v0.5.0")
			t.Setenv("CLAUDECODE", "1")
			if c.grant != nil {
				data, err := json.Marshal(c.grant)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(update.Dir(h.State), "grant.json"), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer

			// Act
			code := releaseUpdate(h, false, "v0.5.0", "update-v0.5.0-1", &output, &output)

			// Assert
			if code != c.code || !strings.Contains(output.String(), c.says) {
				t.Fatalf("update --run exited %d, want %d saying %q:\n%s", code, c.code, c.says, output.String())
			}
			if server.downloads.Load() != 0 {
				t.Fatalf("it downloaded %d files", server.downloads.Load())
			}
		})
	}
}
