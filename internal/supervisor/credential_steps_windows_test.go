package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/cdp"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// A goblin finishes or withdraws its own card from its own terminal, the
// CFO withdraws any card, and nobody else touches one.
func TestTheAskerOrTheCFOClosesACardOverThePipe(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	t.Setenv(auth.StoreDirEnv, t.TempDir())
	_, _, _, cfo := primaryFixture(t, store)
	service := &Service{Store: store, Options: Options{CFO: cfo, Credentials: auth.OpenStore}}
	runPipe(t, service)
	meta, _, _, _ := goblinFixture(t, store)
	file := func(why string) CredentialRequest {
		t.Helper()
		filed, err := FileCredentialRequest(h, CredentialRequest{Task: meta.ID, Project: "throwaway", Why: why, Link: "https://example.com/signup"})
		if err != nil {
			t.Fatal(err)
		}
		if err := service.ingestCredentialRequests(); err != nil {
			t.Fatal(err)
		}
		return filed
	}
	state := func(id string) string {
		t.Helper()
		request, found := store.credential(id)
		if !found {
			t.Fatalf("card %s is not on the board", id)
		}
		return request.State
	}
	finished, withdrawn, byCFO := file("Create the shop's test account"), file("Confirm the account's email"), file("Accept the shop's terms")

	// Act
	strangerErr := StepCredential(h, CredentialStep{ID: finished.ID, Task: "task-9", Action: "done"})
	doneErr := StepCredential(h, CredentialStep{ID: finished.ID, Task: meta.ID, Action: "done"})
	withdrawErr := StepCredential(h, CredentialStep{ID: withdrawn.ID, Task: meta.ID, Action: "withdraw", Reason: "The account already existed"})
	cfoErr := StepCredential(h, CredentialStep{ID: byCFO.ID, Action: "withdraw", Reason: "Not needed any more"})
	againErr := StepCredential(h, CredentialStep{ID: finished.ID, Task: meta.ID, Action: "withdraw", Reason: "late"})

	// Assert
	if strangerErr == nil {
		t.Fatal("a task that did not ask closed the card")
	}
	if doneErr != nil || withdrawErr != nil || cfoErr != nil {
		t.Fatalf("done = %v, withdraw = %v, the CFO's withdraw = %v", doneErr, withdrawErr, cfoErr)
	}
	if againErr == nil {
		t.Fatal("a closed card was withdrawn again")
	}
	if got := []string{state(finished.ID), state(withdrawn.ID), state(byCFO.ID)}; strings.Join(got, " ") != "done withdrawn withdrawn" {
		t.Fatalf("states = %v, want done, withdrawn, withdrawn", got)
	}
	if request, _ := store.credential(withdrawn.ID); request.Reason != "The account already existed" {
		t.Fatalf("reason = %q, want the goblin's", request.Reason)
	}
}

