package supervisor

import (
	"bufio"
	"context"
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
	"github.com/fpresta0607/code-goblins/internal/sessionstream"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// messagesBoard is a board whose user home holds the goblin's conversation,
// as the messages tests arrange one.
func messagesBoard(t *testing.T, entries ...string) (func(string) *httptest.ResponseRecorder, state.TaskMeta, *Service) {
	t.Helper()
	store, h := testStore(t)
	userHome := t.TempDir()
	meta := treeGoblin(t, h.State, userHome, "talker", true)
	appendEntries(t, filepath.Join(userHome, ".claude", "projects", "C--work-talker", "talker-conversation.jsonl"), entries...)
	service := &Service{Store: store, Instance: "board-token", Options: Options{Tree: &fleettree.Reader{Home: userHome}}}
	service.readTrees(t.Context())
	handler := NewHTTP(service, "127.0.0.1:50000", nil)
	get := func(query string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		request := httptest.NewRequest("GET", "http://127.0.0.1:50000/api/messages?"+query, nil)
		request.RemoteAddr = "127.0.0.1:50001"
		handler.ServeHTTP(response, request)
		return response
	}
	return get, meta, service
}

func messageEntries(stamp string) []string {
	return []string{
		`{"type":"user","uuid":"u1","timestamp":"` + stamp + `","message":{"role":"user","content":"Why is the card red?"}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"` + stamp + `","message":{"id":"msg_1","model":"claude-opus-5-5","role":"assistant","content":[{"type":"text","text":"Its gate failed."}]}}`,
	}
}

// TestTheBoardWritesTheSessionsStreamAsItReadsIt keeps the fleet's one record
// of a session: the supervisor writes what it read of the harness into the
// session's stream, under the task's generation, and every later read of the
// session is that stream.
func TestTheBoardWritesTheSessionsStreamAsItReadsIt(t *testing.T) {
	// Arrange
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	get, meta, service := messagesBoard(t, messageEntries(stamp)...)
	query := "task=" + meta.ID + "&generation=" + meta.SpawnGen + "&token=board-token"

	// Act
	response := get(query)

	// Assert
	var page fleettree.MessagePage
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Messages) != 3 {
		t.Fatalf("GET %s = %d %s", query, response.Code, response.Body)
	}
	stream := filepath.Join(service.Store.Home.State, "conversations", meta.ID, meta.SpawnGen, "talker-conversation.jsonl")
	data, err := os.ReadFile(stream)
	if err != nil {
		t.Fatalf("the supervisor wrote no stream at %s: %v", stream, err)
	}
	var kinds []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var event struct {
			Kind    string `json:"kind"`
			Missing string `json:"missing"`
		}
		if json.Unmarshal([]byte(line), &event) != nil || event.Kind == "" {
			t.Fatalf("stream entry %q names no event", line)
		}
		kinds = append(kinds, event.Kind)
		// A session whose harness has reported no turn says so, rather than
		// leaving the board to guess whether one runs.
		if event.Kind == "missing" && event.Missing == "" {
			t.Fatalf("a missing event says nothing of what is missing: %q", line)
		}
	}
	if want := []string{"tool_call", "tool_result", "prompt", "text", "missing"}; !slices.Equal(kinds, want) {
		t.Fatalf("stream events %v; want %v", kinds, want)
	}
	if page.Messages[1].ID != "u1" || page.Messages[2].Blocks[0].Text != "Its gate failed." {
		t.Fatalf("the board drew something other than the stream: %+v", page.Messages)
	}
}

