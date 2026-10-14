package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserSignInOpenRequiresALocalCurrentClick(t *testing.T) {
	for _, test := range []struct {
		name, peer, header, value, body string
		status                          int
	}{
		{name: "local click", status: http.StatusOK},
		{name: "remote peer", peer: "192.0.2.1:2345", status: http.StatusForbidden},
		{name: "forwarded", header: "Forwarded", value: "for=127.0.0.1", status: http.StatusForbidden},
		{name: "forwarded for", header: "X-Forwarded-For", value: "127.0.0.1", status: http.StatusForbidden},
		{name: "missing token", header: "X-CFO-Token", status: http.StatusForbidden},
		{name: "foreign token", header: "X-CFO-Token", value: "old", status: http.StatusForbidden},
		{name: "foreign origin", header: "Origin", value: "https://example.invalid", status: http.StatusForbidden},
		{name: "missing origin", header: "Origin", status: http.StatusForbidden},
		{name: "not json", header: "Content-Type", value: "text/plain", status: http.StatusBadRequest},
		{name: "malformed json", body: "{", status: http.StatusBadRequest},
		{name: "unknown field", body: `{"id":"unused","generation":"old","command":"open"}`, status: http.StatusBadRequest},
		{name: "trailing json", body: `{} {}`, status: http.StatusBadRequest},
		{name: "oversized body", body: `{"id":"` + strings.Repeat("x", 4096) + `"}`, status: http.StatusBadRequest},
		{name: "stale generation", body: `{"id":"unused","generation":"old"}`, status: http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, chrome, _ := signInBoard(t)
			if _, err := service.requestBrowserSignIn(BrowserRequest{Task: "pd-keys", Provider: "stripe"}); err != nil {
				t.Fatal(err)
			}
			card := service.Store.Snapshot().BrowserSignIns[0]
			body := test.body
			if body == "" {
				body = fmt.Sprintf(`{"id":%q,"generation":%q}`, card.ID, card.Generation)
			}
			handler := NewHTTP(service, credentialBoardHost, nil)
			for range 2 {
				request := httptest.NewRequest(http.MethodPost, "http://"+credentialBoardHost+"/api/browser-signins/open", strings.NewReader(body))
				request.RemoteAddr = "127.0.0.1:2345"
				if test.peer != "" {
					request.RemoteAddr = test.peer
				}
				request.Header.Set("Origin", "http://"+credentialBoardHost)
				request.Header.Set("X-CFO-Token", service.Instance)
				request.Header.Set("Content-Type", "application/json")
				if test.header != "" {
					request.Header.Set(test.header, test.value)
				}
				response := httptest.NewRecorder()

				handler.ServeHTTP(response, request)

				if response.Code != test.status {
					t.Fatalf("status = %d, want %d", response.Code, test.status)
				}
			}
			if err := service.processBrowserSignIns(context.Background()); err != nil {
				t.Fatal(err)
			}
			if (len(chrome.launches) == 1) != (test.status == http.StatusOK) {
				t.Fatalf("browser launches = %d", len(chrome.launches))
			}
		})
	}
}

func TestBrowserSignInBoardItemsHideTheTaskProofAndBrowserAddress(t *testing.T) {
	service, _, _ := signInBoard(t)
	if _, err := service.requestBrowserSignIn(BrowserRequest{Task: "pd-keys", Provider: "stripe"}); err != nil {
		t.Fatal(err)
	}
	database := service.Store.Snapshot()
	database.BrowserSignIns[0].TaskGeneration = "private-task-proof"
	verified := service.browsers.now()
	database.BrowserSignIns[0].VerifiedAt = &verified

	data, err := json.Marshal(boardItems(database))

	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private-task-proof", "task_generation", "verified_at", "webSocketDebuggerUrl", "127.0.0.1:"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("the board exposed %s", forbidden)
		}
	}
}
