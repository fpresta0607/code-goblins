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
	"reflect"
	"slices"
	"strconv"
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
		"a phase the board cannot read": {Session{Phase: "queued"}, true, false, false},
	} {
		got := turnState(tc.node, tc.isTracked, nil)
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
	held := []fleettree.Message{{ID: "u1", Seq: 2}, {ID: "a1", Seq: 3}, {ID: "u2", Seq: 4}, {ID: "a2", Seq: 5}}
	for name, tc := range map[string]struct {
		read []fleettree.Message
		want bool
	}{
		"a read past the stream":   {[]fleettree.Message{{ID: "u1", Seq: 2}, {ID: "a1", Seq: 3}, {ID: "u2", Seq: 4}, {ID: "a2", Seq: 5}, {ID: "u3", Seq: 6}}, false},
		"a read of what it holds":  {slices.Clone(held), false},
		"a read that begins later": {[]fleettree.Message{{ID: "u2", Seq: 4}, {ID: "a2", Seq: 5}}, false},
		// The newest page once the session wrote more than a page: it begins
		// past the stream's oldest message and ends past its newest.
		"a page that slid on past the stream": {[]fleettree.Message{{ID: "u2", Seq: 4}, {ID: "a2", Seq: 5}, {ID: "u3", Seq: 6}, {ID: "a3", Seq: 7}}, false},
		"a stream that began after the read":  {[]fleettree.Message{{ID: "u0", Seq: 0}, {ID: "a0", Seq: 1}, {ID: "u1", Seq: 2}, {ID: "a1", Seq: 3}, {ID: "u2", Seq: 4}, {ID: "a2", Seq: 5}}, false},
		"a read of other messages":            {[]fleettree.Message{{ID: "u1", Seq: 2}, {ID: "u9", Seq: 3}}, true},
		"a read that dropped the newest":      {[]fleettree.Message{{ID: "u1", Seq: 2}, {ID: "a1", Seq: 3}, {ID: "u2", Seq: 4}}, true},
		"a read that shares nothing with it":  {[]fleettree.Message{{ID: "u7", Seq: 14}, {ID: "a7", Seq: 15}}, true},
		"a read of an empty record":           {nil, false},
		"a stream that holds nothing":         {[]fleettree.Message{{ID: "u1", Seq: 0}}, false},
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

// conversationOf is the transcript of a Claude Code session's exchanges from
// one to before another, each a question and its answer.
func conversationOf(stamp string, from, to int) []string {
	entries := make([]string, 0, 2*(to-from))
	for at := from; at < to; at++ {
		n := strconv.Itoa(at)
		entries = append(entries,
			`{"type":"user","uuid":"u`+n+`","timestamp":"`+stamp+`","message":{"role":"user","content":"Question `+n+`"}}`,
			`{"type":"assistant","uuid":"a`+n+`","timestamp":"`+stamp+`","message":{"id":"msg_`+n+`","model":"claude-opus-5-5","role":"assistant","content":[{"type":"text","text":"Answer `+n+`"}]}}`)
	}
	return entries
}

// TestAViewReopenedOnALongSessionRestatesNothing is the newest page of a
// session longer than a page, read again once its harness wrote more: that
// page slid past messages the stream holds, which only carries the stream on,
// so the stream holds the whole of both reads and restates neither.
func TestAViewReopenedOnALongSessionRestatesNothing(t *testing.T) {
	// Arrange: a view read the newest page of a session of 30 exchanges.
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	get, meta, service := messagesBoard(t, conversationOf(stamp, 0, 30)...)
	query := "task=" + meta.ID + "&generation=" + meta.SpawnGen + "&token=board-token"
	var first fleettree.MessagePage
	if response := get(query); response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &first) != nil || !first.More || len(first.Messages) == 0 {
		t.Fatalf("the first read is not the newest page of a longer session: %d %s", response.Code, response.Body)
	}
	appendEntries(t, filepath.Join(service.Options.Tree.Home, ".claude", "projects", "C--work-talker", "talker-conversation.jsonl"), conversationOf(stamp, 30, 31)...)

	// Act: a view opens on the session again.
	response := get(query)

	// Assert
	var page fleettree.MessagePage
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Messages) == 0 || page.Messages[len(page.Messages)-1].ID != "msg_30" {
		t.Fatalf("the reopened view's page does not end at the newest answer: %d %s", response.Code, response.Body)
	}
	events, err := sessionstream.Read(service.Store.Home.State, sessionstream.Identity{Task: meta.ID, Generation: meta.SpawnGen, Harness: "claude", Session: "talker-conversation"})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Kind == sessionstream.KindRestated {
			t.Fatalf("a page that slid on past the stream was restated: %d events", len(events))
		}
	}
	var ids []string
	for _, message := range fleettree.MessagesOf(events) {
		ids = append(ids, message.ID)
	}
	if !slices.Contains(ids, first.Messages[0].ID) || !slices.Contains(ids, "msg_30") {
		t.Fatalf("the stream holds %v; want %s, the first read's oldest, through msg_30", ids, first.Messages[0].ID)
	}
}

