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
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestOlderMessagesRequireMatchingConversationIdentity(t *testing.T) {
	store, taskHome := testStore(t)
	profile := t.TempDir()
	meta := codexGoblin(t, taskHome.State, profile, "codgester", "s1")
	handler := NewHTTP(&Service{Store: store, Instance: "board-token", Options: Options{Tree: &fleettree.Reader{Home: profile}}}, "127.0.0.1:50000", nil)
	query := "task=" + meta.ID + "&generation=s1&token=board-token"
	get := func(extra string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		request := httptest.NewRequest("GET", "http://"+handler.Host+"/api/messages?"+query+extra, nil)
		request.RemoteAddr = "127.0.0.1:50001"
		handler.ServeHTTP(response, request)
		return response
	}
	page := get("")
	var wire struct {
		Conversation string `json:"conversation"`
	}
	if page.Code != 200 || json.Unmarshal(page.Body.Bytes(), &wire) != nil || wire.Conversation == "" {
		t.Fatalf("newest page lacks identity: %d %s", page.Code, page.Body)
	}
	if response := get("&before=1"); response.Code != http.StatusBadRequest {
		t.Fatalf("older request without identity: %d %s", response.Code, response.Body)
	}
	older := "&before=1&conversation=" + url.QueryEscape(wire.Conversation)
	if response := get(older); response.Code != http.StatusOK {
		t.Fatalf("older request with matching identity: %d %s", response.Code, response.Body)
	}
	path := fleettree.SessionTranscript(profile, "codex", "codgester-session")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	replaced := strings.Replace(string(data), "Why is the card red?", "Why is the card tan?", 1)
	if replaced == string(data) {
		t.Fatal("fixture text missing")
	}
	if err := os.WriteFile(path, []byte(replaced), 0o600); err != nil {
		t.Fatal(err)
	}
	if response := get(older); response.Code != http.StatusConflict {
		t.Fatalf("older request after replacement: %d %s", response.Code, response.Body)
	}
}

