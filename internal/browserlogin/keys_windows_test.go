package browserlogin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/cdp"
	"github.com/fpresta0607/code-goblins/internal/keypages"
)

// keyCanary is a key no page, error or file holds unless the code under
// test put it there.
func keyCanary(t *testing.T, prefix string) string {
	t.Helper()
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	return prefix + "canary" + hex.EncodeToString(random[:])
}

// standInKeyPage is a key page in the shape of Stripe's: the publishable
// key shown, the secret key masked until a person's click on its Reveal
// control asks the page's server for it. page is the page's body, which
// may change the rows, and account is the account the server keeps the
// browser on.
type standInKeyPage struct {
	server     *httptest.Server
	reveals    atomic.Int32
	isSignedIn bool
	account    string
	secret     string
	page       string
}

const standInKeys = `<!doctype html><title>API keys</title>
<table>
<tr><th>Publishable key</th><td><code>PUBLISHABLE</code></td></tr>
<tr><th>Secret key</th><td><code id="secret">sk_test_••••••••4abc</code> <button id="reveal">Reveal test key</button></td></tr>
<tr><th>Restricted key</th><td><button>Roll key</button></td></tr>
</table>
<script>
document.getElementById("reveal").addEventListener("click", async (event) => {
	if (!event.isTrusted) return;
	const answer = await fetch("/reveal", {method: "POST"});
	document.getElementById("secret").textContent = (await answer.json()).secret;
	event.target.remove();
});
</script>`