// TestASessionStreamThatCannotBeReadIsReportedAndItsRecordStands keeps a
// damaged stream from showing an empty conversation: the session's own record
// is drawn, and the board says what it could not read.
func TestASessionStreamThatCannotBeReadIsReportedAndItsRecordStands(t *testing.T) {
	// Arrange
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	get, meta, service := messagesBoard(t, messageEntries(stamp)...)
	writeFile(t, filepath.Join(service.Store.Home.State, "conversations", meta.ID, meta.SpawnGen, "talker-conversation.jsonl"), "not an event\n")
	query := "task=" + meta.ID + "&generation=" + meta.SpawnGen + "&token=board-token"

	// Act
	response := get(query)

	// Assert
	var page fleettree.MessagePage
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Messages) != 3 {
		t.Fatalf("a damaged stream did not fall back to the session's record: %d %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body.String(), "Why is the card red?") {
		t.Fatalf("the board drew no conversation: %s", response.Body)
	}
}

func TestWhatASessionsOwnReportSaysOfItsTurn(t *testing.T) {
	for name, tc := range map[string]struct {
		node      Session
		isTracked bool
		ended     bool
		known     bool
	}{
		"a settled turn":                {Session{Phase: "settled"}, true, true, true},
		"an interrupted turn":           {Session{Phase: "interrupted"}, true, true, true},
		"a running turn":                {Session{Phase: "active"}, true, false, true},
		"a turn it began":               {Session{Phase: "started"}, true, false, true},
		"a session untracked":           {Session{}, false, false, false},
		"a session that compacts":       {Session{Phase: "compacting"}, true, false, true},
		"a phase the board cannot read": {Session{Phase: "queued"}, true, false, false},
	} {
		got := turnState(tc.node, tc.isTracked, time.Time{})
		if got.Ended != tc.ended || got.Known != tc.known {
			t.Fatalf("%s: %+v; want ended %t, known %t", name, got, tc.ended, tc.known)
		}
		if !got.Known && got.Why == "" {
			t.Fatalf("%s: an unknown turn says nothing of why", name)
		}
	}
}

func TestWhatTheStreamSaysOfATurn(t *testing.T) {
	prompts := []fleettree.Message{{ID: "p1", Seq: 0, Kind: fleettree.MessagePrompt, Text: "go"}, {ID: "r1", Seq: 1, Kind: fleettree.MessageReply}}
	for name, tc := range map[string]struct {
		messages []fleettree.Message
		events   []sessionstream.Event
		state    string
	}{
		"a turn still running":       {prompts, nil, "running"},
		"a turn that ended":          {prompts, []sessionstream.Event{{Kind: "turn_end", Turn: "p1"}}, "ended"},
		"a turn never reported":      {prompts, []sessionstream.Event{{Kind: "missing", Turn: "p1", Missing: "no hooks"}}, "unknown"},
		"a report that says it runs": {prompts, []sessionstream.Event{{Kind: "missing", Turn: "p1", Missing: "no hooks"}, {Kind: "turn_state", Turn: "p1"}}, "running"},
		"a session with no turn":     {nil, nil, "none"},
		"an earlier turn's end":      {prompts, []sessionstream.Event{{Kind: "turn_end", Turn: "p0"}}, "running"},
	} {
		turn, got, why := streamTurn(tc.messages, tc.events)
		if got != tc.state {
			t.Fatalf("%s: %q; want %q", name, got, tc.state)
		}
		if tc.messages == nil {
			if turn != "" {
				t.Fatalf("%s names turn %q", name, turn)
			}
			continue
		}
		if turn != "p1" {
			t.Fatalf("%s names turn %q; want p1", name, turn)
		}
		if got == "unknown" && why == "" {
			t.Fatalf("%s says nothing of why the turn is unknown", name)
		}
	}
}

func TestThePageTheBoardDrawsIsTheStreams(t *testing.T) {
	// Arrange
	transcript := fleettree.MessagePage{Conversation: "conv-1", More: false, Messages: []fleettree.Message{{ID: "t1", Seq: 0, Kind: fleettree.MessagePrompt}}}
	projection := []fleettree.Message{{ID: "s1", Seq: 7, Kind: fleettree.MessagePrompt}, {ID: "s2", Seq: 9, Kind: fleettree.MessageReply}}

	// Act
	page := pageOfStream(transcript, projection, -1)
	older := pageOfStream(transcript, projection, 9)

	// Assert
	if len(page.Messages) != 2 || page.Messages[0].ID != "s1" || page.Conversation != "conv-1" {
		t.Fatalf("the page of the stream: %+v", page)
	}
	if len(older.Messages) != 1 || older.Messages[0].ID != "s1" {
		t.Fatalf("the page before a message: %+v", older)
	}
}