func TestPictureRequiresItsConversationIdentity(t *testing.T) {
	store, taskHome := testStore(t)
	profile := t.TempDir()
	meta := codexGoblin(t, taskHome.State, profile, "codgester", "s1")
	path := fleettree.SessionTranscript(profile, "codex", "codgester-session")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString(`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"picture","content":[{"type":"image","url":"data:image/png;base64,Y29udmVyc2F0aW9uIEE="}]}}}` + "\n")
	if closeErr := file.Close(); err != nil || closeErr != nil {
		t.Fatalf("append: %v %v", err, closeErr)
	}
	handler := NewHTTP(&Service{Store: store, Instance: "board-token", Options: Options{Tree: &fleettree.Reader{Home: profile}}}, "127.0.0.1:50000", nil)
	query := "task=" + meta.ID + "&generation=s1&token=board-token"
	get := func(extra string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		request := httptest.NewRequest("GET", "http://"+handler.Host+"/api/messages?"+query+extra, nil)
		request.RemoteAddr = "127.0.0.1:50001"
		handler.ServeHTTP(response, request)
		return response
	}
	response := get("")
	var page fleettree.MessagePage
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Messages) != 3 || len(page.Messages[2].Pictures) != 1 {
		t.Fatalf("page: %d %s", response.Code, response.Body)
	}
	picture := "&picture=" + url.QueryEscape(page.Messages[2].Pictures[0].ID)
	identity := "&conversation=" + url.QueryEscape(page.Conversation)
	if got := get(picture + identity); got.Code != http.StatusOK || got.Body.String() != "conversation A" || got.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("current picture: %d %s", got.Code, got.Body)
	}
	for _, test := range []struct {
		extra  string
		status int
	}{{picture, http.StatusBadRequest}, {picture + "&conversation=stale", http.StatusNotFound}} {
		if got := get(test.extra); got.Code != test.status || strings.Contains(got.Body.String(), "conversation A") || strings.HasPrefix(got.Header().Get("Content-Type"), "image/") {
			t.Errorf("unbound picture: %d %s, want %d without image bytes", got.Code, got.Body, test.status)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	replaced := strings.Replace(string(data), "Y29udmVyc2F0aW9uIEE=", "Y29udmVyc2F0aW9uIEI=", 1)
	if replaced == string(data) {
		t.Fatal("picture fixture missing")
	}
	if err := os.WriteFile(path, []byte(replaced), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := get(picture + identity); got.Code != http.StatusNotFound || strings.Contains(got.Body.String(), "conversation B") || strings.HasPrefix(got.Header().Get("Content-Type"), "image/") {
		t.Errorf("replacement picture returned bytes: %d %s", got.Code, got.Body)
	}
}

// codexGoblin is a task running Codex whose conversation the board recorded,
// with the rollout Codex writes as it works.
func codexGoblin(t *testing.T, stateDir, userHome, id, generation string) state.TaskMeta {
	t.Helper()
	meta := state.TaskMeta{
		ID: id, Project: t.TempDir(), Worktree: filepath.Join(t.TempDir(), id),
		Harness: "codex", Mode: "direct-PR", Kind: "ship", Backend: "native", SpawnGen: generation,
	}
	if err := state.WriteTaskMeta(stateDir, meta); err != nil {
		t.Fatal(err)
	}
	session := id + "-session"
	if err := state.WriteConversation(stateDir, state.Conversation{
		ID: id, Generation: generation, Harness: "codex", Session: session, ProvedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(userHome, ".codex", "sessions", "2026", "10", "06")
	writeFile(t, filepath.Join(folder, "rollout-2026-10-06T21-30-00-"+session+".jsonl"), strings.Join([]string{
		`{"timestamp":"` + stampNow() + `","type":"session_meta","payload":{"id":"` + session + `","cwd":"C:\\work\\` + id + `","source":"cli","thread_source":"user"}}`,
		`{"timestamp":"` + stampNow() + `","type":"turn_context","payload":{"model":"gpt-6-astra"}}`,
		`{"timestamp":"` + stampNow() + `","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"user-1","content":[{"type":"text","text":"Why is the card red?"}]}}}`,
		`{"timestamp":"` + stampNow() + `","type":"response_item","payload":{"type":"custom_tool_call","id":"ctc-1","call_id":"call-1","name":"exec","status":"completed","input":"text(await tools.exec_command({cmd:\"git log -1\"}));"}}`,
		`{"timestamp":"` + stampNow() + `","type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"agent-1","content":[{"type":"Text","text":"Its gate failed."}]}}}`,
		`{"timestamp":"` + stampNow() + `","type":"token_usage_record","payload":{"usage":{"total_tokens":12}}}`,
		`{"timestamp":"` + stampNow() + `","type":"response_item","payload":{"type":"custom_tool_call_output","id":"ctco-1","call_id":"call-1","output":[{"type":"input_text","text":"Script completed"}]}}`,
	}, "\n")+"\n")
	if err := os.Chtimes(filepath.Join(folder, "rollout-2026-10-06T21-30-00-"+session+".jsonl"), time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	return meta
}

// The board reads a Codex rollout as messages too, through the same view a
// Claude Code conversation and a pi session are read through.
func TestMessagesServeACodexSessionsConversation(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	userHome := t.TempDir()
	meta := codexGoblin(t, h.State, userHome, "codgester", "s1")
	service := &Service{Store: store, Instance: "board-token", Options: Options{Tree: &fleettree.Reader{Home: userHome}}}
	handler := NewHTTP(service, "127.0.0.1:50000", nil)
	response := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "http://127.0.0.1:50000/api/messages?task="+meta.ID+"&generation="+meta.SpawnGen+"&token=board-token", nil)
	request.RemoteAddr = "127.0.0.1:50001"

	// Act
	handler.ServeHTTP(response, request)

	// Assert: the prompt, the reply with its call and that call's result.
	if response.Code != 200 {
		t.Fatalf("the page answered %d: %s", response.Code, response.Body)
	}
	var page fleettree.MessagePage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatalf("the page is not a message page: %v", err)
	}
	if len(page.Messages) != 2 {
		t.Fatalf("the page holds %d messages, want the prompt and the reply: %+v", len(page.Messages), page.Messages)
	}
	if page.Messages[0].Kind != fleettree.MessagePrompt || page.Messages[0].Text != "Why is the card red?" {
		t.Fatalf("the prompt reads as %+v", page.Messages[0])
	}
	blocks := page.Messages[1].Blocks
	if page.Messages[1].Model != "gpt-6-astra" || len(blocks) != 2 || blocks[0].State != "done" || blocks[0].Result != "Script completed" || blocks[1].Text != "Its gate failed." {
		t.Fatalf("the reply reads as %+v", page.Messages[1])
	}
}

func TestCodexMessageEndpointServesRecordedPicturesAndUsage(t *testing.T) {
	store, taskHome := testStore(t)
	profile := t.TempDir()
	meta := codexGoblin(t, taskHome.State, profile, "codgester", "s1")
	path := fleettree.SessionTranscript(profile, "codex", "codgester-session")
	data := []byte("recorded image bytes")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteString(`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"picture","content":[{"type":"image","url":"data:image/png;base64,` + base64.StdEncoding.EncodeToString(data) + `"}]}}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":50,"cached_input_tokens":20,"output_tokens":10},"last_token_usage":{"input_tokens":30,"cached_input_tokens":20,"output_tokens":5},"model_context_window":1000}}}` + "\n")
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("append: %v %v", writeErr, closeErr)
	}
	handler := NewHTTP(&Service{Store: store, Instance: "board-token", Options: Options{Tree: &fleettree.Reader{Home: profile}}}, "127.0.0.1:50000", nil)
	get := func(query string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		request := httptest.NewRequest("GET", "http://"+handler.Host+"/api/messages?"+query, nil)
		request.RemoteAddr = "127.0.0.1:50001"
		handler.ServeHTTP(response, request)
		return response
	}
	query := "task=" + meta.ID + "&generation=s1&token=board-token"
	response := get(query)
	var page fleettree.MessagePage
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil {
		t.Fatalf("page: %d %s", response.Code, response.Body)
	}
	if page.Usage == nil || page.Usage.InputTokens != 50 || page.Usage.ContextTokens != 35 || page.Usage.ContextWindow != 1000 {
		t.Fatalf("usage: %+v", page.Usage)
	}
	if len(page.Messages) != 3 || len(page.Messages[2].Pictures) != 1 {
		t.Fatalf("pictures: %+v", page.Messages)
	}
	picture := page.Messages[2].Pictures[0]
	pictureQuery := "&picture=" + url.QueryEscape(picture.ID) + "&conversation=" + url.QueryEscape(page.Conversation)
	response = get(query + pictureQuery)
	if response.Code != 200 || response.Header().Get("Content-Type") != "image/png" || response.Body.String() != string(data) {
		t.Fatalf("picture: %d %s", response.Code, response.Body)
	}
	for _, test := range []struct {
		query  string
		status int
	}{
		{query + "&picture=unknown&conversation=" + url.QueryEscape(page.Conversation), 404},
		{strings.Replace(query, "generation=s1", "generation=old", 1) + pictureQuery, 409},
		{strings.Replace(query, "board-token", "other", 1) + pictureQuery, 403},
	} {
		if response := get(test.query); response.Code != test.status {
			t.Fatalf("refusal: %d %s, want %d", response.Code, response.Body, test.status)
		}
	}
}

func TestCodexCEOConversationNeedsNoHookRecordsOrTrust(t *testing.T) {
	store, taskHome := testStore(t)
	profile := t.TempDir()
	codexGoblin(t, taskHome.State, profile, "codgester", "s1")
	registerNativeCEO(t, taskHome.State, "codex", "codgester-session")
	handler := NewHTTP(&Service{Store: store, Instance: "board-token", Options: Options{Tree: &fleettree.Reader{Home: profile}}}, "127.0.0.1:50000", nil)
	response := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "http://"+handler.Host+"/api/messages?ceo="+NativeCEOTerminal+"&token=board-token", nil)
	request.RemoteAddr = "127.0.0.1:50001"

	handler.ServeHTTP(response, request)

	var page fleettree.MessagePage
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil {
		t.Fatalf("page: %d %s", response.Code, response.Body)
	}
	if len(page.Messages) != 2 || len(page.Messages[1].Blocks) != 2 || page.Messages[1].Blocks[1].Text != "Its gate failed." {
		t.Fatalf("CEO conversation: %+v", page.Messages)
	}
}

func TestCodexUsageStreamsAfterTheLastMessage(t *testing.T) {
	for _, subject := range []string{"goblin", "CEO"} {
		t.Run(subject, func(t *testing.T) {
			// Arrange
			store, taskHome := testStore(t)
			profile := t.TempDir()
			meta := codexGoblin(t, taskHome.State, profile, "codgester", "s1")
			query := "task=" + meta.ID + "&generation=" + meta.SpawnGen
			if subject == "CEO" {
				registerNativeCEO(t, taskHome.State, "codex", "codgester-session")
				query = "ceo=" + NativeCEOTerminal
			}
			handler := NewHTTP(&Service{Store: store, Instance: "board-token", Options: Options{Tree: &fleettree.Reader{Home: profile}}}, "", nil)
			server := httptest.NewUnstartedServer(handler)
			handler.Host = server.Listener.Addr().String()
			server.Start()
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			request, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/messages?"+query+"&token=board-token&stream=1", nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
				t.Fatalf("not streaming: %d %s", response.StatusCode, response.Header.Get("Content-Type"))
			}
			scanner := bufio.NewScanner(response.Body)
			scanner.Buffer(nil, 1<<20)
			read := func(want string) string {
				t.Helper()
				current := ""
				for scanner.Scan() {
					if name, isEvent := strings.CutPrefix(scanner.Text(), "event: "); isEvent {
						current = name
					}
					if data, isData := strings.CutPrefix(scanner.Text(), "data: "); isData {
						// The fleet's session event and its keep-alive are
						// not what these read; anything else is a fault.
						if current != want {
							if current == "session" || current == "alive" {
								continue
							}
							t.Fatalf("got %s event, want %s", current, want)
						}
						return data
					}
				}
				t.Fatalf("no %s event: %v", want, scanner.Err())
				return ""
			}
			var page fleettree.MessagePage
			if err := json.Unmarshal([]byte(read("page")), &page); err != nil || page.Conversation == "" {
				t.Fatalf("page identity: %+v %v", page, err)
			}
			var initial struct {
				Conversation string          `json:"conversation"`
				Usage        fleettree.Usage `json:"usage"`
			}
			if err := json.Unmarshal([]byte(read("usage")), &initial); err != nil || initial.Conversation != page.Conversation {
				t.Fatalf("usage identity: %+v %v", initial, err)
			}
			path := fleettree.SessionTranscript(profile, "codex", "codgester-session")
			for _, update := range []struct {
				records []string
				want    fleettree.Usage
			}{
				{[]string{`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":50,"cached_input_tokens":20,"output_tokens":10},"last_token_usage":{"input_tokens":30,"cached_input_tokens":20,"output_tokens":5},"model_context_window":1000}}}`}, fleettree.Usage{InputTokens: 50, CacheReadTokens: 20, OutputTokens: 10, ContextTokens: 35, ContextWindow: 1000, Model: "gpt-6-astra"}},
				{[]string{`{"type":"turn_context","payload":{"model":"gpt-6-luna"}}`, `{"type":"token_usage_record","payload":{"thread_token_usage":{"input_tokens":65,"cached_input_tokens":20,"output_tokens":11},"usage":{"input_tokens":40,"output_tokens":6,"total_tokens":46}}}`}, fleettree.Usage{InputTokens: 65, CacheReadTokens: 20, OutputTokens: 11, ContextTokens: 46, ContextWindow: 1000, Model: "gpt-6-luna"}},
			} {
				// Act: only usage records follow the last message.
				appendEntries(t, path, update.records...)

				// Assert: the same connection emits usage without another page or message.
				var event struct {
					Conversation string          `json:"conversation"`
					Usage        fleettree.Usage `json:"usage"`
				}
				if err := json.Unmarshal([]byte(read("usage")), &event); err != nil {
					t.Fatal(err)
				}
				if event.Conversation != page.Conversation || event.Usage != update.want {
					t.Fatalf("usage = %+v, want %+v for %q", event, update.want, page.Conversation)
				}
			}
		})
	}
}
