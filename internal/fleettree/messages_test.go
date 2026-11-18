package fleettree

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/routing"
)

// pictureAppendFile is a transcript's open handle that lets its session
// append a whole entry right after each look at the handle, as a live
// session writes on while one of its pictures is read.
type pictureAppendFile struct {
	*os.File
	looks  int
	append func()
}

func (file *pictureAppendFile) Stat() (os.FileInfo, error) {
	info, err := file.File.Stat()
	file.looks++
	file.append()
	return info, err
}

func TestPictureReadsWhileItsSessionAppends(t *testing.T) {
	// Arrange
	path := conversation(t, t.TempDir(), prompted("same", []object{textBlock("Look at this"), imageBlock([]byte("picture A"))}))
	reader := Reader{}
	page, _, err := reader.MessagePage(path, -1)
	if err != nil || len(page.Messages) != 1 || len(page.Messages[0].Pictures) != 1 {
		t.Fatalf("page: %+v %v", page, err)
	}
	log := reader.messageLogs[path]
	at := log.pictures[page.Messages[0].Pictures[0].ID]
	file, err := fsx.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	appending := &pictureAppendFile{File: file, append: func() { appendLines(t, path, prompted("later", "written while the picture is read")) }}

	// Act
	media, data, err := resolvePicture(appending, path, log.fileState, at.offset, func(line []byte) (string, string, bool) {
		var entry messageEntry
		var blocks []messageBlock
		if json.Unmarshal(line, &entry) != nil || json.Unmarshal(entry.Message.Content, &blocks) != nil || len(blocks) <= at.block {
			return "", "", false
		}
		return blocks[at.block].Source.MediaType, blocks[at.block].Source.Data, true
	})

	// Assert
	if appending.looks < 2 || err != nil || media != "image/png" || string(data) != "picture A" {
		t.Fatalf("picture read while its session appended: %q %q, %v after %d looks", media, data, err, appending.looks)
	}
}

// pictureStreamFile is a transcript's open handle that, the first time a
// picture is read from it, lets its session's stream read on and waits a
// while for that read, as the live view polls on while a picture is read.
type pictureStreamFile struct {
	messageReadFile
	once       sync.Once
	stream     func()
	isStreamed bool
}

func (file *pictureStreamFile) ReadAt(data []byte, offset int64) (int, error) {
	file.once.Do(func() {
		done := make(chan struct{})
		go func() {
			file.stream()
			close(done)
		}()
		select {
		case <-done:
			file.isStreamed = true
		case <-time.After(time.Minute):
		}
	})
	return file.messageReadFile.ReadAt(data, offset)
}

func TestPictureReadLetsItsSessionStreamOn(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("picture A"))
	tests := []struct {
		name     string
		makeFile func(*testing.T, string) string
	}{
		{"claude", func(t *testing.T, home string) string {
			return conversation(t, home, prompted("same", []object{imageBlock([]byte("picture A"))}))
		}},
		{"codex", func(t *testing.T, home string) string {
			return codexRollout(t, home, "picture", codexCompleted("UserMessage", object{"content": []object{{"type": "image", "url": "data:image/png;base64," + encoded}}}))
		}},
		{"pi", func(t *testing.T, home string) string {
			return piSession(t, home, piHeader(), piMessageEntry("same", "", "user", object{"content": []object{{"type": "image", "data": encoded, "mimeType": "image/png"}}}))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			path := test.makeFile(t, t.TempDir())
			reader := Reader{}
			page, cursor, err := reader.MessagePage(path, -1)
			if err != nil || len(page.Messages) != 1 || len(page.Messages[0].Pictures) != 1 {
				t.Fatalf("page: %+v %v", page, err)
			}
			var opened *pictureStreamFile
			var streamErr error
			open := func(path string) (messageReadFile, error) {
				file, err := openMessageFile(path)
				if err != nil {
					return nil, err
				}
				opened = &pictureStreamFile{messageReadFile: file, stream: func() { _, _, _, _, streamErr = reader.MessagesSince(path, cursor) }}
				return opened, nil
			}

			// Act
			media, data, err := reader.messagePicture(path, page.Conversation, page.Messages[0].Pictures[0].ID, open)

			// Assert
			if err != nil || media != "image/png" || string(data) != "picture A" {
				t.Fatalf("picture: %s %q %v", media, data, err)
			}
			if opened == nil || !opened.isStreamed {
				t.Fatal("the session's stream waited on the picture read")
			}
			if streamErr != nil {
				t.Fatalf("stream read: %v", streamErr)
			}
		})
	}
}

