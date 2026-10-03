package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/fpresta0607/code-goblins/internal/afk"
)

func replyPipe(t *testing.T, s *Service, ctx context.Context) (*os.File, <-chan struct{}) {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(runPipeName(s.Store.Home.State))
	if err != nil {
		t.Fatal(err)
	}
	handle, _, callErr := procCreateNamedPipeW.Call(uintptr(unsafe.Pointer(name)), pipeAccessDuplex, pipeRejectRemoteClients, pipeUnlimitedInstances, 64<<10, 64<<10, 0, 0)
	if syscall.Handle(handle) == syscall.InvalidHandle {
		t.Fatal(callErr)
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ok, _, callErr := procConnectNamedPipe.Call(handle, 0)
		if ok == 0 && !errors.Is(callErr, errorPipeConnected) {
			t.Errorf("connect reply pipe: %v", callErr)
			_ = syscall.CloseHandle(syscall.Handle(handle))
			return
		}
		s.handleRunClient(ctx, syscall.Handle(handle), time.Now())
	}()
	client, err := os.OpenFile(runPipeName(s.Store.Home.State), os.O_RDWR|syscall.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		cancel()
		_ = syscall.CloseHandle(syscall.Handle(handle))
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("reply handler did not release its pipe after the client closed")
		}
	})
	if err := client.SetDeadline(time.Now().Add(runReplyTimeout + 5*time.Second)); err != nil {
		t.Fatal(err)
	}
	return client, done
}

func TestEveryPipeReplySurvivesAClientThatReadsAfterTheHandlerReplies(t *testing.T) {
	for _, test := range []struct {
		name, kind, refusal string
	}{
		{"run accepted", "", ""},
		{"run refused", "", "a run item's shell"},
		{"question accepted", "question", ""},
		{"question refused", "question", "registered CFO's own"},
		{"review accepted", "review", ""},
		{"review refused", "review", "registered CFO's own"},
		{"answer accepted", "answer", ""},
		{"answer refused", "answer", "its question is gone"},
		{"credential accepted", "credential", ""},
		{"credential refused", "credential", "registered CFO's own"},
		{"withdrawal accepted", "withdraw-run", ""},
		{"withdrawal refused", "withdraw-run", "no run"},
		{"AFK on accepted", "afk-on", ""},
		{"AFK on refused", "afk-on", "a goblin's terminal"},
		{"AFK off accepted", "afk-off", ""},
		{"AFK off refused", "afk-off", "a goblin's terminal"},
		{"AFK log accepted", "afk-log", ""},
		{"AFK log refused", "afk-log", "evidence"},
		{"report acknowledged", "reported", ""},
		{"unknown kind", "unknown", "takes no"},
		{"invalid JSON", "invalid-json", "not valid JSON"},
		{"incomplete request", "incomplete", "incomplete or over its size limit"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			_, identity, _, cfo := primaryFixture(t, store)
			s := &Service{Store: store, Options: Options{CFO: cfo}, looks: make(chan chan struct{})}
			asOverlordsTerminal(s)
			now := time.Now().UTC()
			question := Question{ID: "pipe-question", Identity: identity, Text: "Which store?", Options: []string{"SQLite", "Postgres"}, CreatedAt: now}
			req := runPipeRequest{Kind: test.kind, ID: "pipe-run", Title: "Run the proof", Shell: "powershell", Command: "Write-Output proof\n"}
			switch test.kind {
			case "":
				if test.refusal != "" {
					req.Shell = "unknown"
				}
			case "question":
				req.Question = &question
				if test.refusal != "" {
					question.Identity = "not-the-CFO"
				}
			case "review":
				req.Review = &Review{ID: "pipe-review", Identity: identity, Title: "Read the proof", State: "open", CreatedAt: now, UpdatedAt: now}
				if test.refusal != "" {
					req.Review.Identity = "not-the-CFO"
				}
			case "answer":
				if test.refusal == "" {
					if err := store.acceptQuestion(question); err != nil {
						t.Fatal(err)
					}
				}
				req.Answer = &cfoAnswer{QuestionID: question.ID, Option: "SQLite", Answer: "SQLite", At: now}
			case "credential":
				req.Credential = &CredentialRequest{ID: "cred-1234567890abcdef", Identity: identity, By: "cfo", Project: "throwaway", Names: []string{"DATABASE_URL"}, Why: "Run the proof", CreatedAt: now}
				if test.refusal != "" {
					req.Credential.Identity = "not-the-CFO"
				}
			case "withdraw-run":
				req.Reason = "Proof finished"
				if test.refusal == "" {
					readyRun(t, store, identity, req.ID, "powershell", false, now)
				}
			case "afk-on", "afk-off", "afk-log":
				if test.kind != "afk-on" {
					if _, _, err := afk.TurnOn(h.State, "test", nil, now); err != nil {
						t.Fatal(err)
					}
				}
				if test.kind == "afk-log" {
					req.AFK = &afk.Entry{Kind: afk.KindOther, What: "A test decision", Evidence: "The proof passed"}
					if test.refusal != "" {
						req.AFK.Evidence = ""
					}
				} else if test.refusal != "" {
					asGoblinsTerminal(s)
				}
			case "reported":
				go func() { close(<-s.looks) }()
			}
			client, _ := replyPipe(t, s, context.Background())
			data, err := json.Marshal(req)
			if err != nil {
				t.Fatal(err)
			}
			data = append(data, '\n')
			if test.kind == "invalid-json" {
				data = []byte("{\n")
			} else if test.kind == "incomplete" {
				data = []byte(strings.Repeat("x", maxRunRequest))
			}

			// Act
			if _, err := client.Write(data); err != nil {
				t.Fatal(err)
			}
			time.Sleep(200 * time.Millisecond)
			line, readErr := bufio.NewReader(client).ReadBytes('\n')

			// Assert
			var reply struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(line, &reply); readErr != nil || err != nil {
				t.Fatalf("delayed reply = %q, read=%v, decode=%v; want intact JSON", line, readErr, err)
			}
			if test.refusal == "" && reply.Error != "" || test.refusal != "" && !strings.Contains(reply.Error, test.refusal) {
				t.Fatalf("reply error = %q, want %q", reply.Error, test.refusal)
			}
		})
	}
}