// A card that brings the goblin's own browser window to him goes from
// requested to open when the goblin's wait shows the window, and back to
// offered, saying why, when it could not.
func TestTheGoblinReportsItsWindowOverThePipe(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	t.Setenv(auth.StoreDirEnv, t.TempDir())
	service := &Service{Store: store, Options: Options{Credentials: auth.OpenStore}}
	runPipe(t, service)
	meta, _, _, _ := goblinFixture(t, store)
	filed, err := FileCredentialRequest(h, CredentialRequest{Task: meta.ID, Project: "throwaway", Why: "Sign in to the shop", Link: "https://example.com/login", Window: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ingestCredentialRequests(); err != nil {
		t.Fatal(err)
	}
	request, _ := store.credential(filed.ID)
	if err := service.requestCredentialWindow(request.ID, request.Generation); err != nil {
		t.Fatal(err)
	}

	// Act
	openErr := StepCredential(h, CredentialStep{ID: filed.ID, Task: meta.ID, Action: "window-open"})
	opened, _ := store.credential(filed.ID)
	failedErr := StepCredential(h, CredentialStep{ID: filed.ID, Task: meta.ID, Action: "window-failed", Reason: "Chrome did not start"})
	failed, _ := store.credential(filed.ID)

	// Assert
	if openErr != nil || failedErr != nil {
		t.Fatalf("open = %v, failed = %v", openErr, failedErr)
	}
	if opened.WindowState != "open" {
		t.Fatalf("after the window opened = %q, want open", opened.WindowState)
	}
	if failed.WindowState != "" || failed.Reason != "Chrome did not start" || failed.State != "open" {
		t.Fatalf("after it failed = %+v, want the button offered again with the reason", failed)
	}
}

// End to end: a goblin in a scratch home drives a real headless Chrome to a
// sign-up page that asks to prove a human is there, which only the Overlord
// may do. It files a card whose link is that exact page, waits on it, and
// goes on in its browser the moment the test presses Done on the board.
func TestAGoblinsHeadlessBrowserReachesHisStepAndGoesOnAtDone(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	t.Setenv(auth.StoreDirEnv, t.TempDir())
	service := &Service{Store: store, Instance: "test-instance", Options: Options{Credentials: auth.OpenStore}}
	runPipe(t, service)
	handler := NewHTTP(service, credentialBoardHost, nil)
	meta, _, _, _ := goblinFixture(t, store)
	site := http.NewServeMux()
	site.HandleFunc("/signup", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<html><body><h1>Create your account</h1><p>Verify you are a human to continue.</p></body></html>`)
	})
	site.HandleFunc("/welcome", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<html><body><h1>Welcome to your new account</h1></body></html>`)
	})
	shop := httptest.NewServer(site)
	defer shop.Close()
	secureShop := httptest.NewTLSServer(site)
	defer secureShop.Close()
	executable, err := browserlogin.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	chrome, err := browserlogin.Start(ctx, browserlogin.Launch{Executable: executable, Profile: t.TempDir(), Headless: true, Page: shop.URL + "/signup"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := browserlogin.End(context.Background(), chrome); err != nil {
			t.Error(err)
		}
	})
	tab := firstTab(ctx, t, chrome.URL)
	defer tab.Close()

	// Act: the goblin reads the page and sees a step only he may take.
	text := pageText(ctx, t, tab, "Verify you are a human")
	filed, err := FileCredentialRequest(h, CredentialRequest{Task: meta.ID, Project: "throwaway", Why: "Create the shop's test account", Link: secureShop.URL + "/signup", Steps: []string{"Prove you are a human", "Create the account"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ingestCredentialRequests(); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		request CredentialRequest
		err     error
		at      time.Time
	}
	ended := make(chan outcome, 1)
	go func() {
		request, err := WaitCredential(ctx, h.State, filed.ID, nil)
		ended <- outcome{request, err, time.Now()}
	}()
	card, _ := store.credential(filed.ID)
	time.Sleep(500 * time.Millisecond)
	body, _ := json.Marshal(map[string]string{"id": card.ID, "generation": card.Generation, "action": "done"})
	pressed := time.Now()
	press := (&credentialBoard{t: t}).send(handler, "/api/credentials/close", string(body), nil)
	got := <-ended
	if got.err == nil {
		navigate(ctx, t, tab, shop.URL+"/welcome")
	}
	after := pageText(ctx, t, tab, "Welcome to your new account")

	// Assert
	if !strings.Contains(text, "Verify you are a human") {
		t.Fatalf("the goblin's browser shows %q", text)
	}
	if card.Link != secureShop.URL+"/signup" || card.State != "open" || card.Task != meta.ID {
		t.Fatalf("card = %+v, want the goblin's open card linking the exact page", card)
	}
	if press.Code != http.StatusOK || got.err != nil || got.request.State != "done" {
		t.Fatalf("Done = %d %s, wait = %+v %v", press.Code, press.Body, got.request, got.err)
	}
	if waited := got.at.Sub(pressed); waited > 2*time.Second {
		t.Fatalf("the goblin went on %s after Done, want within seconds", waited)
	}
	if !strings.Contains(after, "Welcome to your new account") {
		t.Fatalf("after Done the goblin's browser shows %q", after)
	}
	if pending, _ := wake.Pending(h.State); len(pending) == 0 {
		t.Fatal("the CFO was not told the step is done")
	}
}

// firstTab connects to the first page of the Chrome at address.
func firstTab(ctx context.Context, t *testing.T, address string) *cdp.Conn {
	t.Helper()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, address+"/json/list", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var tabs []struct {
		Type   string `json:"type"`
		Socket string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(response.Body).Decode(&tabs); err != nil {
		t.Fatal(err)
	}
	for _, tab := range tabs {
		if tab.Type == "page" {
			connection, err := cdp.Dial(ctx, tab.Socket)
			if err != nil {
				t.Fatal(err)
			}
			return connection
		}
	}
	t.Fatalf("the browser has no page: %+v", tabs)
	return nil
}

// pageText is the page's text once it holds want, or what it holds after
// ten seconds.
func pageText(ctx context.Context, t *testing.T, tab *cdp.Conn, want string) string {
	t.Helper()
	var text string
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		var result struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		}
		if err := tab.Call(ctx, "Runtime.evaluate", map[string]any{"expression": "document.body ? document.body.innerText : ''", "returnByValue": true}, &result); err != nil {
			t.Fatal(err)
		}
		if text = result.Result.Value; strings.Contains(text, want) {
			break
		}
	}
	return text
}

// navigate loads address in the tab.
func navigate(ctx context.Context, t *testing.T, tab *cdp.Conn, address string) {
	t.Helper()
	if err := tab.Call(ctx, "Page.navigate", map[string]string{"url": address}, nil); err != nil {
		t.Fatal(err)
	}
}
