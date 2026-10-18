package supervisor

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The Overlord, 2026-10-11 about 01:50Z, of a review card that showed an
// empty box named Scrawl page: a card for a Scrawl page shows the page itself.
// The board draws it from the review server's export of the page, sandboxed.

// pageItem records an open review item whose page the review server at
// server serves under key, and returns the store.
func pageItem(t *testing.T, server *httptest.Server, key string) *Store {
	t.Helper()
	store, _ := testStore(t)
	now := time.Now().UTC()
	item := Review{ID: "afk-report-review", Identity: strings.Repeat("a", 64), Title: "Your AFK report with one Merged group", Lavish: server.URL + "/session/" + key, LavishPage: filepath.Join(t.TempDir(), "page.html"), State: "open", CreatedAt: now, UpdatedAt: now}
	if err := store.acceptReview(item); err != nil {
		t.Fatal(err)
	}
	return store
}

// preview asks the board for item id's preview.
func preview(store *Store, id string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	NewHTTP(&Service{Store: store}, "board.local", nil).ServeHTTP(response, httptest.NewRequest("GET", "http://board.local/api/reviews/"+id+"/preview", nil))
	return response
}

func TestAPagesPreviewIsItsExportSandboxedForTheBoardAlone(t *testing.T) {
	// Arrange
	asked := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Path
		w.Header().Set("Content-Disposition", `attachment; filename="page.export.html"`)
		_, _ = w.Write([]byte("<!doctype html><h1>Your AFK report</h1>"))
	}))
	defer server.Close()
	store := pageItem(t, server, "f2ae435ad55a056c")

	// Act
	response := preview(store, "afk-report-review")

	// Assert
	if response.Code != 200 || response.Body.String() != "<!doctype html><h1>Your AFK report</h1>" {
		t.Fatalf("preview = %d %q, want the page's export", response.Code, response.Body.String())
	}
	if asked != "/api/f2ae435ad55a056c/export" {
		t.Errorf("the review server was asked for %q, want the page's export", asked)
	}
	if policy := response.Header().Get("Content-Security-Policy"); policy != "sandbox allow-scripts; frame-ancestors 'self'" {
		t.Errorf("the preview's policy = %q, want it sandboxed and framed only by the board", policy)
	}
	if disposition := response.Header().Get("Content-Disposition"); disposition != "" || response.Header().Get("Content-Type") != "text/html; charset=utf-8" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("the preview's headers = %v, want an HTML page shown in place", response.Header())
	}
}

func TestAPagePreviewIsRefusedWithoutAPageOrAReviewServerThatAnswers(t *testing.T) {
	for name, test := range map[string]struct {
		serve func(w http.ResponseWriter, r *http.Request)
		id    string
		code  int
	}{
		"an item that is not on the board": {id: "no-such-review", code: 404},
		"a review server that fails":       {id: "afk-report-review", code: 502, serve: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }},
		"a redirect elsewhere": {id: "afk-report-review", code: 502, serve: func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
		}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			serve := test.serve
			if serve == nil {
				serve = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<!doctype html>")) }
			}
			server := httptest.NewServer(http.HandlerFunc(serve))
			defer server.Close()
			store := pageItem(t, server, "f2ae435ad55a056c")

			// Act
			response := preview(store, test.id)

			// Assert
			if response.Code != test.code {
				t.Fatalf("preview = %d %q, want %d", response.Code, response.Body.String(), test.code)
			}
		})
	}
}

func TestAPageExportIsAskedOnlyOfTheReviewServerItsLinkNames(t *testing.T) {
	for link, want := range map[string]string{
		"http://127.0.0.1:4387/session/f2ae435ad55a056c":          "http://127.0.0.1:4387/api/f2ae435ad55a056c/export",
		"https://pc.example.ts.net:4388/session/8f05d03ec7af3ec6": "https://pc.example.ts.net:4388/api/8f05d03ec7af3ec6/export",
		"http://127.0.0.1:4387/artifact/f2ae435ad55a056c":         "",
		"http://127.0.0.1:4387/session/../api/sessions":           "",
		"file:///C:/pages/page.html":                              "",
		"http://someone@127.0.0.1:4387/session/f2ae435ad55a056c":  "",
	} {
		got, err := pageExportURL(link)
		if want == "" && err == nil || want != "" && (err != nil || got != want) {
			t.Errorf("pageExportURL(%q) = %q (%v), want %q", link, got, err, want)
		}
	}
}