// TestASupervisorThatNeverReadTheStreamWeighsAReadAgainstItsFile is a harness
// that rewrote its record while no supervisor watched, as pi's branch reset
// drops an answer: a supervisor that has not read the stream yet reads it from
// its file before it compares a read with it, so the answer the harness
// dropped is restated away rather than drawn beside the one that replaced it.
func TestASupervisorThatNeverReadTheStreamWeighsAReadAgainstItsFile(t *testing.T) {
	// Arrange: one supervisor drew the session, its harness then replaced the
	// answer, and another supervisor started.
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	get, meta, service := messagesBoard(t, messageEntries(stamp)...)
	query := "task=" + meta.ID + "&generation=" + meta.SpawnGen + "&token=board-token"
	if response := get(query); response.Code != 200 || !strings.Contains(response.Body.String(), "Its gate failed.") {
		t.Fatalf("the first supervisor's read: %d %s", response.Code, response.Body)
	}
	conversation := filepath.Join(service.Options.Tree.Home, ".claude", "projects", "C--work-talker", "talker-conversation.jsonl")
	data, err := os.ReadFile(conversation)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, conversation, strings.NewReplacer(`"uuid":"a1"`, `"uuid":"a9"`, `"id":"msg_1"`, `"id":"msg_9"`, "Its gate failed.", "Its gate passed on a rerun.").Replace(string(data)))
	restarted := &Service{Store: service.Store, Instance: "board-token", Options: Options{Tree: &fleettree.Reader{Home: service.Options.Tree.Home}}}
	restarted.readTrees(t.Context())
	handler := NewHTTP(restarted, "127.0.0.1:50000", nil)

	// Act
	response := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "http://127.0.0.1:50000/api/messages?"+query, nil)
	request.RemoteAddr = "127.0.0.1:50001"
	handler.ServeHTTP(response, request)

	// Assert
	if response.Code != 200 || !strings.Contains(response.Body.String(), "Its gate passed on a rerun.") || strings.Contains(response.Body.String(), "Its gate failed.") {
		t.Fatalf("the restarted supervisor drew the answer its harness dropped: %d %s", response.Code, response.Body)
	}
	events, err := sessionstream.Read(service.Store.Home.State, sessionstream.Identity{Task: meta.ID, Generation: meta.SpawnGen, Harness: "claude", Session: "talker-conversation"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(events, func(event sessionstream.Event) bool { return event.Kind == sessionstream.KindRestated }) {
		t.Fatalf("the stream does not restate the rewritten record: %d events", len(events))
	}
}

// TestEveryChangeOfASessionsTurnIsTold is a session's own report of one turn
// told through the board's stream as it changes: the turn runs, ends, runs
// again on a notice its prompt did not begin, and ends again, and the board is
// told each change, however often the one turn made it.
func TestEveryChangeOfASessionsTurnIsTold(t *testing.T) {
	// Arrange
	stamp := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	_, meta, service := messagesBoard(t, messageEntries(stamp)...)
	report := func(phase string) {
		t.Helper()
		service.Store.mu.Lock()
		defer service.Store.mu.Unlock()
		service.Store.db.Sessions["claude/talker-conversation"] = Session{
			ID: "claude/talker-conversation", NativeID: "talker-conversation", Harness: "claude", Role: "goblin",
			TaskID: meta.ID, Generation: meta.SpawnGen, Phase: phase, UpdatedAt: time.Now().UTC(),
		}
		if err := service.Store.save(); err != nil {
			t.Fatal(err)
		}
	}
	report("active")
	handler := NewHTTP(service, "127.0.0.1:50000", nil)
	handler.terminalTick = 50 * time.Millisecond
	server := httptest.NewUnstartedServer(handler)
	handler.Host = server.Listener.Addr().String()
	server.Start()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
	told := func(want string) {
		t.Helper()
		current := ""
		for scanner.Scan() {
			if name, isEvent := strings.CutPrefix(scanner.Text(), "event: "); isEvent {
				current = name
			}
			data, isData := strings.CutPrefix(scanner.Text(), "data: ")
			var said struct{ Turn, State string }
			if !isData || current != "session" || json.Unmarshal([]byte(data), &said) != nil || said.State != want {
				continue
			}
			if said.Turn != "u1" {
				t.Fatalf("the board was told of turn %q; want u1, which the notice did not end", said.Turn)
			}
			return
		}
		t.Fatalf("the board was never told the turn is %s: %v", want, scanner.Err())
	}

	// Act and Assert
	told("running")
	report("settled")
	told("ended")
	appendEntries(t, fleettree.SessionTranscript(service.Options.Tree.Home, "claude", "talker-conversation"),
		`{"type":"user","uuid":"n1","timestamp":"`+time.Now().UTC().Format(time.RFC3339Nano)+`","message":{"role":"user","content":"<task-notification>\n<summary>Background command finished</summary>\n</task-notification>"}}`)
	report("active")
	told("running")
	report("settled")
	told("ended")
}

// openMessagesStream opens a view's stream of the goblin's messages on a board
// that checks its session every 50 ms, and answers the data of the next event
// named event whose data carries carries.
func openMessagesStream(t *testing.T, service *Service, meta state.TaskMeta) func(event, carries string) string {
	t.Helper()
	handler := NewHTTP(service, "127.0.0.1:50000", nil)
	handler.terminalTick = 50 * time.Millisecond
	server := httptest.NewUnstartedServer(handler)
	handler.Host = server.Listener.Addr().String()
	server.Start()
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	request, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/messages?task="+meta.ID+"&generation="+meta.SpawnGen+"&token=board-token&stream=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(nil, 1<<20)
	return func(event, carries string) string {
		t.Helper()
		current := ""
		for scanner.Scan() {
			if name, isEvent := strings.CutPrefix(scanner.Text(), "event: "); isEvent {
				current = name
			}
			if data, isData := strings.CutPrefix(scanner.Text(), "data: "); isData && current == event && strings.Contains(data, carries) {
				return data
			}
		}
		t.Fatalf("the board was sent no %s event carrying %q: %v", event, carries, scanner.Err())
		return ""
	}
}

// TestACommandOrAnInterruptLeavesNoTurnRunning is a session's record holding
// what no hook of its harness reports, told through the board's stream: a
// slash command Claude Code ran itself after the turn ended, which begins no
// turn, and an interrupt while the turn ran, which ended it, so the board is
// told the turn ended after either.
func TestACommandOrAnInterruptLeavesNoTurnRunning(t *testing.T) {
	for name, tc := range map[string]struct {
		phase   string
		landed  func(stamp string) []string
		carries string
	}{
		"a /model command after the turn ended": {"settled", func(stamp string) []string {
			return []string{
				`{"type":"user","uuid":"c1","timestamp":"` + stamp + `","message":{"role":"user","content":"<command-name>/model</command-name>\n<command-message>model</command-message>\n<command-args>opus</command-args>"}}`,
				`{"type":"user","uuid":"c2","timestamp":"` + stamp + `","message":{"role":"user","content":"<local-command-stdout>Set model to opus</local-command-stdout>"}}`,
			}
		}, "Set model to opus"},
		"an interrupt while the turn ran": {"active", func(stamp string) []string {
			return []string{`{"type":"user","uuid":"i1","timestamp":"` + stamp + `","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}`}
		}, "Request interrupted by user"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange: the turn's prompt a minute ago, and the session's own
			// report of the turn since.
			_, meta, service := messagesBoard(t, messageEntries(time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano))...)
			service.Store.db.Sessions["claude/talker-conversation"] = Session{
				ID: "claude/talker-conversation", NativeID: "talker-conversation", Harness: "claude", Role: "goblin",
				TaskID: meta.ID, Generation: meta.SpawnGen, Phase: tc.phase, UpdatedAt: time.Now().UTC().Add(-30 * time.Second),
			}
			if err := service.Store.save(); err != nil {
				t.Fatal(err)
			}
			next := openMessagesStream(t, service, meta)
			next("page", `"id":"u1"`)

			// Act
			appendEntries(t, fleettree.SessionTranscript(service.Options.Tree.Home, "claude", "talker-conversation"), tc.landed(time.Now().UTC().Add(-10*time.Second).Format(time.RFC3339Nano))...)

			// Assert
			next("messages", tc.carries)
			var said struct{ Turn, State string }
			if err := json.Unmarshal([]byte(next("session", "")), &said); err != nil {
				t.Fatal(err)
			}
			if said.Turn != "u1" || said.State != "ended" {
				t.Fatalf("the board was told turn %q is %q; want u1 ended", said.Turn, said.State)
			}
		})
	}
}

