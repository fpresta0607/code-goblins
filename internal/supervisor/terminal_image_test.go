package supervisor

import (
	"bytes"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/clipboard"
)

// imageBoard is a board whose clipboard is a record of the pictures put on
// it, answering with refusal.
func imageBoard(t *testing.T, refusal error) (*HTTP, *[][]byte) {
	t.Helper()
	store, _ := testStore(t)
	board := NewHTTP(&Service{Store: store, Instance: "test-instance"}, "127.0.0.1:7777", nil)
	var put [][]byte
	board.putImage = func(picture []byte) error {
		put = append(put, picture)
		return refusal
	}
	return board, &put
}

// addPicture posts body as a picture added to a terminal, as the board's page
// does, with token as its token and contentType as its type.
func addPicture(board *HTTP, token, contentType string, body []byte) *httptest.ResponseRecorder {
	request := httptest.NewRequest("POST", "http://"+board.Host+"/api/terminal/image", bytes.NewReader(body))
	request.Host = board.Host
	request.Header.Set("Origin", "http://"+board.Host)
	request.Header.Set("X-CFO-Token", token)
	request.Header.Set("Content-Type", contentType)
	response := httptest.NewRecorder()
	board.ServeHTTP(response, request)
	return response
}

func TestAPictureAddedToATerminalIsPutOnThisPCsClipboardWhole(t *testing.T) {
	// Arrange
	board, put := imageBoard(t, nil)
	picture := onePixelPNG(t)

	// Act
	response := addPicture(board, "test-instance", "image/png", picture)

	// Assert
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"pasted":true`) {
		t.Fatalf("answered %d %s", response.Code, response.Body)
	}
	if len(*put) != 1 || !bytes.Equal((*put)[0], picture) {
		t.Fatalf("the clipboard was handed %d pictures, want the one added", len(*put))
	}
}

func TestAPictureThatCannotBeAddedIsRefusedAndSaysWhy(t *testing.T) {
	picture := onePixelPNG(t)
	for name, c := range map[string]struct {
		contentType string
		body        []byte
		clipboard   error
		status      int
		refusal     string
		isPut       bool
	}{
		"not a PNG by its type":         {"image/jpeg", []byte{0xff, 0xd8}, nil, 400, "A PNG picture is required", false},
		"over the size cap":             {"image/png", append(picture, make([]byte, terminalImageLimit)...), nil, 413, "at most 16 MiB", false},
		"a PNG that does not decode":    {"image/png", []byte("not a picture"), fmt.Errorf("%w: bad header", clipboard.ErrNotPNG), 400, "not a PNG this PC can read", true},
		"a clipboard another app holds": {"image/png", picture, errors.New("another program holds the clipboard"), 500, "another program holds the clipboard", true},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			board, put := imageBoard(t, c.clipboard)

			// Act
			response := addPicture(board, "test-instance", c.contentType, c.body)

			// Assert
			if response.Code != c.status || !strings.Contains(response.Body.String(), c.refusal) {
				t.Fatalf("answered %d %s, want %d saying %q", response.Code, response.Body, c.status, c.refusal)
			}
			if isPut := len(*put) != 0; isPut != c.isPut {
				t.Fatalf("the clipboard was handed a picture: %v, want %v", isPut, c.isPut)
			}
		})
	}
}

func TestOnlyTheBoardsOwnPageCanPutAPictureOnTheClipboard(t *testing.T) {
	// Arrange
	board, put := imageBoard(t, nil)

	// Act
	response := addPicture(board, "another-board", "image/png", onePixelPNG(t))

	// Assert
	if response.Code != 403 || len(*put) != 0 {
		t.Fatalf("answered %d %s and handed the clipboard %d pictures", response.Code, response.Body, len(*put))
	}
}