func TestAPageTheStreamDoesNotHoldIsTheRecords(t *testing.T) {
	// Arrange
	transcript := fleettree.MessagePage{Conversation: "conv-1", Messages: []fleettree.Message{{ID: "t1", Seq: 0, Kind: fleettree.MessagePrompt}}}
	projection := []fleettree.Message{{ID: "s2", Seq: 9, Kind: fleettree.MessageReply}}

	// Act: an older page the stream began after, and a session with no stream.
	older := pageOfStream(transcript, projection, 9)
	none := pageOfStream(transcript, nil, -1)

	// Assert
	if len(older.Messages) != 1 || older.Messages[0].ID != "t1" {
		t.Fatalf("an older page the stream does not hold: %+v", older)
	}
	if len(none.Messages) != 1 || none.Messages[0].ID != "t1" {
		t.Fatalf("a session with no stream: %+v", none)
	}
}

func TestTheChangesAViewIsToldAreTheStreams(t *testing.T) {
	// Arrange
	changed := []fleettree.Message{{ID: "r1", Seq: 1, Blocks: []fleettree.Block{{Kind: fleettree.BlockCall, State: fleettree.CallRunning}}}}
	projection := []fleettree.Message{{ID: "r1", Seq: 1, Blocks: []fleettree.Block{{Kind: fleettree.BlockCall, State: fleettree.CallDone, Result: "done"}}}}

	// Act
	kept := changedOfStream(changed, projection)

	// Assert
	if len(kept) != 1 || kept[0].Blocks[0].State != fleettree.CallDone {
		t.Fatalf("a changed message the stream holds: %+v", kept)
	}
}

