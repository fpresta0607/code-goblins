package supervisor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recentDictations asks for the board's recent dictations, or keeps one when
// text is not empty, the way the board's own page on this machine does,
// after change has had its say over the request.
func recentDictations(handler *HTTP, text string, change func(*http.Request)) *httptest.ResponseRecorder {
	request := httptest.NewRequest("GET", "http://"+handler.Host+"/api/dictations", nil)
	if text != "" {
		body, _ := json.Marshal(map[string]string{"text": text})
		request = httptest.NewRequest("POST", "http://"+handler.Host+"/api/dictations", bytes.NewReader(body))
		request.Header.Set("Origin", "http://"+handler.Host)
		request.Header.Set("X-CFO-Token", "test-instance")
		request.Header.Set("Content-Type", "application/json")
	}
	request.Host = handler.Host
	request.RemoteAddr = "127.0.0.1:50000"
	if change != nil {
		change(request)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func dictatedTexts(t *testing.T, response *httptest.ResponseRecorder) []string {
	t.Helper()
	var body struct {
		Messages []struct {
			Text string `json:"text"`
			At   int64  `json:"at"`
		} `json:"messages"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil {
		t.Fatalf("answered %d %s", response.Code, response.Body)
	}
	texts := []string{}
	for _, message := range body.Messages {
		if message.At <= 0 {
			t.Fatalf("%q carries no time", message.Text)
		}
		texts = append(texts, message.Text)
	}
	return texts
}

func TestEveryWindowReadsOneListOfRecentDictationsNewestFirstKeptAcrossARestart(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	board := NewHTTP(&Service{Store: store, Instance: "test-instance"}, dictationHost, nil)

	// Act
	recentDictations(board, "open the pull request", nil)
	kept := dictatedTexts(t, recentDictations(board, "  then run the tests ", nil))
	restarted := NewHTTP(&Service{Store: store, Instance: "test-instance"}, dictationHost, nil)

	// Assert
	want := []string{"then run the tests", "open the pull request"}
	if strings.Join(kept, "|") != strings.Join(want, "|") {
		t.Fatalf("keeping a dictation answered %q, want %q", kept, want)
	}
	if read := dictatedTexts(t, recentDictations(restarted, "", nil)); strings.Join(read, "|") != strings.Join(want, "|") {
		t.Fatalf("after a restart the list reads %q, want %q", read, want)
	}
}

func TestABoardWithNoDictationsYetListsNone(t *testing.T) {
	if read := dictatedTexts(t, recentDictations(dictationBoard(t, nil), "", nil)); len(read) != 0 {
		t.Fatalf("a new home lists %q", read)
	}
}

func TestTheListKeepsOnlyTheNewestDictations(t *testing.T) {
	// Arrange
	board := dictationBoard(t, nil)

	// Act
	for i := 1; i <= dictationsKept+2; i++ {
		recentDictations(board, fmt.Sprintf("message %d", i), nil)
	}
	read := dictatedTexts(t, recentDictations(board, "", nil))

	// Assert
	if len(read) != dictationsKept || read[0] != fmt.Sprintf("message %d", dictationsKept+2) || read[len(read)-1] != "message 3" {
		t.Fatalf("the list reads %q", read)
	}
}

func TestRecentDictationsAreReadAndKeptOnlyByTheBoardsOwnPageOnThisMachine(t *testing.T) {
	header := func(name, value string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set(name, value) }
	}
	for name, test := range map[string]struct {
		text   string
		change func(*http.Request)
		code   int
	}{
		"a read from a peer off this machine": {"", func(r *http.Request) { r.RemoteAddr = "100.101.102.103:51234" }, http.StatusForbidden},
		"a read through a proxy":              {"", header("X-Forwarded-For", "100.101.102.103"), http.StatusForbidden},
		"a keep from a peer off this machine": {"private words", func(r *http.Request) { r.RemoteAddr = "100.101.102.103:51234" }, http.StatusForbidden},
		"a keep with no board token":          {"private words", func(r *http.Request) { r.Header.Del("X-CFO-Token") }, http.StatusForbidden},
		"a keep from another site's page":     {"private words", header("Origin", "https://example.test"), http.StatusForbidden},
		"a keep of nothing but spaces":        {"   ", nil, http.StatusBadRequest},
	} {
		// Arrange
		board := dictationBoard(t, nil)
		recentDictations(board, "said before", nil)

		// Act
		response := recentDictations(board, test.text, test.change)

		// Assert
		if response.Code != test.code || strings.Contains(response.Body.String(), "said before") {
			t.Errorf("%s: answered %d %s, want %d and no words", name, response.Code, response.Body, test.code)
		}
		if read := dictatedTexts(t, recentDictations(board, "", nil)); len(read) != 1 {
			t.Errorf("%s: the list reads %q", name, read)
		}
	}
	// A board reached by another name, as one shared over a tailnet is,
	// reads none of them.
	store, _ := testStore(t)
	shared := NewHTTP(&Service{Store: store, Instance: "test-instance"}, "goblins.tailnet.ts.net:443", nil)
	if response := recentDictations(shared, "", nil); response.Code != http.StatusForbidden {
		t.Fatalf("a shared board answered %d %s", response.Code, response.Body)
	}
}

func TestAListThatCannotBeReadSaysSoAndIsReplacedByTheNextDictation(t *testing.T) {
	// Arrange
	board := dictationBoard(t, nil)
	writeDictationsFile(t, board, "{broken")

	// Act
	broken := recentDictations(board, "", nil)
	kept := recentDictations(board, "a fresh start", nil)

	// Assert
	if broken.Code != http.StatusInternalServerError || !strings.Contains(broken.Body.String(), dictationsFile) {
		t.Fatalf("a broken list answered %d %s", broken.Code, broken.Body)
	}
	if read := dictatedTexts(t, kept); strings.Join(read, "|") != "a fresh start" {
		t.Fatalf("the next dictation left %q", read)
	}
}

func writeDictationsFile(t *testing.T, board *HTTP, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(board.Service.Store.Home.State, dictationsFile), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}
