package supervisor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/install"
)

// A goblin's Claude Code posts its hooks to the board with no Origin and no
// board token, so the board hands a post under install.GoblinHookPrefix to
// its goblin hooks, which prove the goblin's terminal sent it, and only one
// from this PC: a web page, another machine, a proxy and the remote gate
// never reach them, and a post anywhere else still needs the board's token.
func TestTheBoardHandsAGoblinsHookPostToItsGoblinHooksAlone(t *testing.T) {
	for _, test := range []struct {
		name        string
		path        string
		change      func(*http.Request)
		isReached   bool
		wantRefusal int
	}{
		{"its goblin's Claude Code", install.GoblinToolPath, func(*http.Request) {}, true, 0},
		{"a web page", install.GoblinSealPath, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, false, http.StatusForbidden},
		{"another site's page", install.GoblinSealPath, func(r *http.Request) { r.Header.Set("Origin", "http://example.com") }, false, http.StatusForbidden},
		{"another machine", install.GoblinSealPath, func(r *http.Request) { r.RemoteAddr = "192.0.2.7:50000" }, false, http.StatusForbidden},
		{"a proxy", install.GoblinSealPath, func(r *http.Request) { r.Header.Set("X-Forwarded-For", "192.0.2.7") }, false, http.StatusForbidden},
		{"the remote gate", install.GoblinSealPath, func(r *http.Request) { r.Header.Set("Tailscale-User-Login", "owner@example.com") }, false, http.StatusForbidden},
		{"a post with no token anywhere else", "/api/afk", func(*http.Request) {}, false, http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			board := NewHTTP(&Service{Instance: "board-token"}, "127.0.0.1:4310", nil)
			isReached := false
			board.GoblinHooks = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				isReached = true
				w.WriteHeader(http.StatusOK)
			})
			request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:4310"+test.path, strings.NewReader(`{}`))
			request.RemoteAddr = "127.0.0.1:50000"
			test.change(request)
			response := httptest.NewRecorder()

			// Act
			board.ServeHTTP(response, request)

			// Assert
			if isReached != test.isReached {
				t.Fatalf("goblin hooks reached = %t, want %t (status %d, %q)", isReached, test.isReached, response.Code, response.Body.String())
			}
			if !test.isReached && response.Code != test.wantRefusal {
				t.Fatalf("status = %d, want %d", response.Code, test.wantRefusal)
			}
		})
	}
}