// TestAViewIsToldOfARunningSubAgentFromTheStream is a sub-agent call told to
// a view while it runs, as the stream holds it: the call names the baby goblin
// it started, so the board draws its row and its Open before the sub-agent's
// result comes back.
func TestAViewIsToldOfARunningSubAgentFromTheStream(t *testing.T) {
	// Arrange
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	_, meta, service := messagesBoard(t, messageEntries(stamp)...)
	next := openMessagesStream(t, service, meta)
	next("page", `"id":"u1"`)

	// Act
	appendEntries(t, fleettree.SessionTranscript(service.Options.Tree.Home, "claude", "talker-conversation"),
		`{"type":"assistant","uuid":"a2","timestamp":"`+stamp+`","message":{"id":"msg_2","model":"claude-opus-5-5","role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Task","input":{"subagent_type":"code-reviewer","description":"Review the branch","prompt":"Review it."}}]}}`)
	var told struct {
		Messages []fleettree.Message `json:"messages"`
	}
	if err := json.Unmarshal([]byte(next("messages", `"id":"msg_2"`)), &told); err != nil {
		t.Fatal(err)
	}

	// Assert
	at := slices.IndexFunc(told.Messages, func(message fleettree.Message) bool { return message.ID == "msg_2" })
	if at < 0 || len(told.Messages[at].Blocks) != 1 {
		t.Fatalf("the view was told %+v; want the reply with its call", told.Messages)
	}
	call := told.Messages[at].Blocks[0]
	if want := (fleettree.AgentLink{Node: "subagent:toolu_1", Type: "code-reviewer"}); call.State != fleettree.CallRunning || call.Agent != want {
		t.Fatalf("the running call the view was told: %+v; want it running with agent %+v", call, want)
	}
}

