package browserlogin

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/cdp"
)

// startChrome starts a headless Chrome on a fresh profile and ends it with
// the test.
func startChrome(t *testing.T, hosts []string) (Process, string) {
	t.Helper()
	executable, err := Executable()
	if err != nil {
		t.Fatal(err)
	}
	profile := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	process, err := Start(ctx, Launch{Executable: executable, Profile: profile, Hosts: hosts, Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := End(context.Background(), process); err != nil {
			t.Errorf("end chrome %d: %v", process.PID, err)
		}
	})
	return process, profile
}

// navigate opens url in the browser's first page and returns the error text
// Chrome gave for it, empty when it loaded, and the page's text.
func navigate(t *testing.T, address, url string) (string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	response, err := http.Get(address + "/json/list")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var targets []struct {
		Type   string `json:"type"`
		Socket string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(response.Body).Decode(&targets); err != nil {
		t.Fatal(err)
	}
	socket := ""
	for _, target := range targets {
		if target.Type == "page" {
			socket = target.Socket
			break
		}
	}
	if socket == "" {
		t.Fatal("chrome has no page")
	}
	conn, err := cdp.Dial(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var navigated struct {
		ErrorText string `json:"errorText"`
	}
	if err := conn.Call(ctx, "Page.navigate", map[string]string{"url": url}, &navigated); err != nil {
		t.Fatal(err)
	}
	if navigated.ErrorText != "" {
		return navigated.ErrorText, ""
	}
	for {
		var evaluated struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		}
		if err := conn.Call(ctx, "Runtime.evaluate", map[string]any{"expression": "document.readyState === 'complete' ? document.body.innerText : ''", "returnByValue": true}, &evaluated); err != nil {
			t.Fatal(err)
		}
		if evaluated.Result.Value != "" {
			return "", evaluated.Result.Value
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestStartedChromeAnswersOnItsProfilesPortAndClosesWhenAsked(t *testing.T) {
	process, profile := startChrome(t, nil)

	if !strings.HasPrefix(process.URL, "http://127.0.0.1:") {
		t.Fatalf("DevTools address = %q, want one on 127.0.0.1", process.URL)
	}
	if !Running(process) {
		t.Fatal("the started chrome is not running")
	}
	if err := End(context.Background(), process); err != nil {
		t.Fatal(err)
	}
	if Running(process) {
		t.Fatal("chrome still runs after End")
	}
	// Nothing of the Chrome holds a file of the profile, its crash
	// handler included, so Forget can delete it at once.
	if err := os.RemoveAll(profile); err != nil {
		t.Fatalf("the profile cannot be removed after End: %v", err)
	}
}

func TestAChromeThatCannotBeAskedToCloseIsEndedWithEverythingItStarted(t *testing.T) {
	process, profile := startChrome(t, nil)
	// Nothing answers at this address, so the browser is never asked to
	// close and only its job can end it.
	unreachable := process
	unreachable.URL = "http://127.0.0.1:1"

	if err := End(context.Background(), unreachable); err != nil {
		t.Fatal(err)
	}

	if Running(process) {
		t.Fatal("chrome still runs after End")
	}
	if err := os.RemoveAll(profile); err != nil {
		t.Fatalf("the profile cannot be removed after End: %v", err)
	}
}

func TestChromeOnHostRulesReachesOnlyTheRecipeHosts(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "the key page") }))
	defer page.Close()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(page.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	process, _ := startChrome(t, []string{"localhost"})

	failed, text := navigate(t, process.URL, "http://localhost:"+port+"/")
	if failed != "" || text != "the key page" {
		t.Fatalf("the recipe's host: error %q, text %q, want the page", failed, text)
	}
	failed, _ = navigate(t, process.URL, "http://elsewhere.localhost:"+port+"/")
	if failed != "net::ERR_NAME_NOT_RESOLVED" {
		t.Fatalf("a host outside the recipe: error %q, want net::ERR_NAME_NOT_RESOLVED", failed)
	}
}
