package browserlogin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/fpresta0607/code-goblins/internal/keypages"
)

func TestSignedInRetriesAChangingPageAndRefusesOtherProtocolFailures(t *testing.T) {
	for _, test := range []struct {
		name, answer string
		shouldFail   bool
	}{
		{"navigation destroys context", `"error":{"code":-32000,"message":"Execution context was destroyed."}`, false},
		{"navigation replaces context", `"error":{"code":-32000,"message":"Cannot find context with specified id"}`, false},
		{"unrelated protocol refusal", `"error":{"code":-32000,"message":"private-page-text"}`, true},
		{"script exception", `"result":{"exceptionDetails":{"text":"private-page-text"}}`, true},
		{"wrong result type", `"result":{"result":{"type":"string","value":"private-page-text"}}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var address string
			page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/json/list" {
					_ = json.NewEncoder(w).Encode([]map[string]string{{"type": "page", "webSocketDebuggerUrl": "ws" + strings.TrimPrefix(address, "http") + "/page"}})
					return
				}
				connection, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer connection.CloseNow()
				_, data, err := connection.Read(r.Context())
				if err != nil {
					t.Error(err)
					return
				}
				var command struct {
					ID int `json:"id"`
				}
				if err := json.Unmarshal(data, &command); err != nil {
					t.Error(err)
					return
				}
				_ = connection.Write(r.Context(), websocket.MessageText, []byte(fmt.Sprintf(`{"id":%d,%s}`, command.ID, test.answer)))
			}))
			defer page.Close()
			address = page.URL
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			isSignedIn, err := SignedIn(ctx, address, keypages.Provider{KeyPage: "https://dashboard.stripe.com/test/apikeys", SignedInPath: "^/test/apikeys$"})

			if isSignedIn || (err != nil) != test.shouldFail {
				t.Fatalf("SignedIn = %t, %v, want false with failure %t", isSignedIn, err, test.shouldFail)
			}
			if err != nil && strings.Contains(err.Error(), "private-page-text") {
				t.Fatal("the watcher exposed protocol content")
			}
		})
	}
}

func TestSignedInRefusesUnusablePageLists(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		shouldFail bool
	}{
		{"unavailable list", `[]`, http.StatusServiceUnavailable, true},
		{"malformed list", `{`, http.StatusOK, true},
		{"no pages", `[]`, http.StatusOK, false},
		{"worker target", `[{"type":"worker"}]`, http.StatusOK, false},
		{"foreign socket", `[{"type":"page","webSocketDebuggerUrl":"ws://example.invalid/private-page-text"}]`, http.StatusOK, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer page.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			isSignedIn, err := SignedIn(ctx, page.URL, keypages.Provider{KeyPage: "https://dashboard.stripe.com/test/apikeys", SignedInPath: "^/test/apikeys$"})

			if isSignedIn || (err != nil) != test.shouldFail {
				t.Fatalf("SignedIn = %t, %v, want false with failure %t", isSignedIn, err, test.shouldFail)
			}
			if err != nil && strings.Contains(err.Error(), "private-page-text") {
				t.Fatal("the watcher exposed a page list's content")
			}
		})
	}
}