func TestPictureRejectsReplacedTranscriptBeforeAnotherPageRead(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("picture A"))
	tests := []struct {
		name     string
		makeFile func(*testing.T, string) string
	}{
		{"claude", func(t *testing.T, home string) string {
			return conversation(t, home, prompted("same", []object{imageBlock([]byte("picture A"))}))
		}},
		{"codex", func(t *testing.T, home string) string {
			return codexRollout(t, home, "picture", codexCompleted("UserMessage", object{"content": []object{{"type": "image", "url": "data:image/png;base64," + encoded}}}))
		}},
		{"pi", func(t *testing.T, home string) string {
			return piSession(t, home, piHeader(), piMessageEntry("same", "", "user", object{"content": []object{{"type": "image", "data": encoded, "mimeType": "image/png"}}}))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := test.makeFile(t, t.TempDir())
			reader := Reader{}
			page, _, err := reader.MessagePage(path, -1)
			if err != nil || len(page.Messages) != 1 || len(page.Messages[0].Pictures) != 1 {
				t.Fatalf("page: %+v %v", page, err)
			}
			id := page.Messages[0].Pictures[0].ID
			if media, data, err := reader.MessagePicture(path, page.Conversation, id); err != nil || media != "image/png" || string(data) != "picture A" {
				t.Fatalf("current picture: %s %q %v", media, data, err)
			}
			file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, err = file.WriteString(`{"unfinished`)
			if closeErr := file.Close(); err != nil || closeErr != nil {
				t.Fatalf("append partial entry: %v %v", err, closeErr)
			}
			if media, data, err := reader.MessagePicture(path, page.Conversation, id); err != nil || media != "image/png" || string(data) != "picture A" {
				t.Fatalf("picture with partial tail: %s %q %v", media, data, err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			replaced := strings.Replace(string(data), encoded, base64.StdEncoding.EncodeToString([]byte("picture B")), 1)
			if replaced == string(data) {
				t.Fatal("picture fixture missing")
			}
			if err := os.WriteFile(path, []byte(replaced), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, data, err := reader.MessagePicture(path, page.Conversation, id); !errors.Is(err, ErrNoRecord) || len(data) != 0 {
				t.Fatalf("stale picture returned %q, %v", data, err)
			}
		})
	}
}

func TestPictureRejectsEmptyDecodedImage(t *testing.T) {
	encoded := "\r\n\r\n"
	tests := []struct {
		name     string
		makeFile func(*testing.T, string) string
	}{
		{"claude", func(t *testing.T, home string) string {
			return conversation(t, home, prompted("same", []object{{"type": "image", "source": object{"type": "base64", "media_type": "image/png", "data": encoded}}}))
		}},
		{"codex", func(t *testing.T, home string) string {
			return codexRollout(t, home, "picture", codexCompleted("UserMessage", object{"content": []object{{"type": "image", "url": "data:image/png;base64," + encoded}}}))
		}},
		{"pi", func(t *testing.T, home string) string {
			return piSession(t, home, piHeader(), piMessageEntry("same", "", "user", object{"content": []object{{"type": "image", "data": encoded, "mimeType": "image/png"}}}))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := test.makeFile(t, t.TempDir())
			reader := Reader{}
			page, _, err := reader.MessagePage(path, -1)
			if err != nil || len(page.Messages) != 1 || len(page.Messages[0].Pictures) != 1 {
				t.Fatalf("page: %+v %v", page, err)
			}
			if _, data, err := reader.MessagePicture(path, page.Conversation, page.Messages[0].Pictures[0].ID); !errors.Is(err, ErrNoRecord) || len(data) != 0 {
				t.Fatalf("empty image: %q %v", data, err)
			}
		})
	}
}

func TestPictureIdentityAcrossReplacementAndTruncation(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("picture A"))
	tests := []struct {
		name     string
		makeFile func(*testing.T, string) string
	}{
		{"claude", func(t *testing.T, home string) string {
			return conversation(t, home, prompted("same", []object{imageBlock([]byte("picture A"))}), prompted("extra", "second"))
		}},
		{"codex", func(t *testing.T, home string) string {
			return codexRollout(t, home, "picture", codexCompleted("UserMessage", object{"content": []object{{"type": "image", "url": "data:image/png;base64," + encoded}}}), codexUserSaid("second"))
		}},
		{"pi", func(t *testing.T, home string) string {
			return piSession(t, home, piHeader(), piMessageEntry("same", "", "user", object{"content": []object{{"type": "image", "data": encoded, "mimeType": "image/png"}}}), piMessageEntry("extra", "same", "user", object{"content": "second"}))
		}},
	}
	for _, test := range tests {
		for _, mode := range []string{"replace", "truncate"} {
			t.Run(test.name+"/"+mode, func(t *testing.T) {
				path := test.makeFile(t, t.TempDir())
				reader := Reader{}
				page, _, err := reader.MessagePage(path, -1)
				if err != nil || len(page.Messages) < 1 || len(page.Messages[0].Pictures) != 1 {
					t.Fatalf("initial page: %+v %v", page, err)
				}
				id := page.Messages[0].Pictures[0].ID
				original, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				changed := strings.Replace(string(original), encoded, base64.StdEncoding.EncodeToString([]byte("picture B")), 1)
				if changed == string(original) {
					t.Fatal("picture fixture missing")
				}
				if mode == "truncate" {
					lines := strings.Split(strings.TrimSuffix(changed, "\n"), "\n")
					changed = strings.Join(lines[:len(lines)-1], "\n") + "\n"
					err = os.WriteFile(path, []byte(changed), 0o600)
				} else {
					err = fsx.AtomicWriteFile(path, []byte(changed))
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, data, err := reader.MessagePicture(path, page.Conversation, id); !errors.Is(err, ErrNoRecord) || len(data) != 0 {
					t.Fatalf("stale picture: %q %v", data, err)
				}
				next, _, err := reader.MessagePage(path, -1)
				if err != nil || next.Conversation == page.Conversation || len(next.Messages) == 0 || len(next.Messages[0].Pictures) != 1 || next.Messages[0].Pictures[0].ID != id {
					t.Fatalf("replacement page: %+v %v", next, err)
				}
				if media, data, err := reader.MessagePicture(path, next.Conversation, id); err != nil || media != "image/png" || string(data) != "picture B" {
					t.Fatalf("replacement picture: %s %q %v", media, data, err)
				}
			})
		}
	}
}

type messageReadRaceFile struct {
	messageReadFile
	readFrom int64
	change   func()
}

func (file *messageReadRaceFile) ReadAt(data []byte, offset int64) (int, error) {
	if offset >= file.readFrom {
		file.change()
	}
	return file.messageReadFile.ReadAt(data, offset)
}

type messageStatRaceFile struct {
	messageReadFile
	stats  int
	change func()
}

func (file *messageStatRaceFile) Stat() (os.FileInfo, error) {
	info, err := file.messageReadFile.Stat()
	file.stats++
	if err == nil && file.stats == 1 {
		file.change()
	}
	return info, err
}

type messageRepeatedRaceFile struct {
	messageReadFile
	reads  int
	change func()
}

func (file *messageRepeatedRaceFile) ReadAt(data []byte, offset int64) (int, error) {
	file.reads++
	if file.reads == 3 {
		file.change()
	}
	return file.messageReadFile.ReadAt(data, offset)
}

func TestMessageUpdateRejectsRewriteDuringIncrementalRead(t *testing.T) {
	tests := []struct {
		name     string
		makeFile func(*testing.T, string) (string, object)
		richFile func(*testing.T, string) (string, []object)
		update   func(*Reader, string, func() uint64, func(string) (messageReadFile, error)) error
		messages func(*Reader, string) []Message
		snapshot func(*Reader, string) string
	}{
		{"claude", func(t *testing.T, home string) (string, object) {
			return conversation(t, home, prompted("same", "first")), prompted("next", "more")
		}, func(t *testing.T, home string) (string, []object) {
			return conversation(t, home, prompted("same", []object{textBlock("first"), imageBlock([]byte("seed"))}), answer("a1", "reply", useBlock("call-1", "Read", object{"file_path": "a.go"}))), []object{
				answered("result", "call-1", "done", object{"stdout": "done"}, nil),
				prompted("next", []object{textBlock("more"), imageBlock([]byte("new image"))}),
				answer("a2", "reply-2", textBlock("new reply")),
			}
		}, func(r *Reader, path string, epoch func() uint64, open func(string) (messageReadFile, error)) error {
			return r.messageLogs[path].updateWith(context.Background(), epoch, 0, open)
		}, func(r *Reader, path string) []Message { return r.messageLogs[path].messages }, func(r *Reader, path string) string { return fmt.Sprintf("%#v", r.messageLogs[path]) }},
		{"codex", func(t *testing.T, home string) (string, object) {
			return codexRollout(t, home, "race", codexUserSaid("first")), codexUserSaid("more")
		}, func(t *testing.T, home string) (string, []object) {
			picture := base64.StdEncoding.EncodeToString([]byte("seed"))
			return codexRollout(t, home, "rich", codexTurnContext("model-one"), codexUserSaid("first"), codexCompleted("UserMessage", object{"content": []object{{"type": "image", "url": "data:image/png;base64," + picture}}}), codexCall("call-1", "exec", "run"), codexLine("token_usage_record", object{"usage": object{"input_tokens": 10, "output_tokens": 2, "total_tokens": 12}})), []object{
				codexOutput("call-1", "done"), codexTurnContext("model-two"), codexLine("token_usage_record", object{"usage": object{"input_tokens": 20, "output_tokens": 4, "total_tokens": 24}}), codexUserSaid("more"), codexCompleted("UserMessage", object{"id": "image-2", "content": []object{{"type": "image", "url": "data:image/png;base64,bmV3"}}}),
			}
		}, func(r *Reader, path string, epoch func() uint64, open func(string) (messageReadFile, error)) error {
			return r.codexLogs[path].updateWith(context.Background(), epoch, open)
		}, func(r *Reader, path string) []Message { return r.codexLogs[path].messages }, func(r *Reader, path string) string { return fmt.Sprintf("%#v", r.codexLogs[path]) }},
		{"pi", func(t *testing.T, home string) (string, object) {
			return piSession(t, home, piHeader(), piMessageEntry("same", "", "user", object{"content": "first"})), piMessageEntry("next", "same", "user", object{"content": "more"})
		}, func(t *testing.T, home string) (string, []object) {
			return piSession(t, home, piHeader(), piMessageEntry("same", "", "user", object{"content": []object{{"type": "text", "text": "first"}, {"type": "image", "data": "c2VlZA==", "mimeType": "image/png"}}}), piMessageEntry("a1", "same", "assistant", object{"model": "model-one", "usage": object{"input": 7, "output": 2}, "content": []object{{"type": "toolCall", "id": "call-1", "name": "read", "arguments": object{"path": "a.go"}}}})), []object{
				piMessageEntry("result", "a1", "toolResult", object{"toolCallId": "call-1", "toolName": "read", "content": []object{{"type": "image", "data": "bmV3", "mimeType": "image/png"}}}),
				piMessageEntry("next", "result", "assistant", object{"model": "model-two", "usage": object{"input": 12, "output": 5}, "content": []object{{"type": "text", "text": "more"}}}),
			}
		}, func(r *Reader, path string, epoch func() uint64, open func(string) (messageReadFile, error)) error {
			return r.piLogs[path].updateWith(context.Background(), epoch, open)
		}, func(r *Reader, path string) []Message { return r.piLogs[path].messages }, func(r *Reader, path string) string { return fmt.Sprintf("%#v", r.piLogs[path]) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path, next := test.makeFile(t, t.TempDir())
			reader := Reader{}
			_, cursorBefore, err := reader.MessagePage(path, -1)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			appendLines(t, path, next)
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			replacement := strings.ReplaceAll(strings.ReplaceAll(string(original), "first", "other"), "more", "else")
			if len(replacement) != len(original) || replacement == string(original) {
				t.Fatal("invalid fixture")
			}
			changed := false
			open := func(path string) (messageReadFile, error) {
				file, err := openMessageFile(path)
				if err != nil {
					return nil, err
				}
				return &messageReadRaceFile{messageReadFile: file, readFrom: info.Size(), change: func() {
					if changed {
						return
					}
					changed = true
					if err := os.WriteFile(path, []byte(replacement), 0o600); err != nil {
						t.Fatal(err)
					}
				}}, nil
			}
			var generation uint64 = 100
			err = test.update(&reader, path, func() uint64 { generation++; return generation }, open)
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(test.messages(&reader, path))
			if err != nil {
				t.Fatal(err)
			}
			if !changed || strings.Contains(string(got), "first") || !strings.Contains(string(got), "other") || !strings.Contains(string(got), "else") {
				t.Fatalf("update published mixed generation: %s (rewrite %t)", got, changed)
			}
			_, _, cursorAfter, isAnew, err := reader.MessagesSince(path, cursorBefore)
			if err != nil || !isAnew || (cursorAfter.epoch == cursorBefore.epoch && cursorAfter.incarnation == cursorBefore.incarnation) {
				t.Fatalf("replacement did not change cursor: before %+v after %+v anew %t error %v", cursorBefore, cursorAfter, isAnew, err)
			}
			path, richDelta := test.richFile(t, t.TempDir())
			reader = Reader{}
			if _, _, err := reader.MessagePage(path, -1); err != nil {
				t.Fatal(err)
			}
			appendLines(t, path, richDelta...)
			before := test.snapshot(&reader, path)
			opens, allocations := 0, 0
			openAgain := func(path string) (messageReadFile, error) {
				file, err := openMessageFile(path)
				if err != nil {
					return nil, err
				}
				opens++
				return &messageRepeatedRaceFile{messageReadFile: file, change: func() {
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					text := string(data)
					if strings.Contains(text, "other") {
						text = strings.ReplaceAll(text, "other", "first")
					} else {
						text = strings.ReplaceAll(text, "first", "other")
					}
					if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
						t.Fatal(err)
					}
				}}, nil
			}
			err = test.update(&reader, path, func() uint64 { allocations++; return 999 }, openAgain)
			if err == nil || opens != 2 || allocations != 0 || test.snapshot(&reader, path) != before {
				t.Fatalf("unstable read changed state: error=%v opens=%d epoch allocations=%d", err, opens, allocations)
			}

			appendPath, firstNext := test.makeFile(t, t.TempDir())
			appendReader := Reader{}
			if _, _, err := appendReader.MessagePage(appendPath, -1); err != nil {
				t.Fatal(err)
			}
			initial, err := os.Stat(appendPath)
			if err != nil {
				t.Fatal(err)
			}
			appendLines(t, appendPath, firstNext)
			last, err := json.Marshal(firstNext)
			if err != nil {
				t.Fatal(err)
			}
			lastText := strings.ReplaceAll(strings.ReplaceAll(string(last), "next", "last"), "more", "else")
			lastText = strings.ReplaceAll(lastText, `"parentId":"same"`, `"parentId":"next"`)
			mid := len(lastText) / 2
			appendText(t, appendPath, lastText[:mid])
			appended := false
			appendOpen := func(path string) (messageReadFile, error) {
				file, err := openMessageFile(path)
				if err != nil {
					return nil, err
				}
				return &messageReadRaceFile{messageReadFile: file, readFrom: initial.Size(), change: func() {
					if !appended {
						appended = true
						appendText(t, path, lastText[mid:]+"\n")
					}
				}}, nil
			}
			err = test.update(&appendReader, appendPath, func() uint64 { return 999 }, appendOpen)
			if err != nil {
				t.Fatal(err)
			}
			staged, err := json.Marshal(test.messages(&appendReader, appendPath))
			if err != nil {
				t.Fatal(err)
			}
			if !appended || !strings.Contains(string(staged), "more") || strings.Contains(string(staged), "else") {
				t.Fatalf("read crossed its fixed extent: %s (append %t)", staged, appended)
			}
			if err := test.update(&appendReader, appendPath, func() uint64 { return 999 }, openMessageFile); err != nil {
				t.Fatal(err)
			}
			afterAppend, err := json.Marshal(test.messages(&appendReader, appendPath))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(afterAppend), "else") {
				t.Fatalf("completed trailing line missing: %s", afterAppend)
			}

			statPath, statNext := test.makeFile(t, t.TempDir())
			statReader := Reader{}
			_, cursor, err := statReader.MessagePage(statPath, -1)
			if err != nil {
				t.Fatal(err)
			}
			appendLines(t, statPath, statNext)
			statAppended, statOpens, statEpochs := false, 0, 0
			statOpen := func(path string) (messageReadFile, error) {
				file, err := openMessageFile(path)
				if err != nil {
					return nil, err
				}
				statOpens++
				return &messageStatRaceFile{messageReadFile: file, change: func() {
					if !statAppended {
						statAppended = true
						appendText(t, path, lastText+"\n")
					}
				}}, nil
			}
			if err := test.update(&statReader, statPath, func() uint64 { statEpochs++; return 999 }, statOpen); err != nil {
				t.Fatal(err)
			}
			statMessages, err := json.Marshal(test.messages(&statReader, statPath))
			if err != nil {
				t.Fatal(err)
			}
			if !statAppended || statOpens != 1 || statEpochs != 0 || !strings.Contains(string(statMessages), "more") || strings.Contains(string(statMessages), "else") {
				t.Fatalf("append after stat reset or crossed extent: %s (append %t opens %d epochs %d)", statMessages, statAppended, statOpens, statEpochs)
			}
			later, _, laterCursor, isAnew, err := statReader.MessagesSince(statPath, cursor)
			if err != nil || isAnew || laterCursor.epoch != cursor.epoch || laterCursor.incarnation != cursor.incarnation {
				t.Fatalf("append after stat changed conversation: %+v %t %v", laterCursor, isAnew, err)
			}
			laterJSON, err := json.Marshal(later)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(laterJSON), "else") {
				t.Fatalf("later append missing: %s", laterJSON)
			}
		})
	}
}