func TestPipeReplyDeadlineReleasesASilentClientWithoutHoldingUpOtherCallers(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	s := &Service{Store: store}
	asGoblinsTerminal(s)
	silent, released := replyPipe(t, s, context.Background())
	started := time.Now()
	if _, err := silent.Write([]byte(`{"kind":"afk-off"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	select {
	case <-released:
		t.Fatal("the silent client's unread refusal was discarded before its deadline")
	default:
	}

	// Act
	other, finished := replyPipe(t, s, context.Background())
	if err := other.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Write([]byte(`{"kind":"afk-off"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(other).ReadBytes('\n')

	// Assert
	if err != nil || !strings.Contains(string(line), "a goblin's terminal") {
		t.Fatalf("another caller's reply = %q, %v; want its refusal without waiting for the silent client", line, err)
	}
	_ = other.Close()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("the caller that read its reply did not release its handler")
	}
	select {
	case <-released:
		if elapsed := time.Since(started); elapsed < runReplyTimeout {
			t.Fatalf("silent client released in %s, want its reply kept until the deadline", elapsed)
		}
	case <-time.After(runReplyTimeout + 5*time.Second):
		t.Fatal("the silent client's reply handler outlived its deadline")
	}
}

func TestPipeReplyTeardownStopsOnShutdownEvenWhileAReplyWriteWaits(t *testing.T) {
	for _, isLarge := range []bool{false, true} {
		t.Run(map[bool]string{false: "unread reply", true: "blocked reply write"}[isLarge], func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			_, _, _, cfo := primaryFixture(t, store)
			s := &Service{Store: store, Options: Options{CFO: cfo}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client, released := replyPipe(t, s, ctx)
			kind := "unknown"
			if isLarge {
				kind = strings.Repeat("x", 256<<10)
			}
			data, err := json.Marshal(runPipeRequest{Kind: kind})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Write(append(data, '\n')); err != nil {
				t.Fatal(err)
			}
			time.Sleep(200 * time.Millisecond)
			select {
			case <-released:
				t.Fatal("the handler released a reply its client has not read")
			default:
			}

			// Act
			cancel()

			// Assert
			select {
			case <-released:
			case <-time.After(2 * time.Second):
				t.Fatal("shutdown left the reply handler waiting on its client")
			}
		})
	}
}

func TestPipeReportClientReleasesItsHandlerWithoutReadingAReply(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	s := &Service{Store: store, looks: make(chan chan struct{})}
	client, released := replyPipe(t, s, context.Background())
	go func() { close(<-s.looks) }()

	// Act
	if _, err := client.Write([]byte(`{"kind":"reported"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	_ = client.Close()

	// Assert
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("the fire-and-forget report left its handler waiting for a reader")
	}
}

func TestAFKRefusalSurvivesAClientThatReadsAfterTheHandlerReplies(t *testing.T) {
	store, h := testStore(t)
	if _, _, err := afk.TurnOn(h.State, "test", nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	s := &Service{Store: store}
	asGoblinsTerminal(s)
	runPipe(t, s)
	var client *os.File
	var err error
	deadline := time.Now().Add(2 * time.Second)
	for {
		client, err = os.OpenFile(runPipeName(h.State), os.O_RDWR|syscall.FILE_FLAG_OVERLAPPED, 0)
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte(`{"kind":"afk-off"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	line, readErr := bufio.NewReader(client).ReadBytes('\n')
	if switched, err := afk.Read(h.State); err != nil || !switched.On {
		t.Fatalf("AFK state = %+v, %v; want still on", switched, err)
	}
	t.Log("AFK remains on after the refused request")
	var reply struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(line, &reply); readErr != nil || err != nil || !strings.Contains(reply.Error, "a goblin's terminal") {
		t.Fatalf("refusal after delayed read = %q, read=%v, decode=%v; want the goblin refusal", line, readErr, err)
	}
}
