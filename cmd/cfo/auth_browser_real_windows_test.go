package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/execx"
)

// TestAHardenedBridgeDrivesALentBrowserThroughItsOwnerOnlyTransport is the
// proof, with a real chrome-devtools-axi, that the handoff `cfo auth browser`
// makes reaches a login's Chrome: the bridge reports the windows-user-v1
// transport, takes the lent browser's address, opens a stand-in key page in
// that browser through the transport only this Windows user may use, and
// lets go of it again.
//
// It needs a chrome-devtools-axi that ships the secured bridge, which is not
// what CI installs, so it runs only when asked:
//
//	CFO_AXI_REAL=1 CFO_AXI_PREFIX=<folder holding that chrome-devtools-axi.cmd>
//	CFO_AXI_RESULTS=<an empty directory for the bridge's state and the profile>
func TestAHardenedBridgeDrivesALentBrowserThroughItsOwnerOnlyTransport(t *testing.T) {
	if os.Getenv("CFO_AXI_REAL") != "1" {
		t.Skip("set CFO_AXI_REAL=1 with CFO_AXI_PREFIX and CFO_AXI_RESULTS to prove the handoff with a real secured chrome-devtools-axi")
	}
	prefix, results := os.Getenv("CFO_AXI_PREFIX"), os.Getenv("CFO_AXI_RESULTS")
	if prefix == "" || results == "" {
		t.Fatal("CFO_AXI_PREFIX and CFO_AXI_RESULTS are both required")
	}

	// Arrange: a login's Chrome lent as the broker lends it, reaching a
	// stand-in key page, then the secured tool first on the path with its
	// state in a home of the test's own.
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>Stand-in key page</title><h1>Stand-in API keys</h1><p>Secret key, hidden</p>`)
	}))
	defer page.Close()
	pageURL := strings.Replace(page.URL, "127.0.0.1", "localhost", 1) + "/test/apikeys"
	executable, err := browserlogin.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	lent, err := browserlogin.Start(ctx, browserlogin.Launch{Executable: executable, Profile: t.TempDir(), Hosts: []string{"localhost"}, Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := browserlogin.End(context.Background(), lent); err != nil {
			t.Error(err)
		}
	}()
	t.Setenv("PATH", prefix+string(os.PathListSeparator)+os.Getenv("PATH"))
	tool, err := exec.LookPath("chrome-devtools-axi")
	if err != nil || !strings.EqualFold(filepath.Dir(tool), filepath.Clean(prefix)) {
		t.Fatalf("chrome-devtools-axi resolves to %q (%v), want the one in %s", tool, err, prefix)
	}
	profileHome := filepath.Join(results, "user-profile")
	if err := os.MkdirAll(profileHome, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("USERPROFILE", profileHome)
	t.Setenv("HOME", profileHome)
	t.Setenv("CHROME_DEVTOOLS_AXI_SESSION", fmt.Sprintf("axi-proof-%d", time.Now().UnixNano()))
	t.Setenv("CHROME_DEVTOOLS_AXI_HEADED", "")
	hand := handBrowserSession(execx.OSRunner{})

	// Act
	defer func() {
		if err := hand(context.Background(), ""); err != nil {
			t.Errorf("letting go of the lent browser: %v", err)
		}
	}()
	if err := hand(ctx, lent.URL); err != nil {
		t.Fatalf("handoff to the secured bridge = %v", err)
	}
	opened := runAxi(ctx, t, "open", pageURL)
	snapshot := runAxi(ctx, t, "snapshot")

	// Assert
	if !strings.Contains(opened+snapshot, "Stand-in API keys") {
		t.Fatalf("the bridge did not show the stand-in page: open said %q, snapshot said %q", opened, snapshot)
	}
	if !lentBrowserShows(t, lent.URL, pageURL) {
		t.Fatal("the stand-in page is not open in the lent browser, so the bridge drove a browser of its own")
	}
}

func runAxi(ctx context.Context, t *testing.T, args ...string) string {
	t.Helper()
	result, err := execx.OSRunner{}.Run(ctx, execx.Request{Name: "chrome-devtools-axi", Args: args, KillTree: true})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("chrome-devtools-axi %s = exit %d, %v: %s", strings.Join(args, " "), result.ExitCode, err, result.Stderr)
	}
	return string(result.Stdout)
}

// lentBrowserShows reads the lent browser's pages over its own transport,
// which answers this Windows user alone.
func lentBrowserShows(t *testing.T, address, pageURL string) bool {
	t.Helper()
	response, err := http.Get(address + "/json/list")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var targets []struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	}
	if err := json.NewDecoder(response.Body).Decode(&targets); err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		if target.Type == "page" && target.URL == pageURL {
			return true
		}
	}
	return false
}
