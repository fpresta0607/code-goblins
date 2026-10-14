package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/cdp"
	"github.com/fpresta0607/code-goblins/internal/keypages"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func TestAKeyFetchAfterHisSignInReachesOnlyTheVault(t *testing.T) {
	onAHiddenDesktop(t, proveKeyFetch)
}

// proveKeyFetch runs the whole fetch against stand-in login and key pages:
// a goblin's fetch meets a signed-out page and files his sign-in card, his
// click opens the window, a sign-in there completes the card, and the fetch
// carries on headless, reveals the secret key with a person's click, and
// stores both keys. Each key is then in the vault and in no file of the
// home, its login's profile included, no answer to the goblin and no
// notice to the CFO.
func proveKeyFetch(t *testing.T) {
	publishable, secret := "pk"+"_test_"+strings.TrimPrefix(newCanary(t), "sk_test_"), newCanary(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		_, err := r.Cookie("standin-login")
		isSignedIn := err == nil
		switch {
		case r.URL.Path == "/signed-in":
			http.SetCookie(w, &http.Cookie{Name: "standin-login", Value: "accepted", MaxAge: 3600, Path: "/", HttpOnly: true})
			http.Redirect(w, r, "/acct_standin/test/apikeys", http.StatusSeeOther)
		case !isSignedIn:
			if r.URL.Path != "/login" {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			fmt.Fprint(w, `<!doctype html><title>Sign in</title><a href="/signed-in">Sign in to the stand-in</a>`)
		case r.URL.Path == "/reveal" && r.Method == http.MethodPost:
			fmt.Fprintf(w, `{"secret":%q}`, secret)
		case r.URL.Path == "/test/apikeys":
			http.Redirect(w, r, "/acct_standin/test/apikeys", http.StatusSeeOther)
		case r.URL.Path == "/acct_standin/test/apikeys":
			fmt.Fprintf(w, `<!doctype html><title>API keys</title>
<table>
<tr><th>Publishable key</th><td><code>%s</code></td></tr>
<tr><th>Secret key</th><td><code id="secret">sk_test_••••••••4abc</code> <button id="reveal">Reveal test key</button></td></tr>
</table>
<script>
document.getElementById("reveal").addEventListener("click", async (event) => {
	if (!event.isTrusted) return;
	const answer = await fetch("/reveal", {method: "POST"});
	document.getElementById("secret").textContent = (await answer.json()).secret;
	event.target.remove();
});
</script>`, publishable)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	address := strings.Replace(server.URL, "127.0.0.1", "localhost", 1)
	store, h := testStore(t)
	vault := t.TempDir()
	t.Setenv(auth.StoreDirEnv, vault)
	provider := keypages.Provider{ID: "standin", Name: "Stand-in", Hosts: []string{"localhost"}, KeyPage: address + "/test/apikeys", SignedInPath: "^/(acct_[a-z]+/)?test/apikeys$", AccountPath: "^/(acct_[a-z]+)/test/apikeys$", Keys: []keypages.Key{
		{Name: "STRIPE_TEST_PUBLISHABLE_KEY", Row: "Publishable key", Prefix: "pk_test_"},
		{Name: "STRIPE_TEST_SECRET_KEY", Row: "Secret key", Reveal: "Reveal test key", Prefix: "sk_test_"},
	}}
	broker := newBrowserBroker(h.State, h.Data, Options{KeyPages: keypages.Recipes{Providers: []keypages.Provider{provider}}, Dispatch: &Dispatch{Memory: func() (Memory, error) { return Memory{Available: 8 << 30, CommitAvailable: 8 << 30}, nil }}}, func(err error) { t.Error(err) })
	broker.live = func(string) error { return nil }
	service := &Service{Store: store, Instance: "test-instance", browsers: broker, Options: Options{
		Credentials:        auth.OpenStore,
		RefreshCredentials: func(context.Context, string) ([]string, error) { return []string{"pd-keys"}, nil },
	}}
	brokerTask(t, broker, "pd-keys", "throwaway")
	if err := state.WriteTaskCredentials(broker.state, "pd-keys", []string{"standin"}); err != nil {
		t.Fatal(err)
	}
	brokerManifest(t, broker, "throwaway", auth.Service{Name: "standin", Method: auth.MethodEnv, Env: []string{"STRIPE_TEST_PUBLISHABLE_KEY", "STRIPE_TEST_SECRET_KEY"}})
	t.Cleanup(func() {
		if err := broker.closeAll(); err != nil {
			t.Error(err)
		}
	})
	// A credential request names an https page, and the stand-in is plain
	// HTTP on this machine, so the request goes on the board as the board
	// takes one, past the link check alone.
	now := time.Now().UTC()
	request, err := store.acceptCredential(CredentialRequest{ID: "cred-0123456789abcdef", Generation: "fetch-proof", Identity: strings.Repeat("c", 64), By: "cfo", Task: "pd-keys", Project: "throwaway", Names: []string{"STRIPE_TEST_PUBLISHABLE_KEY", "STRIPE_TEST_SECRET_KEY"}, Why: "Charge test cards in the checkout tests", Link: provider.KeyPage, State: "open", CreatedAt: now, ExpiresAt: now.Add(credentialLifetime)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// The goblin's fetch finds no signed-in login and files his card.
	asked, err := service.fetchCredential(ctx, BrowserRequest{Action: "fetch", Task: "pd-keys", Request: request.ID})
	if err != nil || asked.SignIn == "" {
		t.Fatalf("first fetch = %+v, %v, want his sign-in card", asked, err)
	}
	card := store.Snapshot().BrowserSignIns[0]
	handler := NewHTTP(service, credentialBoardHost, nil)
	click := httptest.NewRequest(http.MethodPost, "http://"+credentialBoardHost+"/api/browser-signins/open", strings.NewReader(fmt.Sprintf(`{"id":%q,"generation":%q}`, card.ID, card.Generation)))
	click.RemoteAddr = "127.0.0.1:8000"
	click.Header.Set("Origin", "http://"+credentialBoardHost)
	click.Header.Set("X-CFO-Token", service.Instance)
	click.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, click)
	if response.Code != http.StatusOK {
		t.Fatalf("his click = %d: %s", response.Code, response.Body.String())
	}
	if err := service.processBrowserSignIns(ctx); err != nil {
		t.Fatal(err)
	}

	// He signs in, in the window, as the stand-in's sign-in link does.
	window := broker.processes[card.Login]
	pageList, err := http.Get(window.URL + "/json/list")
	if err != nil {
		t.Fatal(err)
	}
	var pages []struct {
		Socket string `json:"webSocketDebuggerUrl"`
		Type   string `json:"type"`
	}
	err = json.NewDecoder(pageList.Body).Decode(&pages)
	pageList.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	socket := ""
	for _, page := range pages {
		if page.Type == "page" {
			socket = page.Socket
			break
		}
	}
	if socket == "" {
		t.Fatal("his window has no page")
	}
	connection, err := cdp.Dial(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	err = connection.Call(ctx, "Page.navigate", map[string]string{"url": address + "/signed-in"}, nil)
	connection.Close()
	if err != nil {
		t.Fatal(err)
	}
	for ctx.Err() == nil && store.Snapshot().BrowserSignIns[0].State == "open" {
		time.Sleep(100 * time.Millisecond)
		if err := service.processBrowserSignIns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if card := store.Snapshot().BrowserSignIns[0]; card.State != "signed_in" {
		t.Fatalf("card = %+v, want signed in", card)
	}

	// The fetch carries on by itself once the window is gone.
	service.credentialWork.Wait()
	broker.work.Wait()

	for name, value := range map[string]string{"STRIPE_TEST_PUBLISHABLE_KEY": publishable, "STRIPE_TEST_SECRET_KEY": secret} {
		stored, err := os.ReadFile(filepath.Join(vault, "throwaway", name))
		if err != nil || string(stored) != value {
			t.Fatalf("%s is not in the vault after the fetch: %v", name, err)
		}
		if found := filesHolding(t, h.Root, value); len(found) != 0 {
			t.Fatalf("%s reached files of the home: %v", name, found)
		}
		if data, _ := json.Marshal(asked); strings.Contains(string(data), value) {
			t.Fatalf("%s reached the goblin's answer", name)
		}
	}
	saved, found := store.credential(request.ID)
	if !found || saved.State != "saved" || !slices.Equal(saved.Fetched, []string{"STRIPE_TEST_PUBLISHABLE_KEY", "STRIPE_TEST_SECRET_KEY"}) {
		t.Fatalf("request = %+v, want it saved with both keys fetched", saved)
	}
	logins, err := browserlogin.Read(h.State)
	if err != nil || len(logins) != 1 || logins[0].AccountID != "acct_standin" || logins[0].Browser != nil {
		t.Fatalf("logins = %+v, %v, want his account recorded and no browser left", logins, err)
	}
	pending, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || !strings.Contains(pending[0].Detail, "fetched from the key page into throwaway's vault") || strings.Contains(pending[0].Detail, publishable) || strings.Contains(pending[0].Detail, secret) {
		t.Fatalf("wakes = %+v, want one notice of the names fetched and no key", pending)
	}
}