func TestMessagePageConversationIdentityTracksTranscriptGeneration(t *testing.T) {
	tests := []struct {
		name     string
		makeFile func(*testing.T, string) (string, object)
	}{
		{"claude", func(t *testing.T, home string) (string, object) {
			return conversation(t, home, prompted("same", "first")), prompted("next", "more")
		}},
		{"codex", func(t *testing.T, home string) (string, object) {
			return codexRollout(t, home, "identity", codexUserSaid("first")), codexUserSaid("more")
		}},
		{"pi", func(t *testing.T, home string) (string, object) {
			return piSession(t, home, piHeader(), piMessageEntry("same", "", "user", object{"content": "first"})), piMessageEntry("next", "same", "user", object{"content": "more"})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path, next := test.makeFile(t, t.TempDir())
			reader := Reader{}
			identity := func(before int) string {
				t.Helper()
				page, _, err := reader.MessagePage(path, before)
				if err != nil {
					t.Fatal(err)
				}
				data, err := json.Marshal(page)
				if err != nil {
					t.Fatal(err)
				}
				var wire struct {
					Conversation string `json:"conversation"`
				}
				if err := json.Unmarshal(data, &wire); err != nil {
					t.Fatal(err)
				}
				return wire.Conversation
			}
			first := identity(-1)
			if first == "" || strings.Contains(first, path) {
				t.Fatalf("page identity %q must be nonempty and opaque", first)
			}
			appendLines(t, path, next)
			if got := identity(-1); got != first {
				t.Fatalf("append changed identity: %q to %q", first, got)
			}
			if got := identity(1); got != first {
				t.Fatalf("older page identity = %q, want %q", got, first)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			replaced := strings.Replace(string(data), "first", "other", 1)
			if replaced == string(data) {
				t.Fatal("fixture does not contain first")
			}
			if err := os.WriteFile(path, []byte(replaced), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := identity(-1); got == "" || got == first {
				t.Fatalf("same-size rewrite retained identity %q", got)
			}
		})
	}
}

func TestPiPageConversationIdentityChangesOnBranchSwitch(t *testing.T) {
	_, path := piConversation(t)
	reader := Reader{}
	identity := func() string {
		page, _, err := reader.MessagePage(path, -1)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			Conversation string `json:"conversation"`
		}
		if err := json.Unmarshal(data, &wire); err != nil {
			t.Fatal(err)
		}
		return wire.Conversation
	}
	before := identity()
	appendLines(t, path, piMessageEntry("u4", "a3", "user", object{"content": "Back to it"}))
	after := identity()
	if before == "" || after == "" || before == after {
		t.Fatalf("branch identities %q and %q must differ", before, after)
	}
}

func TestWholeReadRejectsReplacedConversationWithMatchingMessages(t *testing.T) {
	path := conversation(t, t.TempDir(), prompted("same", "first"))
	reader := Reader{}
	before, _, err := reader.MessagePage(path, -1)
	if err != nil || len(before.Messages) != 1 || before.Messages[0].Text != "first" {
		t.Fatalf("initial page = %+v, %v", before, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	replaced := strings.Replace(string(data), "first", "other", 1)
	if replaced == string(data) {
		t.Fatal("fixture text missing")
	}
	if err := os.WriteFile(path, []byte(replaced), 0o600); err != nil {
		t.Fatal(err)
	}
	log := reader.messageLogs[path]
	log.mu.Lock()
	log.readWhole(func() uint64 { return messageEpoch.Add(1) })
	done := log.whole
	log.mu.Unlock()
	<-done
	log.mu.Lock()
	after := log.page(-1)
	after.Conversation = (MessageCursor{log.epoch, log.revision, log.incarnation}).Conversation()
	err = log.wholeErr
	log.mu.Unlock()
	if err != nil || len(after.Messages) != 1 || after.Messages[0].Text != "other" {
		t.Fatalf("whole-read replacement page = %+v, %v", after, err)
	}
	if after.Conversation == "" || after.Conversation == before.Conversation || after.Messages[0].ID != before.Messages[0].ID || after.Messages[0].Seq != before.Messages[0].Seq {
		t.Fatalf("replacement identity or message coordinates: before %+v, after %+v", before, after)
	}
}

func TestConversationIdentityChangesOnInteriorRewriteWithIncompleteTail(t *testing.T) {
	content := strings.Repeat("x", 10000) + "first" + strings.Repeat("y", 10000)
	tests := []struct {
		name     string
		makeFile func(*testing.T, string) string
	}{
		{"claude", func(t *testing.T, home string) string { return conversation(t, home, prompted("same", content)) }},
		{"codex", func(t *testing.T, home string) string {
			return codexRollout(t, home, "interior", codexUserSaid(content))
		}},
		{"pi", func(t *testing.T, home string) string {
			return piSession(t, home, piHeader(), piMessageEntry("same", "", "user", object{"content": content}))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := test.makeFile(t, t.TempDir())
			file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.WriteString(`{"type":"unfinished`); err != nil {
				file.Close()
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			reader := Reader{}
			first, _, err := reader.MessagePage(path, -1)
			if err != nil || len(first.Messages) != 1 || !strings.Contains(first.Messages[0].Text, "first") {
				t.Fatalf("initial page = %+v, %v", first, err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			replaced := strings.Replace(string(data), "first", "other", 1)
			if replaced == string(data) {
				t.Fatal("fixture text missing")
			}
			if err := os.WriteFile(path, []byte(replaced), 0o600); err != nil {
				t.Fatal(err)
			}
			modified := info.ModTime().Add(time.Second)
			if err := os.Chtimes(path, modified, modified); err != nil {
				t.Fatal(err)
			}
			updated, _, err := reader.MessagePage(path, -1)
			if err != nil || updated.Conversation == "" || updated.Conversation == first.Conversation || len(updated.Messages) != 1 || !strings.Contains(updated.Messages[0].Text, "other") {
				t.Fatalf("rewritten page = %+v, %v; previous identity %q", updated, err, first.Conversation)
			}
		})
	}
}

func TestConversationIdentitySurvivesIdleEviction(t *testing.T) {
	tests := []struct {
		name     string
		makeFile func(*testing.T, string, string) (string, object, object)
	}{
		{"claude", func(t *testing.T, home, id string) (string, object, object) {
			path := filepath.Join(home, ".claude", "projects", "C--work-repo", id+".jsonl")
			writeLines(t, path, at, prompted("same", "first"))
			return path, prompted("next", "more"), prompted("branch", "other")
		}},
		{"codex", func(t *testing.T, home, id string) (string, object, object) {
			return codexRollout(t, home, id, codexUserSaid("first")), codexUserSaid("more"), codexUserSaid("other")
		}},
		{"pi", func(t *testing.T, home, id string) (string, object, object) {
			path := filepath.Join(home, ".pi", "agent", "sessions", "--C--work-repo--", "20261006_213000_"+id+".jsonl")
			writeLines(t, path, at, piHeader(), piMessageEntry("same", "", "user", object{"content": "first"}), piMessageEntry("leaf", "same", "user", object{"content": "second"}))
			return path, piMessageEntry("next", "leaf", "user", object{"content": "more"}), piMessageEntry("branch", "same", "user", object{"content": "other"})
		}},
	}
	for _, test := range tests {
		for _, scenario := range []string{"append", "replace", "rename", "branch"} {
			if scenario == "branch" && test.name != "pi" {
				continue
			}
			t.Run(test.name+"/"+scenario, func(t *testing.T) {
				home := t.TempDir()
				path, next, branch := test.makeFile(t, home, "one")
				other, _, _ := test.makeFile(t, home, "two")
				now := time.Now()
				reader := Reader{Now: func() time.Time { return now }}
				page := func(path string, before int) MessagePage {
					t.Helper()
					result, _, err := reader.MessagePage(path, before)
					if err != nil {
						t.Fatal(err)
					}
					return result
				}
				first, oldCursor, err := reader.MessagePage(path, -1)
				if err != nil {
					t.Fatal(err)
				}
				now = now.Add(messageLogIdle + time.Minute)
				page(other, -1)
				if reader.messageLogs[path] != nil || reader.codexLogs[path] != nil || reader.piLogs[path] != nil {
					t.Fatal("idle transcript body was not evicted")
				}
				switch scenario {
				case "append":
					appendLines(t, path, next)
				case "branch":
					appendLines(t, path, branch)
				case "replace":
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					replaced := strings.Replace(string(data), "first", "other", 1)
					if replaced == string(data) {
						t.Fatal("fixture text missing")
					}
					if err := os.WriteFile(path, []byte(replaced), 0o600); err != nil {
						t.Fatal(err)
					}
				case "rename":
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(path, path+".old"); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, data, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				reconnected := page(path, -1)
				if first.Conversation == "" || reconnected.Conversation == "" {
					t.Fatal("empty identity")
				}
				if scenario == "append" && reconnected.Conversation != first.Conversation {
					t.Fatalf("ordinary reconnect changed identity: %q to %q", first.Conversation, reconnected.Conversation)
				}
				if scenario != "append" && reconnected.Conversation == first.Conversation {
					t.Fatalf("%s kept identity %q", scenario, first.Conversation)
				}
				if older := page(path, 1); older.Conversation != reconnected.Conversation {
					t.Fatalf("older page identity %q differs from %q", older.Conversation, reconnected.Conversation)
				}
				if changes, _, _, isAnew, err := reader.MessagesSince(path, oldCursor); err != nil || !isAnew || len(changes) != 0 {
					t.Fatalf("evicted cursor returned changes %+v, anew %t, error %v", changes, isAnew, err)
				}
			})
		}
	}
}

// The entries below are made up in the shape Claude Code 2.1.308 writes a
// conversation in: one entry per line, each block of an answer an entry of
// its own under the answer's id.

const workDir = `C:\work\repo`

func stamped(entry object) object {
	entry["timestamp"] = at.Format(time.RFC3339Nano)
	entry["cwd"] = workDir
	return entry
}

// prompted is a prompt as Claude Code records one it took.
func prompted(uuid string, content any) object {
	return stamped(object{"type": "user", "uuid": uuid, "message": object{"role": "user", "content": content}})
}

// answer is one block of the answer id.
func answer(uuid, id string, block object) object {
	return stamped(object{"type": "assistant", "uuid": uuid, "message": object{"id": id, "model": "claude-opus-5-5", "role": "assistant", "content": []object{block}}})
}

// answered is a call's result and what Claude Code recorded of it, with
// fields of the entry.
func answered(uuid, call string, content any, recorded any, fields object) object {
	entry := stamped(object{"type": "user", "uuid": uuid, "message": object{"role": "user", "content": []object{{"type": "tool_result", "tool_use_id": call, "content": content}}}, "toolUseResult": recorded})
	for key, value := range fields {
		entry[key] = value
	}
	return entry
}

func failedResult(uuid, call, content string) object {
	entry := answered(uuid, call, content, "Error: "+content, nil)
	entry["message"].(object)["content"].([]object)[0]["is_error"] = true
	return entry
}

func textBlock(text string) object { return object{"type": "text", "text": text} }

func thinkingBlock(text string) object {
	return object{"type": "thinking", "thinking": text, "signature": "c2lnbmF0dXJl"}
}

func useBlock(id, name string, input object) object {
	return object{"type": "tool_use", "id": id, "name": name, "input": input}
}

func imageBlock(data []byte) object {
	return object{"type": "image", "source": object{"type": "base64", "media_type": "image/png", "data": base64.StdEncoding.EncodeToString(data)}}
}

func system(uuid, subtype string, fields object) object {
	entry := stamped(object{"type": "system", "subtype": subtype, "uuid": uuid})
	for key, value := range fields {
		entry[key] = value
	}
	return entry
}

func attachment(uuid string, fields object) object {
	return stamped(object{"type": "attachment", "uuid": uuid, "attachment": fields})
}

// conversation writes entries as a Claude Code conversation under home and
// returns its path.
func conversation(t *testing.T, home string, entries ...any) string {
	t.Helper()
	path := filepath.Join(home, ".claude", "projects", "C--work-repo", "session-1.jsonl")
	writeLines(t, path, at, entries...)
	return path
}

func prompt(seq int, id, source, text string) Message {
	return Message{ID: id, Seq: seq, Kind: MessagePrompt, At: at, Source: source, Text: text}
}

func reply(seq int, id string, blocks ...Block) Message {
	return Message{ID: id, Seq: seq, Kind: MessageReply, At: at, Model: "claude-opus-5-5", Blocks: blocks}
}

func callBlock(id, tool, subject, state, result string) Block {
	return Block{Kind: BlockCall, CallID: id, Tool: tool, Subject: subject, State: state, Result: result}
}

// entryOffsets is where each entry of the conversation at path begins.
func entryOffsets(t *testing.T, path string) []int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var offsets []int
	for offset := 0; offset < len(data); {
		offsets = append(offsets, offset)
		end := strings.IndexByte(string(data[offset:]), '\n')
		if end < 0 {
			break
		}
		offset += end + 1
	}
	return offsets
}

// numbered is messages as a test names them: each seq, and the offset that
// begins each picture's id, as the number of the entry of the conversation at
// path it is the offset of.
func numbered(t *testing.T, path string, messages []Message) []Message {
	t.Helper()
	numbers := map[int]int{}
	for number, offset := range entryOffsets(t, path) {
		numbers[offset] = number
	}
	number := func(offset int) int {
		t.Helper()
		number, isEntry := numbers[offset]
		if !isEntry {
			t.Fatalf("%d begins no entry", offset)
		}
		return number
	}
	renamed := func(pictures []Picture) []Picture {
		var named []Picture
		for _, picture := range pictures {
			offset, rest, _ := strings.Cut(picture.ID, ".")
			at, err := strconv.Atoi(offset)
			if err != nil {
				t.Fatalf("picture id %q begins with no offset", picture.ID)
			}
			picture.ID = strconv.Itoa(number(at)) + "." + rest
			named = append(named, picture)
		}
		return named
	}
	named := make([]Message, 0, len(messages))
	for _, message := range messages {
		message.Seq = number(message.Seq)
		message.Pictures = renamed(message.Pictures)
		message.Blocks = slices.Clone(message.Blocks)
		for i := range message.Blocks {
			message.Blocks[i].Pictures = renamed(message.Blocks[i].Pictures)
		}
		named = append(named, message)
	}
	return named
}

// beyondTheTail is what Claude Code keeps for its model alone, past a tail's
// reach.
func beyondTheTail() []any {
	kept := `{"type":"api-request-blob","sessionId":"session-1","message":{"content":"` + strings.Repeat("x", 64<<10) + `"}}`
	var entries []any
	for range messageTailReach/len(kept) + 16 {
		entries = append(entries, kept)
	}
	return entries
}

// longConversation is a conversation longer than a tail's reach: a first
// prompt with a picture and an answer whose call is settled, and whose last
// block is written, past the reach, then sixty prompts, the last with a
// picture, and the answer to it.
func longConversation() []any {
	first := answer("a1", "msg_first", useBlock("toolu_1", "Bash", object{"command": "go test ./..."}))
	first["message"].(object)["usage"] = object{"input_tokens": 1000, "output_tokens": 10}
	entries := append([]any{prompted("u-first", []object{imageBlock([]byte("the first picture")), textBlock("the first words")}), first}, beyondTheTail()...)
	entries = append(entries, answered("u-result", "toolu_1", "ok", object{"stdout": "ok"}, nil), answer("a1-end", "msg_first", textBlock("The suite passed.")))
	for i := range 59 {
		entries = append(entries, prompted("u"+strconv.Itoa(i), "prompt "+strconv.Itoa(i)))
	}
	last := answer("a2", "msg_last", textBlock("the last words"))
	last["message"].(object)["usage"] = object{"input_tokens": 2000, "output_tokens": 20}
	return append(entries, prompted("u-last", []object{imageBlock([]byte("the last picture")), textBlock("prompt 59")}), last)
}

// A long conversation's newest page comes from a read of its tail, and the
// read of the whole conversation beside it changes nothing a view holds: each
// message keeps its seq, id and pictures, a view following the tail's page is
// sent nothing again, an answer begun before the tail shows only whole, and
// the older pages and what the session spent come from the whole.
func TestALongConversationReadsTheSameOnceReadWhole(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := conversation(t, home, longConversation()...)
	reader := Reader{Home: home}
	newest, cursor, err := reader.MessagePage(path, -1)
	if err != nil || len(newest.Messages) != MessagesPerPage || !newest.More {
		t.Fatalf("MessagePage = %d messages, more %t, %v; want a full page and more", len(newest.Messages), newest.More, err)
	}

	// Act
	older, _, olderErr := reader.MessagePage(path, newest.Messages[0].Seq)
	changed, _, _, isAnew, sinceErr := reader.MessagesSince(path, cursor)
	again, _, againErr := reader.MessagePage(path, -1)

	// Assert
	if newest.Usage != nil {
		t.Errorf("the tail's page carries usage %+v; want none until the whole conversation is read", newest.Usage)
	}
	if olderErr != nil || len(older.Messages) != 13 || older.More || older.Messages[0].Text != "the first words" || older.Messages[12].Text != "prompt 10" || newest.Messages[0].Text != "prompt 11" {
		t.Fatalf("the page before the newest = %d messages, more %t, %v; want the first words to prompt 10 and no more", len(older.Messages), older.More, olderErr)
	}
	if first := older.Messages[1]; len(first.Blocks) != 2 || first.Blocks[0].State != CallDone || first.Blocks[0].Result != "ok" || first.Blocks[1].Text != "The suite passed." {
		t.Errorf("the first answer = %+v; want its call done with the result read past the tail's reach, and its last block", first.Blocks)
	}
	held := append(slices.Clone(older.Messages), newest.Messages...)
	for i := 1; i < len(held); i++ {
		if held[i].Seq <= held[i-1].Seq {
			t.Errorf("%s at seq %d follows %s at seq %d; want the seqs of the pages rising", held[i].ID, held[i].Seq, held[i-1].ID, held[i-1].Seq)
			break
		}
	}
	if sinceErr != nil || isAnew || len(changed) != 0 {
		t.Errorf("MessagesSince the tail's page = %d changed, anew %t, %v; want nothing sent again", len(changed), isAnew, sinceErr)
	}
	if againErr != nil || !reflect.DeepEqual(again.Messages, newest.Messages) {
		t.Errorf("the newest page once read whole = %+v, %v\nwant the tail's page %+v", again.Messages, againErr, newest.Messages)
	}
	if again.Usage == nil || again.Usage.InputTokens != 3000 || again.Usage.OutputTokens != 30 {
		t.Errorf("usage once read whole = %+v; want both answers counted", again.Usage)
	}
	for _, tc := range []struct {
		message Message
		want    string
	}{
		{older.Messages[0], "the first picture"},
		{newest.Messages[48], "the last picture"},
	} {
		if len(tc.message.Pictures) != 1 {
			t.Fatalf("message %s holds pictures %+v; want one", tc.message.ID, tc.message.Pictures)
		}
		if _, data, err := reader.MessagePicture(path, newest.Conversation, tc.message.Pictures[0].ID); err != nil || string(data) != tc.want {
			t.Errorf("MessagePicture(%s) = %q, %v; want %q", tc.message.Pictures[0].ID, data, err, tc.want)
		}
	}
}

// A message written while a long conversation is read whole comes to a view
// of its tail's page as a change, the same as one written later, whichever
// read finds it.
func TestAMessageWrittenWhileTheWholeIsReadComesAsAChange(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := conversation(t, home, longConversation()...)
	reader := Reader{Home: home}
	newest, cursor, err := reader.MessagePage(path, -1)
	if err != nil || len(newest.Messages) == 0 {
		t.Fatalf("MessagePage = %d messages, %v; want the newest page", len(newest.Messages), err)
	}
	appendLines(t, path, prompted("u-new", "written meanwhile"))

	// Act
	_, _, olderErr := reader.MessagePage(path, newest.Messages[0].Seq)
	changed, _, _, isAnew, sinceErr := reader.MessagesSince(path, cursor)

	// Assert
	if olderErr != nil || sinceErr != nil || isAnew || len(changed) != 1 || changed[0].ID != "u-new" {
		t.Errorf("MessagesSince = %+v, anew %t, %v, %v; want the prompt written meanwhile alone", changed, isAnew, sinceErr, olderErr)
	}
}

// A long conversation whose tail begins no message, an answer begun before
// it and nothing after, shows nothing from the tail and has a view take the
// newest page anew once the whole is read.
func TestALongConversationWhoseTailHoldsNoMessageIsPagedAnew(t *testing.T) {
	// Arrange
	home := t.TempDir()
	entries := append([]any{prompted("u1", "map the repo"), answer("a1", "msg_1", textBlock("Reading it."))}, beyondTheTail()...)
	path := conversation(t, home, append(entries, answer("a2", "msg_1", textBlock("It holds two packages.")))...)
	reader := Reader{Home: home}
	tail, cursor, err := reader.MessagePage(path, -1)

	// Act
	_, _, beforeErr := reader.MessagePage(path, 0)
	_, _, _, isAnew, sinceErr := reader.MessagesSince(path, cursor)
	whole, _, wholeErr := reader.MessagePage(path, -1)

	// Assert
	if err != nil || len(tail.Messages) != 0 || !tail.More {
		t.Errorf("the tail's page = %+v, more %t, %v; want no message and more", tail.Messages, tail.More, err)
	}
	if beforeErr != nil || sinceErr != nil || !isAnew {
		t.Errorf("MessagesSince once read whole = anew %t, %v, %v; want the view to take the page anew", isAnew, sinceErr, beforeErr)
	}
	if wholeErr != nil || len(whole.Messages) != 2 || whole.More || len(whole.Messages[1].Blocks) != 2 {
		t.Errorf("the newest page once read whole = %+v, more %t, %v; want the prompt and its whole answer", whole.Messages, whole.More, wholeErr)
	}
}

// Each kind of entry a Claude Code conversation holds reads as the message
// the message view shows: prompts with where they came from, answers
// grouped by their id, calls with their outcome and what came back, edits,
// sub-agents, commands, compactions and interrupts, and nothing of what
// Claude Code records for its model alone.
func TestAnAnswerSaysWhetherItCameFromTheBoardOrItsPage(t *testing.T) {
	for _, test := range []struct{ text, source string }{
		{"The Overlord answered your question on the board. Answer: yes", FromAnswer},
		{"The Overlord answered your question on its page. Answer: yes", FromPage},
		{"CEO: answered on your review page https://example/page: Build it as shown", FromPage},
	} {
		source, _ := promptSource(test.text)
		if source != test.source {
			t.Errorf("promptSource(%q) = %q, want %q", test.text, source, test.source)
		}
	}
}

// A message he typed to a goblin on the board reaches its terminal behind the
// CEO's stamp and the board's, and reads back as his own words, so the board
// can show it as his and as read. The CEO's own steer stays the CEO's, and a
// command he typed stays as he typed it.
func TestABoardMessageToAGoblinReadsAsHisOwnWords(t *testing.T) {
	for _, tc := range []struct{ name, text, source, words string }{
		{"his message from the board", routing.SteerPrefix + routing.BoardMessagePrefix + "check the deploy before you merge", FromOverlord, "check the deploy before you merge"},
		{"the CEO's steer", routing.SteerPrefix + "rebase on main", FromCEO, "rebase on main"},
		{"his command", "/compact", FromOverlord, "/compact"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			source, words := promptSource(tc.text)

			// Assert
			if source != tc.source || words != tc.words {
				t.Errorf("promptSource(%q) = %q, %q; want %q, %q", tc.text, source, words, tc.source, tc.words)
			}
		})
	}
}

func TestMessagesReadEachKindOfEntry(t *testing.T) {
	session := func(home string) string {
		return filepath.Join(home, ".claude", "projects", "C--work-repo", "session-1")
	}
	for _, tc := range []struct {
		name    string
		entries func(t *testing.T, home string) []any
		want    []Message
	}{
		{
			name: "an answer's blocks group under its id, a result written between them",
			entries: func(*testing.T, string) []any {
				return []any{
					prompted("u1", "Read the readme"),
					answer("a1", "msg_1", thinkingBlock("")),
					answer("a2", "msg_1", thinkingBlock("The readme says what the repo is.")),
					answer("a3", "msg_1", useBlock("toolu_1", "Read", object{"file_path": workDir + `\README.md`})),
					answered("u2", "toolu_1", "1\t# Repo", object{"type": "text"}, nil),
					answer("a4", "msg_1", textBlock("It names the repo.")),
					answer("a5", "msg_2", textBlock("Done.")),
				}
			},
			want: []Message{
				prompt(0, "u1", FromOverlord, "Read the readme"),
				reply(2, "msg_1", Block{Kind: BlockThinking, Text: "The readme says what the repo is."}, callBlock("toolu_1", "Read", "README.md", CallDone, "1\t# Repo"), Block{Kind: BlockText, Text: "It names the repo."}),
				reply(6, "msg_2", Block{Kind: BlockText, Text: "Done."}),
			},
		},
		{
			name: "prompts name where they came from",
			entries: func(*testing.T, string) []any {
				return []any{
					prompted("u1", `Read C:\work\state\instruction.md and follow it exactly: it is your instruction from the CEO.`),
					prompted("u2", "CEO: merge origin/main first"),
					prompted("u3", "CEO: The Overlord answered your question on the board. Question: Which first? Answer: Fix it next"),
					prompted("u4", "User answer to CEO question q-1. Question: Ship it? Answer: Yes"),
					prompted("u5", "Overlord: keep the tests\n<system-reminder>Claude Code's own note</system-reminder>"),
				}
			},
			want: []Message{
				prompt(0, "u1", FromCEO, `Read C:\work\state\instruction.md and follow it exactly: it is your instruction from the CEO.`),
				prompt(1, "u2", FromCEO, "merge origin/main first"),
				prompt(2, "u3", FromAnswer, "The Overlord answered your question on the board. Question: Which first? Answer: Fix it next"),
				prompt(3, "u4", FromAnswer, "User answer to CEO question q-1. Question: Ship it? Answer: Yes"),
				prompt(4, "u5", FromOverlord, "Overlord: keep the tests"),
			},
		},
		{
			name: "a queued prompt is marked queued, taken mid-turn or as the next turn",
			entries: func(*testing.T, string) []any {
				queuedPrompt := prompted("u2", "check the phone too")
				queuedPrompt["promptSource"] = "queued"
				return []any{
					attachment("q1", object{"type": "queued_command", "commandMode": "prompt", "prompt": `CEO: also run the specs <pasted_content id="07">now</pasted_content id="07">`}),
					queuedPrompt,
					attachment("q2", object{"type": "queued_command", "commandMode": "prompt", "prompt": []object{textBlock("look at this"), imageBlock([]byte("png"))}}),
				}
			},
			want: []Message{
				{ID: "q1", Seq: 0, Kind: MessagePrompt, At: at, Source: FromCEO, IsQueued: true, Text: "also run the specs now"},
				{ID: "u2", Seq: 1, Kind: MessagePrompt, At: at, Source: FromOverlord, IsQueued: true, Text: "check the phone too"},
				{ID: "q2", Seq: 2, Kind: MessagePrompt, At: at, Source: FromOverlord, IsQueued: true, Text: "look at this"},
			},
		},
		{
			// Claude Code 2.1.323 put a mark right after the path of each
			// picture it took into a running turn with the line that names it,
			// live on 2026-11-13, so his message read as some other words and
			// drew no picture (#2541). The row is his line as he sent it, whose
			// paths the board draws as its pictures; a mark he typed is his.
			name: "a line taken into a running turn with its pictures reads as he sent it",
			entries: func(*testing.T, string) []any {
				return []any{
					attachment("q1", object{"type": "queued_command", "commandMode": "prompt", "prompt": []object{textBlock(`look at this C:\goblins\state\composer-files\pasted-4f2a91c7.png[Image #50] C:\goblins\state\composer-files\pasted-0b8e3d52.JPG[Image #51]`), imageBlock([]byte("png")), imageBlock([]byte("jpg"))}}),
					attachment("q2", object{"type": "queued_command", "commandMode": "prompt", "prompt": "the third row reads [Image #2] here"}),
				}
			},
			want: []Message{
				{ID: "q1", Seq: 0, Kind: MessagePrompt, At: at, Source: FromOverlord, IsQueued: true, Text: `look at this C:\goblins\state\composer-files\pasted-4f2a91c7.png C:\goblins\state\composer-files\pasted-0b8e3d52.JPG`},
				{ID: "q2", Seq: 1, Kind: MessagePrompt, At: at, Source: FromOverlord, IsQueued: true, Text: "the third row reads [Image #2] here"},
			},
		},
		{
			name: "a task notification reads as its summary",
			entries: func(*testing.T, string) []any {
				return []any{
					prompted("u1", notification("b1", "toolu_9", "completed", `Background command "Run the suite" completed (exit code 0)`, "")+"\n<system-reminder>Claude Code's own note</system-reminder>"),
					attachment("q1", object{"type": "queued_command", "commandMode": "task-notification", "prompt": notification("m1", "", "", `Monitor "CI" reported`, "build green")}),
				}
			},
			want: []Message{
				prompt(0, "u1", FromNotice, `Background command "Run the suite" completed (exit code 0)`),
				{ID: "q1", Seq: 1, Kind: MessagePrompt, At: at, Source: FromNotice, IsQueued: true, Text: "Monitor \"CI\" reported\nbuild green"},
			},
		},
		{
			name: "a slash command reads as typed, with its output",
			entries: func(*testing.T, string) []any {
				caveat := prompted("m1", "<local-command-caveat>Caveat: the messages below were generated by the user while running local commands.</local-command-caveat>")
				caveat["isMeta"] = true
				skill := prompted("m2", []object{textBlock("Review the current changes thoroughly.")})
				skill["isMeta"] = true
				return []any{
					caveat,
					prompted("u1", "<command-name>/model</command-name>\n            <command-message>model</command-message>\n            <command-args>opus</command-args>"),
					prompted("u2", "<local-command-stdout>Set model to \x1b[1mopus\x1b[22m</local-command-stdout>"),
					prompted("u3", "<command-message>review is running…</command-message>\n<command-name>/review</command-name>"),
					skill,
				}
			},
			want: []Message{
				{ID: "u1", Seq: 1, Kind: MessagePrompt, At: at, Source: FromSlash, Text: "/model opus", Output: "Set model to opus"},
				prompt(3, "u3", FromSlash, "/review"),
			},
		},
		{
			name: "a ! command reads as typed, with what it printed",
			entries: func(*testing.T, string) []any {
				return []any{
					prompted("u1", "<bash-input>git status</bash-input>"),
					prompted("u2", "<bash-stdout>On branch main</bash-stdout><bash-stderr>warning: stale ref</bash-stderr>"),
				}
			},
			want: []Message{
				{ID: "u1", Seq: 0, Kind: MessagePrompt, At: at, Source: FromShell, Text: "git status", Output: "On branch main", Errors: "warning: stale ref"},
			},
		},
		{
			name: "a slash command Claude Code ran itself, recorded only as system entries, reads as typed",
			entries: func(*testing.T, string) []any {
				return []any{
					prompted("u1", "Read the readme"),
					system("s1", "local_command", object{"content": "/remote-control", "level": "info"}),
					system("s2", "local_command", object{"content": "<local-command-stdout>/remote-control isn't available in this environment.</local-command-stdout>", "level": "info"}),
					system("s3", "local_command", object{"content": "<command-name>/model</command-name>\n<command-args>opus</command-args>", "level": "info"}),
				}
			},
			want: []Message{
				prompt(0, "u1", FromOverlord, "Read the readme"),
				{ID: "s1", Seq: 1, Kind: MessagePrompt, At: at, Source: FromSlash, Text: "/remote-control", Output: "/remote-control isn't available in this environment."},
				prompt(3, "s3", FromSlash, "/model opus"),
			},
		},
		{
			name: "an older command's system output folds into its command",
			entries: func(*testing.T, string) []any {
				return []any{
					prompted("u1", "<command-name>/clear</command-name>\n            <command-message>clear</command-message>\n            <command-args></command-args>"),
					system("s1", "local_command", object{"content": "<local-command-stdout>Cleared.</local-command-stdout>", "level": "info"}),
				}
			},
			want: []Message{
				{ID: "u1", Seq: 0, Kind: MessagePrompt, At: at, Source: FromSlash, Text: "/clear", Output: "Cleared."},
			},
		},
		{
			name: "remote control's status is its command, recorded with nothing else",
			entries: func(*testing.T, string) []any {
				return []any{
					prompted("u1", "Read the readme"),
					system("s1", "bridge_status", object{"content": "/remote-control is active · Continue here, on your phone, or at https://claude.ai/code/session_1", "url": "https://claude.ai/code/session_1", "isMeta": false}),
					system("s2", "local_command", object{"content": "/remote-control"}),
					system("s3", "bridge_status", object{"content": "/remote-control is active · Continue here, on your phone, or at https://claude.ai/code/session_2", "url": "https://claude.ai/code/session_2", "isMeta": false}),
					system("s4", "bridge_status", object{"content": "Remote control is reconnecting"}),
				}
			},
			want: []Message{
				prompt(0, "u1", FromOverlord, "Read the readme"),
				{ID: "s1", Seq: 1, Kind: MessagePrompt, At: at, Source: FromSlash, Text: "/remote-control", Output: "/remote-control is active · Continue here, on your phone, or at https://claude.ai/code/session_1"},
				{ID: "s2", Seq: 2, Kind: MessagePrompt, At: at, Source: FromSlash, Text: "/remote-control", Output: "/remote-control is active · Continue here, on your phone, or at https://claude.ai/code/session_2"},
			},
		},
		{
			name: "a compaction carries its summary",
			entries: func(*testing.T, string) []any {
				summary := prompted("u2", "This session is being continued from a previous conversation.")
				summary["isCompactSummary"] = true
				summary["isVisibleInTranscriptOnly"] = true
				return []any{
					system("s1", "compact_boundary", object{"content": "Conversation compacted", "compactMetadata": object{"trigger": "manual", "preTokens": 180000}}),
					attachment("x1", object{"type": "date"}),
					summary,
				}
			},
			want: []Message{
				{ID: "s1", Seq: 0, Kind: MessageCompaction, At: at, Trigger: "manual", Text: "This session is being continued from a previous conversation."},
			},
		},
		{
			name: "an interrupt reads as his stop, and the call it cut short as interrupted",
			entries: func(*testing.T, string) []any {
				return []any{
					answer("a1", "msg_1", useBlock("toolu_1", "Bash", object{"command": "go test ./...\ngo vet ./...", "description": "Run the tests"})),
					answered("u1", "toolu_1", "The user doesn't want to proceed with this tool use.", object{"interrupted": true, "stdout": "", "stderr": ""}, object{"toolDenialKind": "interrupted"}),
					prompted("u2", []object{textBlock("[Request interrupted by user for tool use]")}),
					prompted("u3", []object{textBlock("[Request interrupted by user]")}),
				}
			},
			want: []Message{
				reply(0, "msg_1", callBlock("toolu_1", "Bash", "go test ./...", CallInterrupted, "The user doesn't want to proceed with this tool use.")),
				{ID: "u2", Seq: 2, Kind: MessageInterrupt, At: at, Text: "Request interrupted by user for tool use"},
				{ID: "u3", Seq: 3, Kind: MessageInterrupt, At: at, Text: "Request interrupted by user"},
			},
		},
		{
			name: "a spilled result reads from the file Claude Code saved it to",
			entries: func(t *testing.T, home string) []any {
				saved := filepath.Join(session(home), "tool-results", "toolu_1.txt")
				writeFile(t, saved, "ok  internal/monitor\nok  internal/fleettree\n", at)
				return []any{
					answer("a1", "msg_1", useBlock("toolu_1", "Bash", object{"command": "go test ./..."})),
					answered("u1", "toolu_1", "<persisted-output>\nOutput too large (40.0KB). Full output saved to: "+saved+"\n\nPreview (first 2KB):\nok  internal/monitor\n</persisted-output>", object{"stdout": "", "persistedOutputPath": saved, "persistedOutputSize": 40960}, nil),
				}
			},
			want: []Message{
				reply(0, "msg_1", Block{Kind: BlockCall, CallID: "toolu_1", Tool: "Bash", Subject: "go test ./...", State: CallDone, Result: "ok  internal/monitor\nok  internal/fleettree", Spilled: 44}),
			},
		},
		{
			name: "a spill saved outside its conversation's folder keeps its preview",
			entries: func(t *testing.T, home string) []any {
				elsewhere := filepath.Join(home, "elsewhere", "toolu_1.txt")
				writeFile(t, elsewhere, "not this conversation's", at)
				return []any{
					answer("a1", "msg_1", useBlock("toolu_1", "mcp__docs__fetch", object{"url": "https://example.com/spec"})),
					answered("u1", "toolu_1", "<persisted-output>\nOutput too large (40.0KB). Full output saved to: "+elsewhere+"\n\nPreview (first 2KB):\n# Spec\n</persisted-output>", nil, nil),
				}
			},
			want: []Message{
				reply(0, "msg_1", Block{Kind: BlockCall, CallID: "toolu_1", Tool: "mcp__docs__fetch", Subject: "https://example.com/spec", State: CallDone, Result: "# Spec", Spilled: 40960}),
			},
		},
		{
			name: "calls end done, failed, denied or still running",
			entries: func(*testing.T, string) []any {
				return []any{
					answer("a1", "msg_1", useBlock("toolu_1", "Bash", object{"command": "go build ./..."})),
					answer("a2", "msg_1", useBlock("toolu_2", "Bash", object{"command": "rm -rf build"})),
					answer("a3", "msg_1", useBlock("toolu_3", "Grep", object{"pattern": "readOn"})),
					answer("a4", "msg_1", useBlock("toolu_4", "Bash", object{"command": "go test ./..."})),
					failedResult("u1", "toolu_1", "<tool_use_error>exit status 1</tool_use_error>"),
					answered("u2", "toolu_2", "Permission for this action was denied.", "Error: Permission denied", object{"permissionDecision": object{"decision": "reject", "source": "config"}, "toolDenialKind": "automode-blocked"}),
					answered("u3", "toolu_3", "Found 2 files", object{"numFiles": 2}, nil),
				}
			},
			want: []Message{
				reply(0, "msg_1",
					callBlock("toolu_1", "Bash", "go build ./...", CallFailed, "exit status 1"),
					callBlock("toolu_2", "Bash", "rm -rf build", CallDenied, "Permission for this action was denied."),
					callBlock("toolu_3", "Grep", "readOn", CallDone, "Found 2 files"),
					callBlock("toolu_4", "Bash", "go test ./...", CallRunning, "")),
			},
		},
		{
			name: "an edit carries its counts, and a sub-agent call its node",
			entries: func(*testing.T, string) []any {
				return []any{
					answer("a1", "msg_1", useBlock("toolu_1", "Edit", object{"file_path": workDir + `\main.go`, "old_string": "a", "new_string": "b"})),
					answer("a2", "msg_1", useBlock("toolu_2", "Write", object{"file_path": workDir + `\new.go`, "content": "package x\n\nfunc X() {}\n"})),
					answer("a3", "msg_1", useBlock("toolu_3", "Agent", object{"description": "Map the monitor", "subagent_type": "Explore", "prompt": "map it"})),
					answered("u1", "toolu_1", "The file has been updated.", object{"filePath": workDir + `\main.go`, "structuredPatch": []object{{"oldStart": 1, "oldLines": 3, "newStart": 1, "newLines": 4, "lines": []string{" package main", "-var a = 1", "+var b = 2", "+var c = 3"}}}}, nil),
					answered("u2", "toolu_2", "File created successfully.", object{"type": "create", "filePath": workDir + `\new.go`, "content": "package x\n\nfunc X() {}\n", "structuredPatch": []object{}}, nil),
					answered("u3", "toolu_3", "Async agent launched successfully.", object{"status": "async_launched", "agentId": "a1"}, nil),
				}
			},
			want: []Message{
				reply(0, "msg_1",
					Block{Kind: BlockCall, CallID: "toolu_1", Tool: "Edit", Subject: "main.go", State: CallDone, Result: "The file has been updated.", Edit: Edit{Added: 2, Removed: 1, Diff: []string{" package main", "-var a = 1", "+var b = 2", "+var c = 3"}}},
					Block{Kind: BlockCall, CallID: "toolu_2", Tool: "Write", Subject: "new.go", State: CallDone, Result: "File created successfully.", Edit: Edit{Added: 3, Diff: []string{"+package x", "+", "+func X() {}"}}},
					Block{Kind: BlockCall, CallID: "toolu_3", Tool: "Agent", Subject: "Map the monitor", State: CallDone, Result: "Async agent launched successfully.", Agent: AgentLink{Node: "subagent:toolu_3", Type: "Explore"}}),
			},
		},
		{
			name: "pictures in a prompt and in a result",
			entries: func(*testing.T, string) []any {
				return []any{
					prompted("u1", []object{imageBlock([]byte("a screenshot")), textBlock("Why is this red?")}),
					answer("a1", "msg_1", useBlock("toolu_1", "Read", object{"file_path": workDir + `\shot.png`})),
					answered("u2", "toolu_1", []object{imageBlock([]byte("the file"))}, object{"type": "image"}, nil),
				}
			},
			want: []Message{
				{ID: "u1", Seq: 0, Kind: MessagePrompt, At: at, Source: FromOverlord, Text: "Why is this red?", Pictures: []Picture{{ID: "0.0", MediaType: "image/png", Bytes: 12}}},
				reply(1, "msg_1", Block{Kind: BlockCall, CallID: "toolu_1", Tool: "Read", Subject: "shot.png", State: CallDone, Pictures: []Picture{{ID: "2.0.0", MediaType: "image/png", Bytes: 8}}}),
			},
		},
		{
			name: "what Claude Code records for its model alone shows nothing",
			entries: func(*testing.T, string) []any {
				sidechain := answer("a9", "msg_9", textBlock("a sub-agent's own words"))
				sidechain["isSidechain"] = true
				return []any{
					`{"type":"api-request-blob","sessionId":"session-1","hash":"h","message":{"content":[{"type":"text","text":"a copy of a prompt"}]}}`,
					attachment("x1", object{"type": "hook_success", "hookName": "PostToolUse"}),
					system("s1", "turn_duration", object{"durationMs": 1200}),
					sidechain,
					answer("a1", "msg_1", thinkingBlock("")),
					"not an entry",
				}
			},
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			home := t.TempDir()
			path := conversation(t, home, tc.entries(t, home)...)
			reader := Reader{Home: home}

			// Act
			page, _, err := reader.MessagePage(path, -1)

			// Assert
			want := tc.want
			if want == nil {
				want = []Message{}
			}
			if got := numbered(t, path, page.Messages); err != nil || !reflect.DeepEqual(got, want) || page.More {
				t.Errorf("MessagePage = %+v, more %t, %v\nwant %+v", got, page.More, err, want)
			}
		})
	}
}

// Claude Code writes an entry whole and then its newline, so a read that
// lands between them leaves the entry for the next read, which reads on from
// where the last stopped.
func TestMessagesLeaveAHalfWrittenLastLineForTheNextRead(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := conversation(t, home, prompted("u1", "first"))
	whole := `{"type":"user","uuid":"u2","timestamp":"` + at.Format(time.RFC3339Nano) + `","message":{"role":"user","content":"second"}}`
	appendText(t, path, whole[:40])
	reader := Reader{Home: home}
	page, cursor, err := reader.MessagePage(path, -1)
	if err != nil || !reflect.DeepEqual(numbered(t, path, page.Messages), []Message{prompt(0, "u1", FromOverlord, "first")}) {
		t.Fatalf("MessagePage with a half-written line = %+v, %v; want the first prompt alone", page.Messages, err)
	}

	// Act
	appendText(t, path, whole[40:]+"\n")
	messages, _, _, isAnew, err := reader.MessagesSince(path, cursor)

	// Assert
	if err != nil || isAnew || !reflect.DeepEqual(numbered(t, path, messages), []Message{prompt(1, "u2", FromOverlord, "second")}) {
		t.Errorf("MessagesSince = %+v, anew %t, %v; want the second prompt once written whole", messages, isAnew, err)
	}
}

func appendText(t *testing.T, path, text string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

// A view follows a conversation by what changed since its last read: a new
// message, and an earlier reply whose call's result came back, each once.
func TestMessagesSinceAreTheMessagesThatBeganOrChanged(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := conversation(t, home,
		prompted("u1", "run the suite"),
		answer("a1", "msg_1", useBlock("toolu_1", "Bash", object{"command": "go test ./..."})),
		answer("a2", "msg_2", textBlock("Running.")))
	reader := Reader{Home: home}
	_, cursor, err := reader.MessagePage(path, -1)
	if err != nil {
		t.Fatal(err)
	}
	appendLines(t, path, answered("u2", "toolu_1", "ok", object{"stdout": "ok"}, nil), prompted("u3", "thanks"))

	// Act
	messages, _, next, isAnew, err := reader.MessagesSince(path, cursor)
	again, _, _, _, againErr := reader.MessagesSince(path, next)

	// Assert
	want := []Message{
		reply(1, "msg_1", callBlock("toolu_1", "Bash", "go test ./...", CallDone, "ok")),
		prompt(4, "u3", FromOverlord, "thanks"),
	}
	if err != nil || isAnew || !reflect.DeepEqual(numbered(t, path, messages), want) {
		t.Errorf("MessagesSince = %+v, anew %t, %v; want %+v", messages, isAnew, err, want)
	}
	if againErr != nil || len(again) != 0 {
		t.Errorf("MessagesSince with nothing new = %+v, %v; want none", again, againErr)
	}
}

// A conversation that shrank was replaced, so a view's messages of it no
// longer stand and it takes a page again.
func TestMessagesOfAReplacedConversationAreReadAnew(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := conversation(t, home, prompted("u1", "a long first conversation"), prompted("u2", "with more"))
	reader := Reader{Home: home}
	_, cursor, err := reader.MessagePage(path, -1)
	if err != nil {
		t.Fatal(err)
	}
	conversation(t, home, prompted("v1", "new"))

	// Act
	messages, _, _, isAnew, err := reader.MessagesSince(path, cursor)
	page, _, pageErr := reader.MessagePage(path, -1)

	// Assert
	if err != nil || !isAnew || messages != nil {
		t.Errorf("MessagesSince after a replace = %+v, anew %t, %v; want it read anew", messages, isAnew, err)
	}
	if pageErr != nil || !reflect.DeepEqual(numbered(t, path, page.Messages), []Message{prompt(0, "v1", FromOverlord, "new")}) {
		t.Errorf("MessagePage after a replace = %+v, %v; want the new conversation", page.Messages, pageErr)
	}
}

// Older messages come a page at a time, each page the newest before the
// first the view holds.
func TestMessagePagesReachBackAPageAtATime(t *testing.T) {
	// Arrange
	home := t.TempDir()
	var entries []any
	for i := range 120 {
		entries = append(entries, prompted("u"+strconv.Itoa(i), "prompt "+strconv.Itoa(i)))
	}
	path := conversation(t, home, entries...)
	offsets := entryOffsets(t, path)
	reader := Reader{Home: home}

	// before is the entry a page is before, or a seq past every entry.
	for _, tc := range []struct {
		before, first, last int
		isMore              bool
	}{
		{-1, 70, 119, true},
		{70, 20, 69, true},
		{20, 0, 19, false},
		{1 << 30, 70, 119, true},
		{0, 0, -1, false},
	} {
		t.Run("before "+strconv.Itoa(tc.before), func(t *testing.T) {
			before := tc.before
			if before >= 0 && before < len(offsets) {
				before = offsets[before]
			}

			// Act
			page, _, err := reader.MessagePage(path, before)

			// Assert
			if err != nil || len(page.Messages) != tc.last-tc.first+1 || page.More != tc.isMore {
				t.Fatalf("MessagePage(%d) = %d messages, more %t, %v; want %d to %d, more %t", tc.before, len(page.Messages), page.More, err, tc.first, tc.last, tc.isMore)
			}
			if len(page.Messages) > 0 && (page.Messages[0].Seq != offsets[tc.first] || page.Messages[len(page.Messages)-1].Text != "prompt "+strconv.Itoa(tc.last)) {
				t.Errorf("MessagePage(%d) runs %+v to %+v; want %d to %d", tc.before, page.Messages[0], page.Messages[len(page.Messages)-1], tc.first, tc.last)
			}
		})
	}
}

// A picture is read back from its entry by the id a read gave it, and only
// a picture a read found.
func TestMessagePictureIsReadBackByItsID(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := conversation(t, home,
		prompted("u1", []object{imageBlock([]byte("a screenshot")), textBlock("Why is this red?")}),
		answer("a1", "msg_1", useBlock("toolu_1", "Read", object{"file_path": workDir + `\shot.png`})),
		answered("u2", "toolu_1", []object{imageBlock([]byte("the file"))}, object{"type": "image"}, nil))
	reader := Reader{Home: home}
	page, _, err := reader.MessagePage(path, -1)
	if err != nil {
		t.Fatal(err)
	}
	result := strconv.Itoa(entryOffsets(t, path)[2])

	for _, tc := range []struct {
		id   string
		want string
	}{
		{"0.0", "a screenshot"},
		{result + ".0.0", "the file"},
		{"0.1", ""},
		{"1", ""},
		{"../0.0", ""},
	} {
		t.Run(tc.id, func(t *testing.T) {
			// Act
			mediaType, data, err := reader.MessagePicture(path, page.Conversation, tc.id)

			// Assert
			if tc.want == "" {
				if !errors.Is(err, ErrNoRecord) {
					t.Errorf("MessagePicture(%q) = %q, %v; want ErrNoRecord", tc.id, data, err)
				}
				return
			}
			if err != nil || mediaType != "image/png" || string(data) != tc.want {
				t.Errorf("MessagePicture(%q) = %s %q, %v; want image/png %q", tc.id, mediaType, data, err, tc.want)
			}
		})
	}
}

// A conversation longer than one read's chunk is read across the chunks'
// edges, each entry whole and at its own offset, so a picture past them is
// read back from its own entry.
func TestMessagesReadAcrossTheChunksOfALongConversation(t *testing.T) {
	// Arrange
	home := t.TempDir()
	long := strings.Repeat("x", claudeReadChunk/3)
	var entries []any
	for i := range 4 {
		entries = append(entries, prompted("u"+strconv.Itoa(i), long))
	}
	entries = append(entries, prompted("u4", []object{imageBlock([]byte("past the chunk")), textBlock("and this")}))
	path := conversation(t, home, entries...)
	reader := Reader{Home: home}

	// Act
	page, _, err := reader.MessagePage(path, -1)
	mediaType, data, pictureErr := reader.MessagePicture(path, page.Conversation, strconv.Itoa(entryOffsets(t, path)[4])+".0")

	// Assert
	if err != nil || len(page.Messages) != 5 || page.Messages[4].ID != "u4" || page.Messages[4].Text != "and this" || len([]rune(page.Messages[0].Text)) != messageTextLimit {
		t.Fatalf("MessagePage = %d messages, %v; want five, the last with its picture", len(page.Messages), err)
	}
	if pictureErr != nil || mediaType != "image/png" || string(data) != "past the chunk" {
		t.Errorf("MessagePicture = %s %q, %v; want the picture past the chunk", mediaType, data, pictureErr)
	}
}

// A conversation no view asked for in five minutes is let go, by the next
// ask for any conversation or by the tree's own pass, and a view that asks
// again reads it anew.
func TestAConversationNoViewAsksForIsLetGo(t *testing.T) {
	for _, tc := range []struct {
		name    string
		letsGo  func(reader *Reader, other string)
		isAnew  bool
		elapsed time.Duration
	}{
		{"asked for within five minutes", func(reader *Reader, other string) { reader.Forget(nil) }, false, 4 * time.Minute},
		{"by the tree's own pass", func(reader *Reader, other string) { reader.Forget(nil) }, true, 6 * time.Minute},
		{"by an ask for another", func(reader *Reader, other string) { _, _, _ = reader.MessagePage(other, -1) }, true, 6 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			home := t.TempDir()
			path := conversation(t, home, prompted("u1", []object{imageBlock([]byte("a screenshot")), textBlock("first")}))
			other := filepath.Join(home, "other.jsonl")
			writeLines(t, other, at, prompted("v1", "elsewhere"))
			now := at
			reader := Reader{Home: home, Now: func() time.Time { return now }}
			_, cursor, err := reader.MessagePage(path, -1)
			if err != nil {
				t.Fatal(err)
			}

			// Act
			now = now.Add(tc.elapsed)
			tc.letsGo(&reader, other)
			_, _, pictureErr := reader.MessagePicture(path, cursor.Conversation(), "0.0")
			_, _, _, isAnew, err := reader.MessagesSince(path, cursor)

			// Assert
			if errors.Is(pictureErr, ErrNoRecord) != tc.isAnew || err != nil || isAnew != tc.isAnew {
				t.Errorf("after %s: picture %v, MessagesSince anew %t, %v; want let go %t", tc.elapsed, pictureErr, isAnew, err, tc.isAnew)
			}
		})
	}
}

// The wake line the supervisor types into a Codex or pi CEO's terminal is the
// fleet speaking, never the Overlord: it reads as the session's own notice,
// with its words kept, and everything else he types stays his own message,
// a line that begins with "ceo" included (the Overlord, 2026-10-26).
func TestTheFleetsTypedWakeIsANoticeNotHisMessage(t *testing.T) {
	// Arrange
	wake := WakePrefix + "2 queued wakes (ci cg-a, notify cg-b); run goblins drain, handle what it shows and ack it with the WAKE_ACK_REQUIRED command it prints"
	home := t.TempDir()
	path := conversation(t, home,
		prompted("u1", wake),
		prompted("u2", "ceo, take a look at the failing test"),
		prompted("u3", "ceo watcher wake-ups are noisy, quiet them down"),
	)
	reader := Reader{Home: home}

	// Act
	page, _, err := reader.MessagePage(path, -1)

	// Assert
	want := []Message{
		prompt(0, "u1", FromNotice, wake),
		prompt(1, "u2", FromOverlord, "ceo, take a look at the failing test"),
		prompt(2, "u3", FromOverlord, "ceo watcher wake-ups are noisy, quiet them down"),
	}
	if got := numbered(t, path, page.Messages); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("MessagePage = %+v, %v\nwant %+v", got, err, want)
	}
}

// A pi session reads the same fleet wake line as the session's notice, since
// the supervisor types it into a pi CEO's terminal too.
func TestAPiSessionReadsTheFleetsWakeAsANotice(t *testing.T) {
	// Arrange
	wake := WakePrefix + "1 queued wake (notify cg-a); run goblins drain, handle what it shows and ack it with the WAKE_ACK_REQUIRED command it prints"
	home := t.TempDir()
	path := piSession(t, home,
		piHeader(),
		piMessageEntry("u1", "", "user", object{"content": wake}),
		piMessageEntry("u2", "u1", "user", object{"content": "ceo, what changed?"}),
	)
	reader := Reader{Home: home}

	// Act
	page := readPi(t, &reader, path)

	// Assert
	want := []Message{
		prompt(0, "u1", FromNotice, wake),
		prompt(1, "u2", FromOverlord, "ceo, what changed?"),
	}
	if !reflect.DeepEqual(page.Messages, want) {
		t.Fatalf("messages = %+v, want %+v", page.Messages, want)
	}
}

func TestFleetProducerMessagesAreNotOverlordPrompts(t *testing.T) {
	for _, test := range []struct {
		text, source string
	}{
		{WakePrefix + "1 queued wake (notify task-1); run goblins drain", FromNotice},
		{"== SESSION LOCK ==\nSESSION LOCK: held by pid 123 on test\n== WAKE QUEUE ==\nWAKE QUEUE: empty", FromNotice},
		{"WAKE QUEUE: 1 pending\n  1 notify task-1 done\nWAKE_ACK_REQUIRED: goblins drain --ack-through 1", FromNotice},
		{"AFK MODE IS ON: the Supreme Overlord turned it on 2026-10-02 02:10 UTC from the board, and he is away until he turns it off.\nIt is complete autopilot: no Command Center prompt opens for him while it is on, and nothing is asked of him or held for him.\nWAKE QUEUE: empty", FromNotice},
		{routing.SteerPrefix + "Your run npm test ended with exit 0 after 1m35s; read its output and carry on from it.", FromNotice},
		{routing.SteerPrefix + "Your helper task-1 is done: its work is committed on its branch, ready. Merge it into your branch with: goblins helper merge parent", FromNotice},
		{routing.SteerPrefix + "Your helper task-1 asks: Which path? Answer it with: goblins send task-1 \"<answer>\"", FromNotice},
		{routing.SteerPrefix + "Your helper task-1 failed: test failure. Tell it what to do with: goblins send task-1 \"<what>\", or stop it with: goblins kill task-1 --reason \"<why>\"", FromNotice},
		{"goblins drain please", FromOverlord},
		{"ceo watcher wake-ups need fixing", FromOverlord},
		{"Your run was slow, please check it", FromOverlord},
		{"WAKE QUEUE: please show it", FromOverlord},
		{routing.SteerPrefix + "Your run needs a regression test", FromCEO},
	} {
		for _, harness := range []string{"claude", "pi", "codex"} {
			t.Run(harness+"/"+test.text, func(t *testing.T) {
				home := t.TempDir()
				var path string
				if harness == "pi" {
					path = piSession(t, home, piHeader(), piMessageEntry("u1", "", "user", object{"content": test.text}))
				} else if harness == "codex" {
					path = codexRollout(t, home, "notices", codexMeta("notices", workDir), codexUserSaid(test.text))
				} else {
					path = conversation(t, home, prompted("u1", test.text))
				}
				reader := Reader{Home: home}
				page, _, err := reader.MessagePage(path, -1)
				if err != nil || len(page.Messages) != 1 || page.Messages[0].Source != test.source {
					t.Fatalf("messages = %+v, %v; want source %s", page.Messages, err, test.source)
				}
			})
		}
	}
}

// A todo list a Claude Code call wrote reads as the call's plan: each
// step's words on one line and its state, and a state the board does not
// draw read as pending.
func TestATodoListACallWroteIsItsPlan(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := conversation(t, home,
		prompted("u1", "Fix the swipe"),
		answer("a1", "msg_1", useBlock("toolu_1", "TodoWrite", object{"todos": []object{
			{"content": "Read the touch handlers", "status": "completed", "activeForm": "Reading the touch handlers"},
			{"content": "Hand the swipe to the history\nand keep Paste", "status": "in_progress", "activeForm": "Handing the swipe over"},
			{"content": "Run the phone spec", "status": "blocked", "activeForm": "Running the phone spec"},
		}})),
	)
	reader := Reader{Home: home}

	// Act
	page, _, err := reader.MessagePage(path, -1)

	// Assert
	want := []PlanStep{{"Read the touch handlers", PlanCompleted}, {"Hand the swipe to the history", PlanInProgress}, {"Run the phone spec", PlanPending}}
	if err != nil || len(page.Messages) != 2 || !reflect.DeepEqual(page.Messages[1].Blocks[0].Plan, want) {
		t.Fatalf("MessagePage = %+v, %v; want the call's plan %+v", page.Messages, err, want)
	}
}

// A todo list keeps its plan only once Claude Code wrote it: a TodoWrite
// that failed, was denied or was interrupted is its row with its outcome,
// as Claude Code stored no list.
func TestATodoListKeepsItsPlanOnlyOnceWritten(t *testing.T) {
	for _, test := range []struct {
		name    string
		result  object
		state   string
		hasPlan bool
	}{
		{"written", answered("u1", "toolu_1", "Todos have been modified successfully.", object{"oldTodos": []object{}, "newTodos": []object{{"content": "Fix the swipe", "status": "in_progress", "activeForm": "Fixing the swipe"}}}, nil), CallDone, true},
		{"failed", failedResult("u1", "toolu_1", "<tool_use_error>InputValidationError: TodoWrite failed due to the following issue:\nThe required parameter `todos[0].activeForm` is missing</tool_use_error>"), CallFailed, false},
		{"denied", answered("u1", "toolu_1", "Permission for this action was denied.", "Error: Permission denied", object{"permissionDecision": object{"decision": "reject", "source": "config"}, "toolDenialKind": "automode-blocked"}), CallDenied, false},
		{"interrupted", answered("u1", "toolu_1", "The user doesn't want to proceed with this tool use.", object{"interrupted": true}, object{"toolDenialKind": "interrupted"}), CallInterrupted, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			home := t.TempDir()
			path := conversation(t, home,
				answer("a1", "msg_1", useBlock("toolu_1", "TodoWrite", object{"todos": []object{{"content": "Fix the swipe", "status": "in_progress"}}})),
				test.result,
			)
			reader := Reader{Home: home}

			// Act
			page, _, err := reader.MessagePage(path, -1)

			// Assert
			if err != nil || len(page.Messages) != 1 || len(page.Messages[0].Blocks) != 1 {
				t.Fatalf("MessagePage = %+v, %v; want one call", page.Messages, err)
			}
			call := page.Messages[0].Blocks[0]
			if call.State != test.state || (len(call.Plan) != 0) != test.hasPlan {
				t.Fatalf("call = %+v; want state %q and a plan %v", call, test.state, test.hasPlan)
			}
		})
	}
}

// Each harness's plan is read from its own tool's shape alone: Claude Code's
// TodoWrite from its todos object, and Codex's update_plan from its plan in
// the arguments Codex records as a JSON string.
func TestAPlanIsReadOnlyFromItsOwnToolsShape(t *testing.T) {
	for _, test := range []struct{ name, tool, input string }{
		{"a TodoWrite holding a plan", "TodoWrite", `{"plan":[{"step":"Fix the swipe","status":"pending"}]}`},
		{"a TodoWrite with its todos as a string", "TodoWrite", `"{\"todos\":[{\"content\":\"Fix the swipe\",\"status\":\"pending\"}]}"`},
		{"an update_plan holding todos", "update_plan", `"{\"todos\":[{\"content\":\"Fix the swipe\",\"status\":\"pending\"}]}"`},
		{"an update_plan with its arguments as an object", "update_plan", `{"plan":[{"step":"Fix the swipe","status":"pending"}]}`},
		{"another tool holding todos", "Write", `{"todos":[{"content":"Fix the swipe","status":"pending"}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			plan := callPlan(test.tool, []byte(test.input))

			// Assert
			if plan != nil {
				t.Fatalf("callPlan(%q, %s) = %+v; want no plan", test.tool, test.input, plan)
			}
		})
	}
}

// taskCreated is the result of a TaskCreate that made task id, as Claude
// Code writes it, and taskUpdated that of a TaskUpdate of it.
func taskCreated(uuid, call, id, subject string) object {
	return answered(uuid, call, "Task #"+id+" created successfully: "+subject, object{"task": object{"id": id, "subject": subject}}, nil)
}

func taskUpdated(uuid, call, id string) object {
	return answered(uuid, call, "Updated task #"+id+" status", object{"success": true, "taskId": id, "updatedFields": []string{"status"}}, nil)
}

// plans are the plans of the blocks of a page's messages, by call id.
func plans(page MessagePage) map[string][]PlanStep {
	shown := map[string][]PlanStep{}
	for _, message := range page.Messages {
		for _, block := range message.Blocks {
			if block.Kind == BlockCall {
				shown[block.CallID] = block.Plan
			}
		}
	}
	return shown
}

// Newer Claude Code builds keep their checklist with TaskCreate, which adds
// one task, and TaskUpdate, which changes one by its ID (#1669). The list
// they build over several calls, across answers, reads as one plan at the
// latest task call that ran, a deleted task off it and a renamed one by its
// new words; every earlier task call, and one that failed, is its row, named
// by the subject of the task it made or changed.
func TestTaskCallsBuildOnePlanAtTheLatestThatRan(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := conversation(t, home,
		prompted("u1", "Fix the swipe"),
		answer("a1", "msg_1", useBlock("toolu_1", "TaskCreate", object{"subject": "Read the touch handlers", "description": "Find where a swipe starts", "activeForm": "Reading the touch handlers"})),
		taskCreated("r1", "toolu_1", "1", "Read the touch handlers"),
		answer("a2", "msg_1", useBlock("toolu_2", "TaskCreate", object{"subject": "Fix the swipe", "description": "Hand the swipe to the history", "activeForm": "Fixing the swipe"})),
		taskCreated("r2", "toolu_2", "2", "Fix the swipe"),
		answer("a3", "msg_1", useBlock("toolu_3", "TaskCreate", object{"subject": "Run the phone spec", "description": "The swipe spec on a phone", "activeForm": "Running the phone spec"})),
		taskCreated("r3", "toolu_3", "3", "Run the phone spec"),
		answer("a4", "msg_1", useBlock("toolu_4", "TaskCreate", object{"subject": "A note to drop", "description": "Dropped later", "activeForm": "Noting"})),
		taskCreated("r4", "toolu_4", "4", "A note to drop"),
		answer("a5", "msg_2", useBlock("toolu_5", "TaskUpdate", object{"taskId": "1", "status": "completed"})),
		taskUpdated("r5", "toolu_5", "1"),
		answer("a6", "msg_2", useBlock("toolu_6", "TaskUpdate", object{"taskId": "2", "status": "in_progress"})),
		taskUpdated("r6", "toolu_6", "2"),
		answer("a7", "msg_2", useBlock("toolu_7", "TaskUpdate", object{"taskId": "4", "status": "deleted"})),
		taskUpdated("r7", "toolu_7", "4"),
		answer("a8", "msg_2", useBlock("toolu_8", "TaskUpdate", object{"taskId": "3", "subject": "Run the phone and desktop specs"})),
		taskUpdated("r8", "toolu_8", "3"),
		answer("a9", "msg_2", useBlock("toolu_9", "TaskUpdate", object{"taskId": "2", "status": "completed"})),
		failedResult("r9", "toolu_9", "<tool_use_error>InputValidationError: TaskUpdate failed due to the following issue:\nThe required parameter `taskId` is missing</tool_use_error>"),
	)
	reader := Reader{Home: home}

	// Act
	page, _, err := reader.MessagePage(path, -1)

	// Assert
	if err != nil || len(page.Messages) != 3 {
		t.Fatalf("MessagePage = %+v, %v; want the prompt and two answers", page.Messages, err)
	}
	want := []PlanStep{{"Read the touch handlers", PlanCompleted}, {"Fix the swipe", PlanInProgress}, {"Run the phone and desktop specs", PlanPending}}
	shown := plans(page)
	for _, call := range []string{"toolu_1", "toolu_2", "toolu_3", "toolu_4", "toolu_5", "toolu_6", "toolu_7", "toolu_9"} {
		if shown[call] != nil {
			t.Errorf("call %s shows the plan %+v; want its row alone", call, shown[call])
		}
	}
	if !reflect.DeepEqual(shown["toolu_8"], want) {
		t.Errorf("the latest task call that ran shows %+v; want %+v", shown["toolu_8"], want)
	}
	subjects := map[string]string{}
	for _, message := range page.Messages[1:] {
		for _, block := range message.Blocks {
			subjects[block.CallID] = block.Subject
		}
	}
	wantSubjects := map[string]string{
		"toolu_1": "Read the touch handlers", "toolu_2": "Fix the swipe", "toolu_3": "Run the phone spec", "toolu_4": "A note to drop",
		"toolu_5": "Read the touch handlers", "toolu_6": "Fix the swipe", "toolu_7": "A note to drop", "toolu_8": "Run the phone and desktop specs", "toolu_9": "Fix the swipe",
	}
	if !reflect.DeepEqual(subjects, wantSubjects) {
		t.Errorf("the task calls' rows read %v; want each its task's subject %v", subjects, wantSubjects)
	}
	if failed := page.Messages[2].Blocks[4]; failed.CallID != "toolu_9" || failed.State != CallFailed {
		t.Errorf("the failed TaskUpdate = %+v; want its failed row", failed)
	}
}

// A task call that did not run changes nothing: one that failed, was denied
// or was interrupted is its row with its outcome, and the plan stays at the
// task call before it, as Claude Code's list stayed as it was.
func TestATaskCallThatDidNotRunLeavesThePlanWhereItWas(t *testing.T) {
	for _, test := range []struct {
		name   string
		result object
		state  string
	}{
		{"failed", failedResult("r2", "toolu_2", "<tool_use_error>Task not found</tool_use_error>"), CallFailed},
		{"refused", answered("r2", "toolu_2", "Task #1 not found", object{"success": false, "taskId": "1", "updatedFields": []string{}, "error": "Task not found"}, nil), CallFailed},
		{"denied", answered("r2", "toolu_2", "Permission for this action was denied.", "Error: Permission denied", object{"permissionDecision": object{"decision": "reject", "source": "config"}, "toolDenialKind": "automode-blocked"}), CallDenied},
		{"interrupted", answered("r2", "toolu_2", "The user doesn't want to proceed with this tool use.", object{"interrupted": true}, object{"toolDenialKind": "interrupted"}), CallInterrupted},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			home := t.TempDir()
			path := conversation(t, home,
				answer("a1", "msg_1", useBlock("toolu_1", "TaskCreate", object{"subject": "Fix the swipe", "description": "Hand the swipe to the history"})),
				taskCreated("r1", "toolu_1", "1", "Fix the swipe"),
				answer("a2", "msg_1", useBlock("toolu_2", "TaskUpdate", object{"taskId": "1", "status": "completed"})),
				test.result,
			)
			reader := Reader{Home: home}

			// Act
			page, _, err := reader.MessagePage(path, -1)

			// Assert
			if err != nil || len(page.Messages) != 1 || len(page.Messages[0].Blocks) != 2 {
				t.Fatalf("MessagePage = %+v, %v; want one answer of two calls", page.Messages, err)
			}
			created, update := page.Messages[0].Blocks[0], page.Messages[0].Blocks[1]
			if want := []PlanStep{{"Fix the swipe", PlanPending}}; !reflect.DeepEqual(created.Plan, want) {
				t.Errorf("the TaskCreate shows %+v; want %+v", created.Plan, want)
			}
			if update.State != test.state || update.Plan != nil {
				t.Errorf("the TaskUpdate = %+v; want its %s row", update, test.state)
			}
		})
	}
}

// A list the conversation cannot know shows no plan, never a part of one: a
// TaskUpdate of a task no call here made shows the list was made elsewhere,
// and a TaskCreate whose result names no task leaves the list unreadable.
func TestATaskListTheConversationCannotKnowShowsNoPlan(t *testing.T) {
	for _, test := range []struct {
		name    string
		entries []any
	}{
		{"a task made elsewhere", []any{
			answer("a1", "msg_1", useBlock("toolu_1", "TaskCreate", object{"subject": "Fix the swipe"})),
			taskCreated("r1", "toolu_1", "2", "Fix the swipe"),
			answer("a2", "msg_1", useBlock("toolu_2", "TaskUpdate", object{"taskId": "1", "status": "completed"})),
			taskUpdated("r2", "toolu_2", "1"),
		}},
		{"a made task with no ID", []any{
			answer("a1", "msg_1", useBlock("toolu_1", "TaskCreate", object{"subject": "Fix the swipe"})),
			answered("r1", "toolu_1", "Task created", object{"success": true}, nil),
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			home := t.TempDir()
			path := conversation(t, home, test.entries...)
			reader := Reader{Home: home}

			// Act
			page, _, err := reader.MessagePage(path, -1)

			// Assert
			if err != nil || len(page.Messages) != 1 {
				t.Fatalf("MessagePage = %+v, %v; want one answer", page.Messages, err)
			}
			for call, plan := range plans(page) {
				if plan != nil {
					t.Errorf("call %s shows the plan %+v; want none", call, plan)
				}
			}
		})
	}
}

// A long conversation's tail may begin after a task was made, so the page a
// read of the tail serves shows no plan, and the read of the whole
// conversation puts the whole list at its latest task call, a change a view
// following the tail's page is sent.
func TestATaskListShowsOnlyOnceTheWholeConversationIsRead(t *testing.T) {
	// Arrange
	home := t.TempDir()
	entries := []any{
		prompted("u-first", "Plan the fix"),
		answer("a1", "msg_first", useBlock("toolu_1", "TaskCreate", object{"subject": "Read the touch handlers"})),
		taskCreated("r1", "toolu_1", "1", "Read the touch handlers"),
	}
	entries = append(entries, beyondTheTail()...)
	entries = append(entries,
		prompted("u-next", "Go on"),
		answer("a2", "msg_next", useBlock("toolu_2", "TaskCreate", object{"subject": "Fix the swipe"})),
		taskCreated("r2", "toolu_2", "2", "Fix the swipe"),
	)
	path := conversation(t, home, entries...)
	reader := Reader{Home: home}
	tail, cursor, err := reader.MessagePage(path, -1)
	if err != nil || len(tail.Messages) != 2 || !tail.More {
		t.Fatalf("MessagePage = %+v, %v; want the tail's prompt and answer, and more", tail.Messages, err)
	}

	// Act
	_, _, olderErr := reader.MessagePage(path, tail.Messages[0].Seq)
	changed, _, _, isAnew, sinceErr := reader.MessagesSince(path, cursor)
	whole, _, wholeErr := reader.MessagePage(path, -1)

	// Assert
	if shown := plans(tail); shown["toolu_2"] != nil {
		t.Errorf("the tail's page shows the plan %+v; want none until the whole conversation is read", shown["toolu_2"])
	}
	want := []PlanStep{{"Read the touch handlers", PlanPending}, {"Fix the swipe", PlanPending}}
	if shown := plans(whole); olderErr != nil || wholeErr != nil || !reflect.DeepEqual(shown["toolu_2"], want) {
		t.Errorf("the newest page once read whole shows %+v, %v, %v; want %+v", shown["toolu_2"], olderErr, wholeErr, want)
	}
	if sinceErr != nil || isAnew || len(changed) != 1 || changed[0].ID != "msg_next" || !reflect.DeepEqual(changed[0].Blocks[0].Plan, want) {
		t.Errorf("MessagesSince the tail's page = %+v, anew %t, %v; want the answer with the plan", changed, isAnew, sinceErr)
	}
}
