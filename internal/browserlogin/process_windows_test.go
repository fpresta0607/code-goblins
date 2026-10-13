package browserlogin

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/fpresta0607/code-goblins/internal/cdp"
	"github.com/fpresta0607/code-goblins/internal/execx"
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
	if process.PID != 0 {
		t.Cleanup(func() {
			if err := End(context.Background(), process); err != nil {
				t.Errorf("end chrome %d: %v", process.PID, err)
			}
		})
	}
	if err != nil {
		t.Fatal(err)
	}
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
	process, profile := startChrome(t, []string{"localhost"})

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

func TestPrivateChromeHasNoTCPListenerAndKeepsTwoClientsIndependent(t *testing.T) {
	process, profile := startChrome(t, []string{"localhost"})
	if _, err := os.Stat(filepath.Join(profile, activePortFile)); !os.IsNotExist(err) {
		t.Fatalf("Chrome published a raw debugger port file: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := execx.CommandContext(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", fmt.Sprintf("$ErrorActionPreference = 'Stop'; @(Get-NetTCPConnection -State Listen | Where-Object OwningProcess -eq %d).Count", process.PID))
	output, err := command.Output()
	if err != nil || strings.TrimSpace(string(output)) != "0" {
		t.Fatalf("Chrome's TCP listener check failed: %v, count %q", err, output)
	}
	socket, err := browserSocket(ctx, process.URL)
	if err != nil {
		t.Fatal(err)
	}
	first, err := cdp.Dial(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := cdp.Dial(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	finished := make(chan error, 2)
	for _, connection := range []*cdp.Conn{first, second} {
		go func() { finished <- connection.Call(ctx, "Browser.getVersion", nil, nil) }()
	}
	for range 2 {
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
	}
	first.Close()
	if err := second.Call(ctx, "Target.getTargets", nil, nil); err != nil {
		t.Fatal("closing one client broke the other: ", err)
	}
}

func TestPrivateChromeRoutesAutoAttachedPageCommandsAndEvents(t *testing.T) {
	process, _ := startChrome(t, []string{"localhost"})
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	socket, err := browserSocket(ctx, process.URL)
	if err != nil {
		t.Fatal(err)
	}
	connection, _, err := websocket.Dial(ctx, socket, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	if err := wsjson.Write(ctx, connection, devToolsMessage{ID: 1, Method: "Target.setAutoAttach", Params: json.RawMessage(`{"autoAttach":true,"waitForDebuggerOnStart":false,"flatten":true}`)}); err != nil {
		t.Fatal(err)
	}
	var session string
	var isAttached bool
	for !isAttached || session == "" {
		var message devToolsMessage
		if err := wsjson.Read(ctx, connection, &message); err != nil {
			t.Fatal(err)
		}
		if len(message.Error) != 0 {
			t.Fatal("Chrome refused auto attachment")
		}
		if message.ID == 1 {
			isAttached = true
		}
		if message.Method == "Target.attachedToTarget" {
			var attached struct {
				Session string                `json:"sessionId"`
				Target  struct{ Type string } `json:"targetInfo"`
			}
			if err := json.Unmarshal(message.Params, &attached); err != nil {
				t.Fatal(err)
			}
			if attached.Target.Type == "page" {
				if message.Session != "" {
					t.Fatal("the client's private root session leaked into an event")
				}
				session = attached.Session
			}
		}
	}
	if err := wsjson.Write(ctx, connection, devToolsMessage{ID: 2, Method: "Runtime.evaluate", Session: session, Params: json.RawMessage(`{"expression":"6 * 7","returnByValue":true}`)}); err != nil {
		t.Fatal(err)
	}
	var response devToolsMessage
	if err := wsjson.Read(ctx, connection, &response); err != nil {
		t.Fatal(err)
	}
	var result struct{ Result struct{ Value int } }
	if err := json.Unmarshal(response.Result, &result); err != nil || response.ID != 2 || response.Session != session || result.Result.Value != 42 {
		t.Fatal("an auto-attached page did not receive its command and reply")
	}
}

func TestAChromeThatCannotBeAskedToCloseIsEndedWithEverythingItStarted(t *testing.T) {
	process, profile := startChrome(t, []string{"localhost"})
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
	failed, text = navigate(t, process.URL, page.URL)
	if failed == "" && text == "the key page" {
		t.Fatal("an IP address outside the recipe reached the server")
	}
}
