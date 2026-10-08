package doctor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The answers are the ones each source gave on 2026-10-07: Claude Code's
// latest channel a bare version line, npm the package's latest manifest.
func TestHarnessVersionsNameTheNewestAndTheCommandThatInstallsIt(t *testing.T) {
	// Arrange
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/claude":
			_, _ = response.Write([]byte("2.1.293\n"))
		case "/codex":
			_, _ = response.Write([]byte(`{"name":"@openai/codex","version":"0.161.0","bin":{"codex":"bin/codex.js"}}`))
		case "/pi":
			http.Error(response, "registry unavailable", http.StatusServiceUnavailable)
		case "/html":
			_, _ = response.Write([]byte("<html>not here</html>"))
		}
	}))
	defer server.Close()
	releases := Releases{
		"claude": {URL: server.URL + "/claude", Source: "Claude Code's latest channel", Update: "claude update"},
		"codex":  {URL: server.URL + "/codex", Source: "npm", Update: "npm install -g @openai/codex@", IsNPM: true},
		"pi":     {URL: server.URL + "/pi", Source: "npm", Update: "npm install -g @earendil-works/pi-coding-agent@", IsNPM: true},
	}
	probes := []HarnessProbe{
		{Name: "claude", Detail: "2.1.293 (Claude Code)", OK: true},
		{Name: "codex", Detail: "codex-cli 0.160.0", OK: true},
		{Name: "pi", Detail: "0.85.1", OK: true},
	}

	// Act
	versions := releases.Versions(context.Background(), server.Client(), probes)

	// Assert
	if len(versions) != 3 {
		t.Fatalf("versions = %+v", versions)
	}
	if claude := versions[0]; claude.Installed != "2.1.293" || claude.Newest != "2.1.293" || claude.Update != "" || claude.Problem != "" {
		t.Errorf("claude = %+v, want the newest installed and nothing to run", claude)
	}
	if codex := versions[1]; codex.Installed != "0.160.0" || codex.Newest != "0.161.0" || codex.Update != "npm install -g @openai/codex@0.161.0" || codex.Source != "npm" {
		t.Errorf("codex = %+v, want 0.161.0 on npm and its install command", codex)
	}
	if pi := versions[2]; pi.Installed != "0.85.1" || pi.Newest != "" || !strings.Contains(pi.Problem, "503") {
		t.Errorf("pi = %+v, want the unread newest with why", pi)
	}

	// A page that is not a version is no version.
	html := Releases{"claude": {URL: server.URL + "/html", Source: "Claude Code's latest channel", Update: "claude update"}}
	if read := html.Versions(context.Background(), server.Client(), probes[:1]); read[0].Newest != "" || read[0].Problem == "" {
		t.Errorf("an HTML answer = %+v, want it refused", read[0])
	}
}

func TestHarnessVersionsLeaveOutABrokenHarness(t *testing.T) {
	// Arrange
	releases := Releases{"codex": {URL: "http://127.0.0.1:1/never-asked", Source: "npm", IsNPM: true}}

	// Act
	versions := releases.Versions(context.Background(), http.DefaultClient, []HarnessProbe{{Name: "codex", Detail: "not found on PATH"}})

	// Assert
	if len(versions) != 0 {
		t.Fatalf("a harness the probe found broken has no installed version: %+v", versions)
	}
}

func TestNewerVersionOrdersByNumberAndPutsAPreReleaseFirst(t *testing.T) {
	for _, test := range []struct {
		version, than string
		isNewer       bool
	}{
		{"0.161.0", "0.160.0", true},
		{"0.160.0", "0.161.0", false},
		{"0.10.0", "0.9.9", true},
		{"1.1.0", "0.85.1", true},
		{"2.1.293", "2.1.293", false},
		{"0.158.0", "0.158.0-alpha", true},
		{"0.158.0-alpha", "0.158.0", false},
		{"0.159.0-alpha.12", "0.158.0", true},
	} {
		t.Run(test.version+" than "+test.than, func(t *testing.T) {
			if isNewer := newerVersion(test.version, test.than); isNewer != test.isNewer {
				t.Fatalf("newer=%v, want %v", isNewer, test.isNewer)
			}
		})
	}
}

func TestVersionInReadsTheVersionOutOfAHarnessVersionLine(t *testing.T) {
	for line, want := range map[string]string{
		"2.1.293 (Claude Code)":       "2.1.293",
		"codex-cli 0.160.0":           "0.160.0",
		"0.85.1":                      "0.85.1",
		"codex-cli 0.158.0-alpha.3":   "0.158.0-alpha.3",
		"codex --version timed out":   "",
		"not found on PATH (install)": "",
	} {
		if got := versionIn(line); got != want {
			t.Errorf("versionIn(%q) = %q, want %q", line, got, want)
		}
	}
}

// The Codex desktop app is a packaged app: where it is installed, its
// bundled codex.exe is found by its package family, and elsewhere, as on CI,
// it is reported absent rather than as a failure. Whether it is installed
// for this user is read apart, from the folder Windows keeps its execution
// aliases in, so an answer of absent is checked too.
func TestDesktopCodexIsFoundByItsPackageOrReportedAbsent(t *testing.T) {
	_, aliasErr := os.Stat(filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "WindowsApps", codexPackageFamily))

	path, isInstalled, err := DesktopCodex()

	if err != nil {
		t.Fatal(err)
	}
	if isInstalled != (aliasErr == nil) {
		t.Fatalf("installed=%v, but the app's alias folder says %v", isInstalled, aliasErr == nil)
	}
	if !isInstalled {
		if path != "" {
			t.Fatalf("absent app named %q", path)
		}
		return
	}
	if !strings.EqualFold(filepath.Base(path), "codex.exe") {
		t.Fatalf("desktop codex = %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
