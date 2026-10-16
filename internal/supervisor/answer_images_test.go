package supervisor

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// pastePicture posts body as a picture he pasted into his answer to a card
// asked by task, as the board's own page does.
func pastePicture(board *HTTP, task string, body []byte) *httptest.ResponseRecorder {
	request := httptest.NewRequest("POST", "http://"+board.Host+"/api/answer-images?task="+task, bytes.NewReader(body))
	request.Host = board.Host
	request.Header.Set("Origin", "http://"+board.Host)
	request.Header.Set("X-CFO-Token", "test-instance")
	request.Header.Set("Content-Type", "image/png")
	response := httptest.NewRecorder()
	board.ServeHTTP(response, request)
	return response
}

func onePixelPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// answerImageBoard is a board whose task-1 has a task folder.
func answerImageBoard(t *testing.T) (*HTTP, state.TaskMeta, string) {
	t.Helper()
	store, h := testStore(t)
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	meta.TaskTmp = filepath.Join(h.State, "tasktmp", "task-1")
	if err := os.MkdirAll(meta.TaskTmp, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	return NewHTTP(&Service{Store: store, Instance: "test-instance"}, "127.0.0.1:7777", nil), meta, h.State
}

func TestAPicturePastedIntoAnAnswerIsKeptWhereTheAskerReadsIt(t *testing.T) {
	board, meta, stateDir := answerImageBoard(t)
	for _, c := range []struct{ asker, task, dir string }{
		{"a goblin", "task-1", filepath.Join(meta.TaskTmp, answerImagesDir)},
		{"the CFO", "", filepath.Join(stateDir, answerImagesDir)},
	} {
		t.Run(c.asker, func(t *testing.T) {
			// Arrange
			picture := onePixelPNG(t)

			// Act
			response := pastePicture(board, c.task, picture)

			// Assert
			var kept struct{ Path string }
			if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &kept) != nil {
				t.Fatalf("answered %d %s", response.Code, response.Body)
			}
			if filepath.Dir(kept.Path) != c.dir || filepath.Ext(kept.Path) != ".png" {
				t.Fatalf("kept at %s, want a .png in %s", kept.Path, c.dir)
			}
			if data, err := os.ReadFile(kept.Path); err != nil || !bytes.Equal(data, picture) {
				t.Fatalf("the kept file holds %d bytes (%v), want the %d pasted", len(data), err, len(picture))
			}
		})
	}
}

func TestTwoPicturesPastedTogetherAreKeptApart(t *testing.T) {
	// Arrange
	board, _, _ := answerImageBoard(t)
	picture := onePixelPNG(t)

	// Act
	first, second := pastePicture(board, "task-1", picture), pastePicture(board, "task-1", picture)

	// Assert
	if first.Code != 201 || second.Code != 201 || first.Body.String() == second.Body.String() {
		t.Fatalf("answered %d %s and %d %s", first.Code, first.Body, second.Code, second.Body)
	}
}

func TestAPictureThatCannotBeKeptIsRefusedAndNothingIsWritten(t *testing.T) {
	board, meta, stateDir := answerImageBoard(t)
	for name, c := range map[string]struct {
		task    string
		body    []byte
		status  int
		refusal string
	}{
		"not a picture":      {"task-1", []byte("<svg onload=alert(1)>"), 400, "Only a PNG, JPEG, GIF or WebP picture"},
		"over the size cap":  {"task-1", append(onePixelPNG(t), make([]byte, maxReviewImage)...), 413, "at most 10 MiB"},
		"an ended goblin":    {"task-2", onePixelPNG(t), 409, "has ended"},
		"a path for a task":  {"..%5Ctask-1", onePixelPNG(t), 409, "has ended"},
		"an empty clipboard": {"", nil, 400, "Only a PNG, JPEG, GIF or WebP picture"},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			response := pastePicture(board, c.task, c.body)

			// Assert
			if response.Code != c.status || !strings.Contains(response.Body.String(), c.refusal) {
				t.Fatalf("answered %d %s, want %d saying %q", response.Code, response.Body, c.status, c.refusal)
			}
			for _, dir := range []string{filepath.Join(meta.TaskTmp, answerImagesDir), filepath.Join(stateDir, answerImagesDir)} {
				if entries, _ := os.ReadDir(dir); len(entries) != 0 {
					t.Fatalf("%s holds %d files after a refusal", dir, len(entries))
				}
			}
		})
	}
}

func TestOnlyTheBoardsOwnPageCanKeepAPicture(t *testing.T) {
	// Arrange
	board, meta, _ := answerImageBoard(t)
	request := httptest.NewRequest("POST", "http://"+board.Host+"/api/answer-images?task=task-1", bytes.NewReader(onePixelPNG(t)))
	request.Host = board.Host
	request.Header.Set("Origin", "http://"+board.Host)
	request.Header.Set("X-CFO-Token", "another-board")
	response := httptest.NewRecorder()

	// Act
	board.ServeHTTP(response, request)

	// Assert
	if response.Code != 403 {
		t.Fatalf("answered %d %s, want 403", response.Code, response.Body)
	}
	if entries, _ := os.ReadDir(filepath.Join(meta.TaskTmp, answerImagesDir)); len(entries) != 0 {
		t.Fatalf("a refused page kept %d files", len(entries))
	}
}

func TestAPastedPictureReportsAReadFailure(t *testing.T) {
	board, _, _ := answerImageBoard(t)
	request := httptest.NewRequest("POST", "http://"+board.Host+"/api/answer-images", nil)
	request.Host = board.Host
	request.Header.Set("Origin", "http://"+board.Host)
	request.Header.Set("X-CFO-Token", "test-instance")
	request.Body = failingImageBody{}
	response := httptest.NewRecorder()

	board.ServeHTTP(response, request)

	if response.Code != 400 || !strings.Contains(response.Body.String(), "could not be read") {
		t.Fatalf("answered %d %s, want a read failure", response.Code, response.Body)
	}
}

type failingImageBody struct{}

func (failingImageBody) Read([]byte) (int, error) { return 0, errors.New("interrupted upload") }
func (failingImageBody) Close() error             { return nil }

func TestAPastedPictureReportsAStorageFailure(t *testing.T) {
	board, meta, _ := answerImageBoard(t)
	if err := os.WriteFile(filepath.Join(meta.TaskTmp, answerImagesDir), []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}

	response := pastePicture(board, "task-1", onePixelPNG(t))

	if response.Code != 500 || !strings.Contains(response.Body.String(), "could not be kept") {
		t.Fatalf("answered %d %s, want a storage failure", response.Code, response.Body)
	}
}
