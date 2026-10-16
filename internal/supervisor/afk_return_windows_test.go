package supervisor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
)

// openedAt is an OpenPage that opens every page at link and records each one.
func openedAt(link string, opened *[]string) func(context.Context, string) (string, error) {
	return func(_ context.Context, file string) (string, error) {
		*opened = append(*opened, file)
		return link, nil
	}
}

func TestTurningAFKOffKeepsTheReportOnTheBoardWithoutScrawl(t *testing.T) {
	// Arrange
	s, h, _ := asTheCFO(t)
	var opened []string
	s.Options.OpenPage = openedAt(pageLink, &opened)
	s.Options.BoardAssets = fstest.MapFS{"assets/goblin-personas.png": {Data: []byte("faces")}}
	left := afk.Entry{Kind: afk.KindLeft, What: "Delete the idle cache database?", Evidence: "No reads since 04:00", Diagnosis: "It bills while nothing reads it", Tried: "Watched its reads", Options: []string{"Delete in a week", "Delete now"}, Answer: "Delete in a week"}
	earlier := filepath.Join(h.State, afkReturnDir, "afk-20261014T010000.000Z")
	if err := os.MkdirAll(earlier, 0o700); err != nil {
		t.Fatal(err)
	}

	// Act
	logged := awayAndBack(t, s, h, left)
	s.pageWork.Wait()

	// Assert
	if _, err := os.Stat(earlier); !os.IsNotExist(err) {
		t.Errorf("an earlier stretch's page with no open card = %v, want it removed", err)
	}
	switched, err := afk.Read(h.State)
	if err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(h.State, afkReturnDir, switched.Session, "index.html")
	if len(opened) != 0 {
		t.Errorf("opened %q, want no Scrawl page", opened)
	}
	written, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	if text := string(written); !strings.Contains(text, "Delete the idle cache database?") || !strings.Contains(text, `data-item="change:`+afk.LeftQuestionID(logged[0])+`"`) {
		t.Errorf("the page lacks the decision with the question in his History it changes")
	}
	if faces, err := os.ReadFile(filepath.Join(filepath.Dir(page), "goblin-personas.png")); err != nil || string(faces) != "faces" {
		t.Errorf("the goblin faces beside the page = %q, %v, want the board's own", faces, err)
	}
	var cards []Review
	for _, item := range s.Store.Snapshot().Reviews {
		if item.LavishPage == page {
			cards = append(cards, item)
		}
	}
	if len(cards) != 0 {
		t.Fatalf("report cards = %+v, want none", cards)
	}
	view, err := s.afkView(s.Store.Snapshot())
	if err != nil || !strings.HasPrefix(view.Page, "/api/afk/report/") {
		t.Fatalf("report link = %q, %v", view.Page, err)
	}
	handler := NewHTTP(s, "127.0.0.1:12345", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:12345"+view.Page, nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), left.What) {
		t.Fatalf("report response = %d: %s", response.Code, response.Body.String())
	}
	policy := response.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "frame-ancestors 'self'") {
		t.Error("report must embed only on its own board")
	}
	_, after, found := strings.Cut(policy, "script-src 'nonce-")
	nonce, _, _ := strings.Cut(after, "'")
	if !found || nonce == "" || !strings.Contains(policy, "style-src 'nonce-"+nonce+"'") || !strings.Contains(response.Body.String(), `<script nonce="`+nonce+`">`) || !strings.Contains(response.Body.String(), `<style nonce="`+nonce+`">`) {
		t.Fatal("report must allow only its own script and style with a response nonce")
	}
	again := httptest.NewRecorder()
	handler.ServeHTTP(again, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:12345"+view.Page, nil))
	if again.Header().Get("Content-Security-Policy") == policy {
		t.Error("report reused a response nonce")
	}
	faces := httptest.NewRecorder()
	handler.ServeHTTP(faces, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:12345"+strings.TrimSuffix(view.Page, "index.html")+"goblin-personas.png", nil))
	if faces.Code != http.StatusOK || faces.Body.String() != "faces" || faces.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("report picture = %d %q, want the copied image", faces.Code, faces.Body.String())
	}
}

func TestTheBoardCannotReadFilesOutsideTheAFKReport(t *testing.T) {
	store, _ := testStore(t)
	handler := NewHTTP(&Service{Store: store}, "127.0.0.1:12345", nil)
	for _, path := range []string{"/api/afk/report/missing/index.html", "/api/afk/report/../../primary.json", "/api/afk/report/session/../index.html", "/api/afk/report/%2e%2e/primary.json", "/api/afk/report/session%5c..%5cprimary.json", "/api/afk/report/C:/primary.json"} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:12345"+path, nil))
			if response.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", response.Code)
			}
		})
	}
}

func TestTheReportWaitsForTheAFKSwitchToFinishWritingIt(t *testing.T) {
	store, _ := testStore(t)
	service := &Service{Store: store}
	directory := filepath.Join(store.Home.State, afkReturnDir, "session")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	handler := NewHTTP(service, "127.0.0.1:12345", nil)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	func() {
		service.afkChange.Lock()
		defer service.afkChange.Unlock()
		go func() {
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:12345/api/afk/report/session/index.html", nil))
			close(done)
		}()
		select {
		case <-done:
			t.Error("report request returned before the AFK switch finished writing it")
		case <-time.After(100 * time.Millisecond):
		}
		if err := os.WriteFile(filepath.Join(directory, "index.html"), []byte("Welcome back"), 0o600); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-done:
		if response.Code != http.StatusOK || response.Body.String() != "Welcome back" {
			t.Errorf("report = %d %q, want the completed report", response.Code, response.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("report did not load after the switch finished")
	}
}