// TestAStreamSaysWhatTheSessionsTurnIs is the wire the board draws a turn
// from: a session event naming the turn, whether it runs, ended or was never
// reported, and where the stream's head is, so a view never infers a turn from
// how long it has been quiet.
func TestAStreamSaysWhatTheSessionsTurnIs(t *testing.T) {
	// Arrange
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	_, meta, service := messagesBoard(t, messageEntries(stamp)...)
	handler := NewHTTP(service, "127.0.0.1:50000", nil)
	handler.terminalTick = 100 * time.Millisecond
	server := httptest.NewUnstartedServer(handler)
	handler.Host = server.Listener.Addr().String()
	server.Start()
	defer server.Close()

	// Act
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/messages?task="+meta.ID+"&generation="+meta.SpawnGen+"&token=board-token&stream=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	// Assert: this fixture's session has reported no turn, so the board is
	// told so rather than left to guess, and the head the view draws to.
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(nil, 1<<20)
	current, said := "", ""
	for scanner.Scan() && said == "" {
		line := scanner.Text()
		if name, isEvent := strings.CutPrefix(line, "event: "); isEvent {
			current = name
		}
		if current == "session" && strings.HasPrefix(line, "data: ") {
			said = line
		}
	}
	var report struct {
		Turn   string `json:"turn"`
		State  string `json:"state"`
		Why    string `json:"why"`
		Newest int    `json:"newest"`
		Head   int    `json:"head"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(said, "data: ")), &report); err != nil {
		t.Fatalf("session event %q: %v", said, err)
	}
	if report.Turn == "" || report.State != "unknown" || report.Why == "" {
		t.Fatalf("a session whose harness reports no turn: %+v", report)
	}
	if report.Head != 4 || report.Newest == 0 {
		t.Fatalf("the stream's head and newest: %+v", report)
	}
}

// TestATickThatCarriesInANewMessageKeepsTheConversation is the live path a
// session's second turn takes: the read of the messages that began or changed
// since the last tick carries the stream on instead of restating it to the
// handful that read held, so the turn before it is still drawn.
func TestATickThatCarriesInANewMessageKeepsTheConversation(t *testing.T) {
	// Arrange: the stream holds a goblin's first turn, and its harness writes a
	// second while a view watches.
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	store, h := testStore(t)
	userHome := t.TempDir()
	meta := treeGoblin(t, h.State, userHome, "talker", true)
	conversation := filepath.Join(userHome, ".claude", "projects", "C--work-talker", "talker-conversation.jsonl")
	appendEntries(t, conversation, messageEntries(stamp)...)
	service := &Service{Store: store, Instance: "board-token", Options: Options{Tree: &fleettree.Reader{Home: userHome}}}
	service.readTrees(t.Context())
	handler := NewHTTP(service, "127.0.0.1:50000", nil)
	handler.terminalTick = 50 * time.Millisecond
	server := httptest.NewUnstartedServer(handler)
	handler.Host = server.Listener.Addr().String()
	server.Start()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/messages?task="+meta.ID+"&generation="+meta.SpawnGen+"&token=board-token&stream=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
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
		t.Fatalf("the stream sent no %s event with %q: %v", event, want, scanner.Err())
	}

	// Act
	read("page", `"id":"u1"`)
	appendEntries(t, conversation,
		`{"type":"user","uuid":"u2","timestamp":"`+stamp+`","message":{"role":"user","content":"And now?"}}`,
		`{"type":"assistant","uuid":"a2","timestamp":"`+stamp+`","message":{"id":"msg_2","model":"claude-opus-5-5","role":"assistant","content":[{"type":"text","text":"Rerunning it."}]}}`)
	read("messages", `"id":"u2"`)
	locate, err := handler.messagesLocator(ctx, url.Values{"task": {meta.ID}, "generation": {meta.SpawnGen}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := locate()
	if err != nil {
		t.Fatal(err)
	}
	events, err := sessionstream.Read(store.Home.State, session.Identity)

	// Assert: the conversation the stream holds is both turns, so the board
	// draws the session rather than the message that just arrived.
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, message := range fleettree.MessagesOf(events) {
		ids = append(ids, message.ID)
	}
	if !slices.Contains(ids, "u1") || !slices.Contains(ids, "u2") {
		t.Fatalf("the stream holds %v; want both turns", ids)
	}
}

// TestAReadThatOnlyCarriesOnTheStreamIsNoRestatement keeps an ordinary read of
// newer messages from being taken for a harness that rewrote its record.
func TestAReadThatOnlyCarriesOnTheStreamIsNoRestatement(t *testing.T) {
	held := []fleettree.Message{{ID: "u1", Seq: 0}, {ID: "a1", Seq: 1}}
	for name, tc := range map[string]struct {
		read []fleettree.Message
		want bool
	}{
		"a read past the stream":      {[]fleettree.Message{{ID: "u1", Seq: 0}, {ID: "a1", Seq: 1}, {ID: "u2", Seq: 2}}, false},
		"a read of what it holds":     {[]fleettree.Message{{ID: "u1", Seq: 0}, {ID: "a1", Seq: 1}}, false},
		"a read that begins later":    {[]fleettree.Message{{ID: "a1", Seq: 1}}, false},
		"a read of other messages":    {[]fleettree.Message{{ID: "u1", Seq: 0}, {ID: "u2", Seq: 1}}, true},
		"a read of an empty record":   {nil, false},
		"a stream that holds nothing": {[]fleettree.Message{{ID: "u1", Seq: 0}}, false},
	} {
		held := held
		if name == "a stream that holds nothing" {
			held = nil
		}
		if got := streamDiverges(held, tc.read); got != tc.want {
			t.Fatalf("%s: %t; want %t", name, got, tc.want)
		}
	}
}
