package browserlogin

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/sys/windows"
)

func TestDevToolsTransportProvesTheConnectingWindowsUser(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, owner string
		isAllowed   bool
	}{
		{"same user", user.User.Sid.String(), true},
		{"different user", "S-1-5-21-1-2-3-9999", false},
		{"missing owner", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			called := make(chan bool, 2)
			server := httptest.NewUnstartedServer(ownerOnlyDevTools(test.owner, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called <- true
				if r.URL.Path == "/devtools/browser" {
					connection, err := websocket.Accept(w, r, nil)
					if err == nil {
						connection.CloseNow()
					}
					return
				}
				io.WriteString(w, "synthetic browser metadata")
			})))
			server.Listener.Close()
			server.Listener = listener
			server.Start()
			defer server.Close()
			client := &http.Client{Timeout: 5 * time.Second}

			response, err := client.Get(server.URL + "/json/version")
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			want := http.StatusForbidden
			if test.isAllowed {
				want = http.StatusOK
			}
			if response.StatusCode != want {
				t.Errorf("metadata status = %d, want %d", response.StatusCode, want)
			}
			connection, response, err := websocket.Dial(t.Context(), "ws://"+listener.Addr().String()+"/devtools/browser", nil)
			if connection != nil {
				connection.CloseNow()
			}
			if test.isAllowed && err != nil {
				t.Fatal(err)
			}
			if !test.isAllowed && (err == nil || response == nil || response.StatusCode != http.StatusForbidden) {
				t.Fatal("an unauthorized websocket was not refused")
			}
			if !test.isAllowed && len(called) != 0 {
				t.Fatal("an unauthorized request reached DevTools")
			}
		})
	}
}

func TestDevToolsTransportRefusesUnprovenConnections(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	handler := ownerOnlyDevTools(user.User.Sid.String(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("unproven request reached DevTools") }))
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/json/version", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("unproven status = %d", response.Code)
	}
}