// TestAReplyReadAgainAfterARestatementIsDrawnWhole is a reply the stream held
// while its call ran, a restatement that does not hold it, as a view that
// reopens past a gap writes, and the older page read again once the call
// finished: the reply is written whole after the restatement, by the
// supervisor that wrote it and by one started after, so the board draws the
// reply rather than its result beside empty blocks.
func TestAReplyReadAgainAfterARestatementIsDrawnWhole(t *testing.T) {
	for name, isRestarted := range map[string]bool{"the supervisor that restated it": false, "a supervisor started after": true} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			id := sessionstream.Identity{Task: "talker", Generation: "s1", Harness: "claude", Session: "talker-conversation"}
			held, err := (&Service{Store: store}).sessionStreamOf(id)
			if err != nil {
				t.Fatal(err)
			}
			write := func(held *heldStream, events []sessionstream.Event) []fleettree.Message {
				t.Helper()
				projection, err := held.record(func([]fleettree.Message, []sessionstream.Event) []sessionstream.Event { return events })
				if err != nil {
					t.Fatal(err)
				}
				return projection
			}
			at := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
			prompt := fleettree.Message{ID: "u1", Seq: 0, Kind: fleettree.MessagePrompt, At: at, Source: fleettree.FromOverlord, Text: "Read b.go"}
			reply := func(state, result string) fleettree.Message {
				return fleettree.Message{ID: "msg_1", Seq: 1, Kind: fleettree.MessageReply, At: at, Model: "claude-opus-5-5", Blocks: []fleettree.Block{
					{Kind: fleettree.BlockText, Text: "Reading b.go."},
					{Kind: fleettree.BlockCall, CallID: "toolu_1", Tool: "Read", Subject: "b.go", State: state, Result: result},
				}}
			}
			write(held, fleettree.StreamEvents(id, []fleettree.Message{prompt, reply(fleettree.CallRunning, "")}))
			write(held, []sessionstream.Event{fleettree.RestatedEvent(id, []fleettree.Message{{ID: "u9", Seq: 90, Kind: fleettree.MessagePrompt, At: at, Source: fleettree.FromOverlord, Text: "Later"}})})
			if isRestarted {
				if held, err = (&Service{Store: store}).sessionStreamOf(id); err != nil {
					t.Fatal(err)
				}
			}
			finished := reply(fleettree.CallDone, "package b")

			// Act
			projection := write(held, fleettree.StreamEvents(id, []fleettree.Message{prompt, finished}))

			// Assert
			drawn := slices.IndexFunc(projection, func(message fleettree.Message) bool { return message.ID == "msg_1" })
			if drawn < 0 || !reflect.DeepEqual(projection[drawn], finished) {
				t.Fatalf("the board draws %+v; want the reply whole: %+v", projection, finished)
			}
		})
	}
}

