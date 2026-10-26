package supervisor

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// postComposerFile posts body as a file he pasted into, or added to, a
// message for task, named name, as the board's composer does.
func postComposerFile(board *HTTP, task, name string, body []byte) *httptest.ResponseRecorder {
	request := httptest.NewRequest("POST", "http://"+board.Host+"/api/composer/file?task="+task+"&name="+name, bytes.NewReader(body))
	request.Host = board.Host
	request.Header.Set("Origin", "http://"+board.Host)
	request.Header.Set("X-CFO-Token", "test-instance")
	request.Header.Set("Content-Type", "application/octet-stream")
	response := httptest.NewRecorder()
	board.ServeHTTP(response, request)
	return response
}

func TestAPictureOrFilePastedIntoAMessageIsKeptWhereTheSessionReadsIt(t *testing.T) {
	board, meta, stateDir := answerImageBoard(t)
	for _, c := range []struct {
		what, name           string
		body                 []byte
		task, dir, extension string
	}{
		{"a goblin's picture", "shot.png", onePixelPNG(t), "task-1", filepath.Join(meta.TaskTmp, composerFilesDir), ".png"},
		{"a goblin's file", "notes.md", []byte("# notes\n"), "task-1", filepath.Join(meta.TaskTmp, composerFilesDir), ".md"},
		{"the CFO's picture", "shot.png", onePixelPNG(t), "", filepath.Join(stateDir, composerFilesDir), ".png"},
	} {
		t.Run(c.what, func(t *testing.T) {
			// Act
			response := postComposerFile(board, c.task, c.name, c.body)

			// Assert
			var kept struct{ Path string }
			if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &kept) != nil {
				t.Fatalf("answered %d %s", response.Code, response.Body)
			}
			if filepath.Dir(kept.Path) != c.dir || filepath.Ext(kept.Path) != c.extension {
				t.Fatalf("kept at %s, want a %s in %s", kept.Path, c.extension, c.dir)
			}
			if data, err := os.ReadFile(kept.Path); err != nil || !bytes.Equal(data, c.body) {
				t.Fatalf("the kept file holds %d bytes (%v), want the %d written", len(data), err, len(c.body))
			}
		})
	}
}

func TestAPastedFileTheBoardCannotKeepSaysWhy(t *testing.T) {
	board, _, _ := answerImageBoard(t)
	for _, c := range []struct {
		what, task, name string
		body             []byte
		code             int
		says             string
	}{
		{"over the size cap", "task-1", "shot.png", append(onePixelPNG(t), make([]byte, maxReviewImage)...), 413, "at most 10 MiB"},
		{"empty", "task-1", "shot.png", nil, 400, "could not be read"},
		{"a task that has ended", "gone", "shot.png", onePixelPNG(t), 409, "has ended"},
	} {
		t.Run(c.what, func(t *testing.T) {
			// Act
			response := postComposerFile(board, c.task, c.name, c.body)

			// Assert
			if response.Code != c.code || !strings.Contains(response.Body.String(), c.says) {
				t.Fatalf("answered %d %s, want %d naming %q", response.Code, response.Body, c.code, c.says)
			}
		})
	}
}

func TestAFileNameCannotAddAPathSegment(t *testing.T) {
	board, meta, _ := answerImageBoard(t)

	// Act
	response := postComposerFile(board, "task-1", "../../evil.sh", []byte("#!/bin/sh\n"))

	// Assert
	var kept struct{ Path string }
	if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &kept) != nil {
		t.Fatalf("answered %d %s", response.Code, response.Body)
	}
	if filepath.Dir(kept.Path) != filepath.Join(meta.TaskTmp, composerFilesDir) {
		t.Fatalf("kept at %s, outside the task's composer folder", kept.Path)
	}
}
