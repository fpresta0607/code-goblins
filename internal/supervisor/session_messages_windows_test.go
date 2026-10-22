package supervisor

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// appendEntries appends Claude Code entries to the conversation at path, as
// Claude Code appends them.
func appendEntries(t *testing.T, path string, entries ...string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(strings.Join(entries, "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
}

// The board reads a Claude Code session as messages, a task's goblin of the
// generation it names or the CFO in the terminal it names, only with the
// board's token: a page at a time, a picture a message holds, and a stream
// that follows the conversation as Claude Code writes it and ends once the
// task restarted.
func TestMessagesServeASessionsConversation(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	userHome := t.TempDir()
	meta := treeGoblin(t, h.State, userHome, "talker", true)
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	conversation := filepath.Join(userHome, ".claude", "projects", "C--work-talker", "talker-conversation.jsonl")
	picture := base64.StdEncoding.EncodeToString([]byte("a screenshot"))
	appendEntries(t, conversation,
		`{"type":"user","uuid":"u1","timestamp":"`+stamp+`","message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"`+picture+`"}},{"type":"text","text":"Why is the card red?"}]}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"`+stamp+`","message":{"id":"msg_1","model":"claude-opus-5-5","role":"assistant","content":[{"type":"text","text":"Its gate failed."}]}}`)
	registerNativeCFO(t, h.State, "claude", "cfo-conversation")
	writeFile(t, filepath.Join(userHome, ".claude", "projects", "C--work", "cfo-conversation.jsonl"),
		`{"type":"user","uuid":"c1","timestamp":"`+stamp+`","message":{"role":"user","content":"User answer to CFO question q-1. Question: Ship it? Answer: Yes"}}`+"\n")
	service := &Service{Store: store, Instance: "board-token", Options: Options{Tree: &fleettree.Reader{Home: userHome}}}
	handler := NewHTTP(service, "127.0.0.1:50000", nil)
	get := func(query string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		request := httptest.NewRequest("GET", "http://127.0.0.1:50000/api/messages?"+query, nil)
		request.RemoteAddr = "127.0.0.1:50001"
		handler.ServeHTTP(response, request)
		return response
	}
	task := "task=" + meta.ID + "&generation=" + meta.SpawnGen + "&token=board-token"

	for _, tc := range []struct {
		name, query string
		want        []string
		isMore      bool
	}{
		{"a task's newest page", task, []string{"@0", "u1", "msg_1"}, false},
		{"the page before a message", task + "&before=1", []string{"@0"}, false},
		{"the CFO's newest page", "cfo=cfo&token=board-token", []string{"c1"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			response := get(tc.query)

			// Assert
			var page fleettree.MessagePage
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil {
				t.Fatalf("GET %s = %d %s", tc.query, response.Code, response.Body)
			}
			var ids []string
			for _, message := range page.Messages {
				ids = append(ids, message.ID)
			}
			if !slices.Equal(ids, tc.want) || page.More != tc.isMore {
				t.Errorf("GET %s = %v, more %t; want %v, more %t", tc.query, ids, page.More, tc.want, tc.isMore)
			}
		})
	}
	t.Run("a task's page holds what each message is", func(t *testing.T) {
		var page fleettree.MessagePage
		if response := get(task); json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Messages) != 3 {
			t.Fatalf("GET %s = %d %s", task, response.Code, response.Body)
		}
		agent, prompt, reply := page.Messages[0], page.Messages[1], page.Messages[2]
		if len(agent.Blocks) != 1 || agent.Blocks[0].Agent.Node != "subagent:toolu_A" || agent.Blocks[0].State != fleettree.CallDone {
			t.Errorf("sub-agent call = %+v; want its node and done", agent)
		}
		if prompt.Source != fleettree.FromOverlord || prompt.Text != "Why is the card red?" || len(prompt.Pictures) != 1 || reply.Blocks[0].Text != "Its gate failed." {
			t.Errorf("prompt %+v and reply %+v; want his question with its picture and the answer", prompt, reply)
		}
	})
	t.Run("a picture a message holds", func(t *testing.T) {
		response := get(task + "&picture=1")
		if response.Code != 200 || response.Header().Get("Content-Type") != "image/png" || response.Body.String() != "a screenshot" {
			t.Errorf("picture = %d %s %q; want the screenshot", response.Code, response.Header().Get("Content-Type"), response.Body)
		}
	})
	for _, tc := range []struct {
		name, query string
		code        int
	}{
		{"no token", "task=" + meta.ID + "&generation=" + meta.SpawnGen, 403},
		{"a wrong token", "task=" + meta.ID + "&generation=" + meta.SpawnGen + "&token=another-board", 403},
		{"an unknown task", "task=nobody&generation=" + meta.SpawnGen + "&token=board-token", 404},
		{"a stale generation", "task=" + meta.ID + "&generation=s1&token=board-token", 409},
		{"no session named", "token=board-token", 400},
		{"an invalid task", "task=" + url.QueryEscape(`..\x`) + "&generation=g1&token=board-token", 400},
		{"a Codex task", "task=task-1&generation=g1&token=board-token", 404},
		{"the CFO in another terminal", "cfo=elsewhere&token=board-token", 409},
		{"a page before no message", task + "&before=first", 400},
		{"a picture no message holds", task + "&picture=9", 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			response := get(tc.query)

			// Assert
			if response.Code != tc.code || strings.Contains(response.Body.String(), "card red") {
				t.Errorf("GET %s = %d %s, want %d and no message", tc.query, response.Code, response.Body, tc.code)
			}
		})
	}

	server := httptest.NewUnstartedServer(handler)
	handler.Host = server.Listener.Addr().String()
	handler.terminalTick = 100 * time.Millisecond
	server.Start()
	defer server.Close()
	t.Run("a stream follows the conversation and ends once the task restarted", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/messages?"+task+"&stream=1", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.Header.Get("Content-Type") != "text/event-stream" {
			t.Fatalf("not streaming: %d %s", response.StatusCode, response.Header.Get("Content-Type"))
		}
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(nil, 1<<20)
		read := func(event, want string) {
			t.Helper()
			current := ""
			for scanner.Scan() {
				line := scanner.Text()
				if name, isEvent := strings.CutPrefix(line, "event: "); isEvent {
					current = name
				}
				if current == event && strings.HasPrefix(line, "data: ") && strings.Contains(line, want) {
					return
				}
			}
			t.Fatalf("stream sent no %s event with %q: %v", event, want, scanner.Err())
		}

		// Act and Assert
		read("page", `"text":"Its gate failed."`)
		appendEntries(t, conversation, `{"type":"assistant","uuid":"a2","timestamp":"`+stamp+`","message":{"id":"msg_2","model":"claude-opus-5-5","role":"assistant","content":[{"type":"text","text":"Rerunning it."}]}}`)
		read("messages", `"id":"msg_2"`)
		restarted := meta
		restarted.SpawnGen = "s1"
		if err := state.WriteTaskMeta(h.State, restarted); err != nil {
			t.Fatal(err)
		}
		read("unavailable", "restarted or was replaced")
	})
}
