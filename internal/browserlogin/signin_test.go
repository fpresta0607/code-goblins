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

func TestSignedInReadsOnlyAReadyPageAddressCheck(t *testing.T) {
	for _, isSignedIn := range []bool{false, true} {
		t.Run(map[bool]string{false: "signed out", true: "signed in"}[isSignedIn], func(t *testing.T) {
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
				if command.Method != "Runtime.evaluate" || !strings.Contains(command.Params.Expression, "document.readyState") || !strings.Contains(command.Params.Expression, "location.pathname") || strings.Contains(command.Params.Expression, "document.body") || strings.Contains(command.Params.Expression, "location.href") {
					t.Error("the sign-in watcher read more than page readiness and its reviewed address")
				}
				result := []byte(fmt.Sprintf(`{"id":%d,"result":{"result":{"type":"boolean","value":%t}}}`, command.ID, isSignedIn))
				_ = connection.Write(r.Context(), websocket.MessageText, result)
			}))
			defer page.Close()
			address = page.URL
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			got, err := SignedIn(ctx, address, keypages.Provider{KeyPage: "https://dashboard.stripe.com/test/apikeys", SignedInPath: "^/test/apikeys$"})

			if err != nil || got != isSignedIn {
				t.Fatalf("SignedIn = %t, %v, want %t", got, err, isSignedIn)
			}
		})
	}
}