// TestAStreamWhoseWriteFailedIsReadAgainFromItsFile is a write the stream's
// file refused, after which the file holds an entry this supervisor never saw
// land, as an append whose cut back failed too leaves one: the next write reads
// the stream again from its file, so the entry is neither written twice nor
// missing from what the board draws.
func TestAStreamWhoseWriteFailedIsReadAgainFromItsFile(t *testing.T) {
	// Arrange: a stream that holds a session's first prompt.
	store, _ := testStore(t)
	service := &Service{Store: store}
	id := sessionstream.Identity{Task: "talker", Generation: "s1", Harness: "claude", Session: "talker-conversation"}
	held, err := service.sessionStreamOf(id)
	if err != nil {
		t.Fatal(err)
	}
	write := func(events []sessionstream.Event) ([]fleettree.Message, error) {
		return held.record(func([]fleettree.Message, []sessionstream.Event) []sessionstream.Event { return events })
	}
	if _, err := write(fleettree.StreamEvents(id, []fleettree.Message{{ID: "u1", Seq: 0, Kind: fleettree.MessagePrompt, Text: "Why is the card red?"}})); err != nil {
		t.Fatal(err)
	}
	path, err := id.Path(store.Home.State)
	if err != nil {
		t.Fatal(err)
	}
	second := fleettree.StreamEvents(id, []fleettree.Message{{ID: "u2", Seq: 1, Kind: fleettree.MessagePrompt, Text: "And now?"}})
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o644) })
	if _, err := write(second); err == nil {
		t.Fatal("a write to a stream whose file refuses it succeeded")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	landed := slices.Clone(second)
	landed[0].Seq = 1
	appendEntries(t, path, strings.TrimSuffix(string(sessionstream.Encode(landed)), "\n"))

	// Act
	projection, err := write(second)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	events, err := sessionstream.Read(store.Home.State, id)
	if err != nil {
		t.Fatal(err)
	}
	written := 0
	for _, event := range events {
		if event.ID == second[0].ID {
			written++
		}
	}
	if written != 1 {
		t.Fatalf("the stream's file holds the entry %d times; want once", written)
	}
	drawn := 0
	for _, message := range projection {
		if message.ID == "u2" {
			drawn++
		}
	}
	if drawn != 1 {
		t.Fatalf("the board draws the entry %d times: %+v; want once", drawn, projection)
	}
}

