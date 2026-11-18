package supervisor

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// postComposerFile posts body as a file he pasted into, or added to, a
// message for task, named name, as the board's composer does.
func postComposerFile(board *HTTP, task, name string, body []byte) *httptest.ResponseRecorder {
	request := httptest.NewRequest("POST", "http://"+board.Host+"/api/composer/file?task="+task+"&name="+name, bytes.NewReader(body))
	request.Host = board.Host
	request.Header.Set("Origin", "http://"+board.Host)
	request.Header.Set("X-CEO-Token", "test-instance")
	request.Header.Set("Content-Type", "application/octet-stream")
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

// composerBoard is a board whose task-1 has a task folder.
func composerBoard(t *testing.T) (*HTTP, state.TaskMeta, string) {
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

func TestAPictureOrFilePastedIntoAMessageIsKeptWhereTheSessionReadsIt(t *testing.T) {
	board, meta, stateDir := composerBoard(t)
	for _, c := range []struct {
		what, name           string
		body                 []byte
		task, dir, extension string
	}{
		{"a goblin's picture", "shot.png", onePixelPNG(t), "task-1", filepath.Join(meta.TaskTmp, composerFilesDir), ".png"},
		{"a goblin's file", "notes.md", []byte("# notes\n"), "task-1", filepath.Join(meta.TaskTmp, composerFilesDir), ".md"},
		{"a JSON attachment sent as binary", "notes.json", []byte(`{"value":"example"}`), "task-1", filepath.Join(meta.TaskTmp, composerFilesDir), ".json"},
		{"the CEO's picture", "shot.png", onePixelPNG(t), "", filepath.Join(stateDir, composerFilesDir), ".png"},
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
	board, _, _ := composerBoard(t)
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

// A long answer kept for a credential card reaches the goblin as his word, so
// one holding a word shaped like a key anywhere is refused, kept nowhere and
// never repeated (#2538). The chat's own files are kept as they are.
func TestALongCredentialAnswerHoldingAKeyIsKeptNowhere(t *testing.T) {
	board, meta, _ := composerBoard(t)
	canary := newCanary(t)
	long := strings.Repeat("Use the shop's bot account, not mine.\n", 39) + "my key is " + canary + "\n" + strings.Repeat("Then push.\n", 400)
	for _, c := range []struct {
		what, query string
		body        string
		code        int
	}{
		{"a credential card's answer with a key on line 40", "&credential=1", long, 400},
		{"a credential card's answer with no key", "&credential=1", strings.ReplaceAll(long, canary, "in the vault"), 201},
		{"the chat's file with the same words", "", long, 201},
	} {
		t.Run(c.what, func(t *testing.T) {
			// Arrange
			request := httptest.NewRequest("POST", "http://"+board.Host+"/api/composer/file?task=task-1&name=answer.txt"+c.query, strings.NewReader(c.body))
			request.Host = board.Host
			request.Header.Set("Origin", "http://"+board.Host)
			request.Header.Set("X-CEO-Token", "test-instance")
			request.Header.Set("Content-Type", "application/octet-stream")
			response := httptest.NewRecorder()

			// Act
			board.ServeHTTP(response, request)

			// Assert
			if response.Code != c.code {
				t.Fatalf("answered %d %s, want %d", response.Code, response.Body, c.code)
			}
			if strings.Contains(response.Body.String(), canary) {
				t.Fatal("the answer repeats the key")
			}
			if c.code == 400 && !strings.Contains(response.Body.String(), "Your answer holds a word shaped like a key, so nothing was kept or sent.") {
				t.Fatalf("refused with %s, want it said that nothing was kept or sent", response.Body)
			}
			if found := filesHolding(t, meta.TaskTmp, canary); c.code == 400 && len(found) != 0 {
				t.Fatalf("the key is in %v", found)
			}
		})
	}
}

func TestAFileNameCannotAddAPathSegment(t *testing.T) {
	board, meta, _ := composerBoard(t)

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
	board, _, _ := composerBoard(t)
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

// A long paste the box kept as text is read back whole, line breaks and all,
// for the conversation's chip that opens to it, as text the browser never
// runs (#2404).
func TestAPastedTextKeptForAMessageIsReadBackForItsChip(t *testing.T) {
	board, _, _ := composerBoard(t)
	text := []byte(strings.Repeat("PS C:\\dev> go test ./...\r\nok  \tgithub.com/x/y\t0.4s <b>\n", 3000))
	for _, task := range []string{"task-1", ""} {
		t.Run("task "+task, func(t *testing.T) {
			// Arrange
			var kept struct{ Path string }
			if response := postComposerFile(board, task, "pasted.txt", text); json.Unmarshal(response.Body.Bytes(), &kept) != nil {
				t.Fatalf("not kept: %d %s", response.Code, response.Body)
			}

			// Act
			response := getComposerFile(board, task, kept.Path, "test-instance")

			// Assert
			if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), text) {
				t.Fatalf("answered %d with %d bytes, want 200 with the %d kept", response.Code, response.Body.Len(), len(text))
			}
			if media := response.Header().Get("Content-Type"); media != "text/plain; charset=utf-8" {
				t.Fatalf("served as %q, want text/plain; charset=utf-8", media)
			}
			if sniff := response.Header().Get("X-Content-Type-Options"); sniff != "nosniff" {
				t.Fatalf("X-Content-Type-Options is %q, want nosniff", sniff)
			}
		})
	}
}

func TestOnlyAPictureOrAPastedTextInTheComposerFolderIsReadBack(t *testing.T) {
	board, meta, stateDir := composerBoard(t)
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
		{"a kept file that is neither a picture nor a pasted text", "task-1", kept.Path, "test-instance", 415},
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
