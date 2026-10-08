package supervisor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/wake"
)

// boardWakes are the wakes the board's errors raised for the CFO.
func boardWakes(t *testing.T, stateDir string) []wake.Record {
	t.Helper()
	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	var board []wake.Record
	for _, record := range records {
		if record.Kind == "notify" && record.Key == "board" {
			board = append(board, record)
		}
	}
	return board
}

func boardRequest(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://board.local"+path, strings.NewReader(body))
	req.Header.Set("Origin", "http://board.local")
	req.Header.Set("X-CFO-Token", "instance-1")
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

// A failure only the board saw, such as a clipboard it could not use, comes
// to the CFO through POST /api/cfo/report, once while it repeats.
func TestTheBoardReportsAFailureOnlyItSaw(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  string
		code  int
		wakes []string
	}{
		{"a clipboard the board could not use", `{"where":"credential card","text":"The browser did not copy the commands."}`, http.StatusAccepted, []string{"board: credential card: The browser did not copy the commands."}},
		{"no text", `{"where":"credential card","text":"  "}`, http.StatusBadRequest, nil},
		{"a field the route does not take", `{"where":"card","text":"x","level":"warning"}`, http.StatusBadRequest, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			handler := NewHTTP(&Service{Store: store, Instance: "instance-1"}, "board.local", nil)

			// Act
			first := boardRequest(handler, "POST", "/api/cfo/report", tc.body)
			second := boardRequest(handler, "POST", "/api/cfo/report", tc.body)

			// Assert
			if first.Code != tc.code || second.Code != tc.code {
				t.Fatalf("POST /api/cfo/report = %d then %d, want %d", first.Code, second.Code, tc.code)
			}
			var details []string
			for _, record := range boardWakes(t, store.Home.State) {
				details = append(details, record.Detail)
			}
			if strings.Join(details, "|") != strings.Join(tc.wakes, "|") {
				t.Fatalf("board wakes = %q, want %q", details, tc.wakes)
			}
		})
	}
}
