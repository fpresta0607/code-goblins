package supervisor

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"net/url"
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
		{"a JSON attachment sent as binary", "notes.json", []byte(`{"value":"example"}`), "task-1", filepath.Join(meta.TaskTmp, composerFilesDir), ".json"},
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

// getComposerFile reads path back as the board's message view and box do,
// with token as the board's token.
func getComposerFile(board *HTTP, task, path, token string) *httptest.ResponseRecorder {
	query := url.Values{"path": {path}, "token": {token}}
	if task != "" {
		query.Set("task", task)
	}
	request := httptest.NewRequest("GET", "http://"+board.Host+"/api/composer/file?"+query.Encode(), nil)
	request.Host = board.Host
	response := httptest.NewRecorder()
	board.ServeHTTP(response, request)
	return response
}

func TestAPictureKeptForAMessageIsReadBackForItsPreview(t *testing.T) {
	board, _, _ := answerImageBoard(t)
	picture := onePixelPNG(t)
	for _, task := range []string{"task-1", ""} {
		t.Run("task "+task, func(t *testing.T) {
			// Arrange
			var kept struct{ Path string }
			if response := postComposerFile(board, task, "shot.png", picture); json.Unmarshal(response.Body.Bytes(), &kept) != nil {
				t.Fatalf("not kept: %d %s", response.Code, response.Body)
			}

			// Act
			response := getComposerFile(board, task, kept.Path, "test-instance")

			// Assert
			if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), picture) {
				t.Fatalf("answered %d with %d bytes, want 200 with the %d kept", response.Code, response.Body.Len(), len(picture))
			}
			if media := response.Header().Get("Content-Type"); media != "image/png" {
				t.Fatalf("served as %q, want image/png", media)
			}
			if sniff := response.Header().Get("X-Content-Type-Options"); sniff != "nosniff" {
				t.Fatalf("X-Content-Type-Options is %q, want nosniff", sniff)
			}
		})
	}
}

func TestOnlyAPictureInTheComposerFolderIsReadBack(t *testing.T) {
	board, meta, stateDir := answerImageBoard(t)
	var kept struct{ Path string }
	if response := postComposerFile(board, "task-1", "notes.md", []byte("# notes\n")); json.Unmarshal(response.Body.Bytes(), &kept) != nil {
		t.Fatalf("not kept: %d %s", response.Code, response.Body)
	}
	outside := filepath.Join(meta.TaskTmp, "secret.png")
	if err := os.WriteFile(outside, onePixelPNG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		what, task, path, token string
		code                    int
	}{
		{"a picture beside the folder", "task-1", outside, "test-instance", 403},
		{"a path that climbs out of the folder", "task-1", filepath.Join(meta.TaskTmp, composerFilesDir, "..", "secret.png"), "test-instance", 403},
		{"a relative path", "task-1", filepath.Join(composerFilesDir, "pasted-1.png"), "test-instance", 403},
		{"another session's folder", "", kept.Path, "test-instance", 403},
		{"the state folder's own files", "", filepath.Join(stateDir, "tasks.json"), "test-instance", 403},
		{"a kept file that is not a picture", "task-1", kept.Path, "test-instance", 415},
		{"a picture no longer kept", "task-1", filepath.Join(meta.TaskTmp, composerFilesDir, "pasted-gone.png"), "test-instance", 404},
		{"a stale token", "task-1", kept.Path, "old-instance", 403},
		{"a task that has ended", "gone", kept.Path, "test-instance", 409},
	} {
		t.Run(c.what, func(t *testing.T) {
			// Act
			response := getComposerFile(board, c.task, c.path, c.token)

			// Assert
			if response.Code != c.code {
				t.Fatalf("answered %d %s, want %d", response.Code, response.Body, c.code)
			}
			if c.code != 200 && bytes.Contains(response.Body.Bytes(), []byte("PNG")) {
				t.Fatalf("a refused read still served the file's bytes: %q", response.Body)
			}
		})
	}
}