// TestASessionsReportIsReadAgainstTheNewestPrompt is a session's own report
// of its turn read through the board: a settled phase older than the newest
// prompt is about the turn before it, so the stream holds the prompt's turn as
// running, and one newer than the prompt ended it.
func TestASessionsReportIsReadAgainstTheNewestPrompt(t *testing.T) {
	for name, tc := range map[string]struct {
		reported time.Duration
		want     string
	}{
		"a report older than the newest prompt": {-time.Minute, sessionstream.KindTurnState},
		"a report newer than the newest prompt": {time.Minute, sessionstream.KindTurnEnd},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			stamp := time.Now().UTC().Format(time.RFC3339Nano)
			get, meta, service := messagesBoard(t, messageEntries(stamp)...)
			service.Store.db.Sessions["claude/talker-conversation"] = Session{
				ID: "claude/talker-conversation", NativeID: "talker-conversation", Harness: "claude", Role: "goblin",
				TaskID: meta.ID, Generation: meta.SpawnGen, Phase: "settled", UpdatedAt: time.Now().UTC().Add(tc.reported),
			}
			if err := service.Store.save(); err != nil {
				t.Fatal(err)
			}

			// Act
			response := get("task=" + meta.ID + "&generation=" + meta.SpawnGen + "&token=board-token")

			// Assert
			if response.Code != 200 {
				t.Fatalf("page: %d %s", response.Code, response.Body)
			}
			events, err := sessionstream.Read(service.Store.Home.State, sessionstream.Identity{Task: meta.ID, Generation: meta.SpawnGen, Harness: "claude", Session: "talker-conversation"})
			if err != nil {
				t.Fatal(err)
			}
			turn := ""
			for _, event := range events {
				switch event.Kind {
				case sessionstream.KindTurnState, sessionstream.KindTurnEnd, sessionstream.KindMissing:
					turn = event.Kind
				}
			}
			if turn != tc.want {
				t.Fatalf("the turn the stream holds: %q; want %q", turn, tc.want)
			}
		})
	}
}

// TestAViewIsToldOfACallFromTheStream is the wiring of the board's changes to
// the fleet's stream: a call that finished is told to the view as the stream
// holds it, and the stream holds the call's own event, what it was called on.
func TestAViewIsToldOfACallFromTheStream(t *testing.T) {
	// Arrange
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	_, meta, service := messagesBoard(t, messageEntries(stamp)...)
	handler := NewHTTP(service, "127.0.0.1:50000", nil)
	handler.terminalTick = 100 * time.Millisecond
	server := httptest.NewUnstartedServer(handler)
	handler.Host = server.Listener.Addr().String()
	server.Start()
	defer server.Close()
	conversation := fleettree.SessionTranscript(service.Options.Tree.Home, "claude", "talker-conversation")
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
	read := func(want string) string {
		t.Helper()
		current := ""
		for scanner.Scan() {
			if name, isEvent := strings.CutPrefix(scanner.Text(), "event: "); isEvent {
				current = name
			}
			if data, isData := strings.CutPrefix(scanner.Text(), "data: "); isData && current == want {
				return data
			}
		}
		t.Fatalf("no %s event: %v", want, scanner.Err())
		return ""
	}
	read("page")

	// Act: the call runs and its result lands.
	appendEntries(t, conversation,
		`{"type":"assistant","uuid":"a2","timestamp":"`+stamp+`","message":{"id":"msg_2","model":"claude-opus-5-5","role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"b.go"}}]}}`,
		`{"type":"user","uuid":"a3","timestamp":"`+stamp+`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"package b"}]},"toolUseResult":{}}`)
	said := read("messages")

	// Assert: the view is told the call ended, with what came back.
	if !strings.Contains(said, `"state":"done"`) || !strings.Contains(said, "package b") {
		t.Fatalf("the change a view is told: %s", said)
	}
	events, err := sessionstream.Read(service.Store.Home.State, sessionstream.Identity{Task: meta.ID, Generation: meta.SpawnGen, Harness: "claude", Session: "talker-conversation"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	for _, event := range events {
		if event.Kind == sessionstream.KindToolCall && event.Subject == "b.go" {
			calls++
		}
	}
	if calls != 1 {
		t.Fatalf("the stream holds %d events of the call; want the call with what it was called on", calls)
	}
}
