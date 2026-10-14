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

func TestSignedInReadsOnlyAReadyPageAddressCheckAndItsAccount(t *testing.T) {
	for _, test := range []struct {
		name       string
		isSignedIn bool
		account    string
	}{
		{"signed out", false, ""},
		{"signed in with no account in the address", true, ""},
		{"signed in on an account", true, "acct_fixture"},
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
					ID     int    `json:"id"`
					Method string `json:"method"`
					Params struct {
						Expression string `json:"expression"`
					} `json:"params"`
				}
				if err := json.Unmarshal(data, &command); err != nil {
					t.Error(err)
					return
				}
				if command.Method != "Runtime.evaluate" || !strings.Contains(command.Params.Expression, "document.readyState") || !strings.Contains(command.Params.Expression, "location.pathname") || !strings.Contains(command.Params.Expression, `"account":"^/(acct_[a-z]+)/test/apikeys$"`) || strings.Contains(command.Params.Expression, "document.body") || strings.Contains(command.Params.Expression, "location.href") {
					t.Error("the sign-in watcher read more than page readiness and its reviewed address")
				}
				result := []byte(fmt.Sprintf(`{"id":%d,"result":{"result":{"type":"object","value":{"signedIn":%t,"account":%q}}}}`, command.ID, test.isSignedIn, test.account))
				_ = connection.Write(r.Context(), websocket.MessageText, result)
			}))
			defer page.Close()
			address = page.URL
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			got, account, err := SignedIn(ctx, address, keypages.Provider{KeyPage: "https://dashboard.stripe.com/test/apikeys", SignedInPath: "^/(acct_[a-z]+/)?test/apikeys$", AccountPath: "^/(acct_[a-z]+)/test/apikeys$"})

			if err != nil || got != test.isSignedIn || account != test.account {
				t.Fatalf("SignedIn = %t, %q, %v, want %t, %q", got, account, err, test.isSignedIn, test.account)
			}
		})
	}
}