func newStandInKeyPage(t *testing.T, publishable, secret string) *standInKeyPage {
	t.Helper()
	keys := &standInKeyPage{isSignedIn: true, account: "acct_standin", secret: secret, page: strings.Replace(standInKeys, "PUBLISHABLE", publishable, 1)}
	keys.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		switch {
		case r.URL.Path == "/reveal" && r.Method == http.MethodPost:
			keys.reveals.Add(1)
			fmt.Fprintf(w, `{"secret":%q}`, keys.secret)
		case !keys.isSignedIn:
			if r.URL.Path != "/login" {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			fmt.Fprint(w, `<!doctype html><title>Sign in</title><button>Sign in</button>`)
		case r.URL.Path == "/"+keys.account+"/test/apikeys":
			fmt.Fprint(w, keys.page)
		case strings.HasSuffix(r.URL.Path, "/test/apikeys"):
			http.Redirect(w, r, "/"+keys.account+"/test/apikeys", http.StatusSeeOther)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(keys.server.Close)
	return keys
}

// provider is the stand-in's recipe, on localhost with the server's port.
func (k *standInKeyPage) provider() keypages.Provider {
	address := strings.Replace(k.server.URL, "127.0.0.1", "localhost", 1)
	return keypages.Provider{ID: "standin", Name: "Stand-in", Hosts: []string{"localhost"}, KeyPage: address + "/test/apikeys", SignedInPath: "^/(acct_[a-z]+/)?test/apikeys$", AccountPath: "^/(acct_[a-z]+)/test/apikeys$"}
}

// busyPage keeps a page's main thread busy 1.5 s at a time, so each read of
// the page waits on it and a wait's bound runs out inside one, as a slow
// machine makes it do now and then.
const busyPage = `<script>setInterval(() => { const end = Date.now() + 1500; while (Date.now() < end) {} }, 0);</script>`

// stuckPage keeps a page's main thread busy while it loads for longer than
// a wait's whole bound, so the signed-in wait's bound runs out inside a
// read of it.
var stuckPage = fmt.Sprintf(`<script>{ const end = Date.now() + %d; while (Date.now() < end) {} }</script>`, (keyWait + 5*time.Second).Milliseconds())

var standInRecipeKeys = []keypages.Key{
	{Name: "STRIPE_TEST_PUBLISHABLE_KEY", Row: "Publishable key", Prefix: "pk_test_"},
	{Name: "STRIPE_TEST_SECRET_KEY", Row: "Secret key", Reveal: "Reveal test key", Prefix: "sk_test_"},
}

// openPages are the browser's pages: each one's address, and its document,
// once it finished loading.
func openPages(t *testing.T, address string) map[string]string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tabs, err := pages(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	documents := map[string]string{}
	for _, tab := range tabs {
		connection, err := cdp.Dial(ctx, tab.Socket)
		if err != nil {
			t.Fatal(err)
		}
		var answer struct {
			Result struct {
				Value []string `json:"value"`
			} `json:"result"`
		}
		for ctx.Err() == nil {
			err = connection.Call(ctx, "Runtime.evaluate", map[string]any{"expression": `document.readyState === "complete" ? [location.href, document.documentElement.outerHTML] : []`, "returnByValue": true}, &answer)
			if err == nil && len(answer.Result.Value) == 2 {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		connection.Close()
		if len(answer.Result.Value) != 2 {
			t.Fatal("a page never finished loading")
		}
		documents[tab.ID] = answer.Result.Value[0] + "\n" + answer.Result.Value[1]
	}
	return documents
}

func TestReadKeysRevealsAndReadsOnlyTheRecipesKeysInATabItCloses(t *testing.T) {
	// Arrange
	publishable, secret := keyCanary(t, "pk_test_"), keyCanary(t, "sk_test_")
	keys := newStandInKeyPage(t, publishable, secret)
	process, _ := startChrome(t, []string{"localhost"})
	// The goblin holding the browser has a page of its own open.
	if failed, _ := navigate(t, process.URL, strings.Replace(keys.server.URL, "127.0.0.1", "localhost", 1)+"/the-goblins-page"); failed != "" {
		t.Fatalf("the goblin's page did not load: %s", failed)
	}
	before := openPages(t, process.URL)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Act
	values, err := ReadKeys(ctx, process.URL, keys.provider(), "acct_standin", standInRecipeKeys)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if values["STRIPE_TEST_PUBLISHABLE_KEY"] != publishable || values["STRIPE_TEST_SECRET_KEY"] != secret || len(values) != 2 {
		t.Fatal("the fetch did not read the two stand-in keys")
	}
	if keys.reveals.Load() != 1 {
		t.Fatalf("reveals = %d, want the one trusted click", keys.reveals.Load())
	}
	after := openPages(t, process.URL)
	if len(after) != len(before) {
		t.Fatalf("pages %d after the fetch, %d before, want the fetch's tab closed", len(after), len(before))
	}
	for id, document := range after {
		if document != before[id] || strings.Contains(document, secret) {
			t.Fatal("a page the fetch did not open changed, or a page still shows the revealed key")
		}
	}
}

func TestReadKeysRefusesPagesItMustNotReadWithoutRepeatingThem(t *testing.T) {
	process, _ := startChrome(t, []string{"localhost"})
	for _, test := range []struct {
		name       string
		change     func(*standInKeyPage)
		want       error
		isRevealed bool
	}{
		{"signed out", func(k *standInKeyPage) { k.isSignedIn = false }, ErrSignedOut, false},
		{"a key page too busy to settle in time", func(k *standInKeyPage) {
			k.page = strings.Replace(k.page, `</title>`, `</title>`+stuckPage, 1)
		}, errKeyPageSlow, false},
		{"another account", func(k *standInKeyPage) { k.account = "acct_other" }, nil, false},
		{"two reveal controls", func(k *standInKeyPage) {
			k.page = strings.Replace(k.page, `<button>Roll key</button>`, `<button>Reveal test key</button>`, 1)
		}, ErrPageChanged, false},
		{"no secret key row", func(k *standInKeyPage) {
			k.page = strings.Replace(k.page, `<th>Secret key</th>`, `<th>Signing key</th>`, 1)
		}, ErrPageChanged, false},
		{"no secret key row on a busy page", func(k *standInKeyPage) {
			k.page = strings.Replace(k.page, `<th>Secret key</th>`, `<th>Signing key</th>`, 1) + busyPage
		}, ErrPageChanged, false},
		{"a key that stays masked", func(k *standInKeyPage) { k.secret = "sk_test_••••••••4abc" }, errKeyMasked, true},
		{"a key that stays masked on a busy page", func(k *standInKeyPage) {
			k.secret = "sk_test_••••••••4abc"
			k.page += busyPage
		}, errKeyMasked, true},
		{"two different keys beside one row", func(k *standInKeyPage) {
			k.page = strings.Replace(k.page, `<button id="reveal">`, `<code>`+keyCanary(t, "sk_test_")+`</code> <button id="reveal">`, 1)
		}, ErrPageChanged, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			publishable, secret := keyCanary(t, "pk_test_"), keyCanary(t, "sk_test_")
			keys := newStandInKeyPage(t, publishable, secret)
			test.change(keys)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			// Act
			values, err := ReadKeys(ctx, process.URL, keys.provider(), "acct_standin", standInRecipeKeys)

			// Assert
			if err == nil || values != nil {
				t.Fatal("the fetch read a page it must not read")
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("err = %v, want %v", err, test.want)
			}
			if strings.Contains(err.Error(), publishable) || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "4abc") {
				t.Fatal("the refusal repeated a key from the page")
			}
			if isRevealed := keys.reveals.Load() > 0; isRevealed != test.isRevealed {
				t.Fatalf("revealed = %t, want %t", isRevealed, test.isRevealed)
			}
		})
	}
}

// A wait's own bound running out inside a read decides the wait's answer,
// and the caller's running out does not.
func TestIsWaitOverOnlyWhenTheWaitsOwnBoundRanOut(t *testing.T) {
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	live := context.Background()
	for _, test := range []struct {
		name         string
		wait, caller context.Context
		isOver       bool
	}{
		{"the wait's bound ran out", ended, live, true},
		{"the caller's ran out too", ended, ended, false},
		{"neither ran out", live, live, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if isOver := isWaitOver(test.wait, test.caller); isOver != test.isOver {
				t.Fatalf("over = %t, want %t", isOver, test.isOver)
			}
		})
	}
}

// A tab closeTab closed is gone from the browser's list when it returns.
// Chrome answers a close before the tab is gone and lists it for a moment
// after, where it answers nothing, so a reader of the pages that came next
// waited on it until its bound ran out.
func TestAClosedTabIsNoLongerListedOnceCloseTabReturns(t *testing.T) {
	// Arrange
	process, _ := startChrome(t, []string{"localhost"})
	for range 5 {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		id, connection, err := openTab(ctx, process.URL)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		connection.Close()

		// Act
		err = closeTab(ctx, process.URL, id)

		// Assert
		tabs, listErr := pages(ctx, process.URL)
		cancel()
		if err != nil || listErr != nil {
			t.Fatal(err, listErr)
		}
		if slices.ContainsFunc(tabs, func(tab page) bool { return tab.ID == id }) {
			t.Fatalf("tab %s is still listed after closeTab returned", id)
		}
	}
}
