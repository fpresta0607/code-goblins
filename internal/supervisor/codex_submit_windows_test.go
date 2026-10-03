package supervisor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

type codexSubmitEvent struct {
	Kind    string    `json:"kind"`
	Text    string    `json:"text,omitempty"`
	PID     int       `json:"pid"`
	Start   time.Time `json:"start"`
	Session string    `json:"session"`
}

// The consumer-side pause keeps text and Enter in the same paste burst even
// though the host acknowledged both writes. Codex 0.160 treats Enter in that
// burst as a newline; its queue key flushes the paste before submitting.
func TestNativeCodexSubmitProgram(t *testing.T) {
	args := flag.Args()
	if len(args) != 5 || args[0] != "codex-submit-program" {
		return
	}
	stateDir, output, release, mode := args[1], args[2], args[3], args[4]
	start, alive := proc.StartTime(os.Getpid())
	if !alive {
		os.Exit(2)
	}
	const session = "codex-submit-session"
	record := func(kind, text string) {
		file, err := os.OpenFile(output, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			os.Exit(2)
		}
		data, err := json.Marshal(codexSubmitEvent{Kind: kind, Text: text, PID: os.Getpid(), Start: start, Session: session})
		if err != nil {
			os.Exit(2)
		}
		_, err = file.Write(append(data, '\n'))
		file.Close()
		if err != nil {
			os.Exit(2)
		}
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := host.ReadRecord(stateDir, os.Getenv(host.IDVariable)); err == nil {
			break
		}
		if time.Now().After(deadline) {
			record("error", "host never recorded the fixture")
			os.Exit(2)
		}
	}
	if _, err := Register(context.Background(), stateDir, nil, "codex", session); err != nil {
		record("error", err.Error())
		os.Exit(2)
	}
	if err := windows.SetConsoleMode(windows.Handle(os.Stdin.Fd()), windows.ENABLE_VIRTUAL_TERMINAL_INPUT); err != nil {
		record("error", err.Error())
		os.Exit(2)
	}
	input, _ := json.Marshal(map[string]string{"session_id": session, "cwd": stateDir, "hook_event_name": "SessionStart"})
	event, err := nativehook.Normalize(bytes.NewReader(input), nativehook.Context{Harness: "codex", HostID: os.Getenv(host.IDVariable)})
	if err == nil {
		err = nativehook.Spool(stateDir, event)
	}
	if err != nil {
		record("error", err.Error())
		os.Exit(2)
	}
	busy := mode == "busy"
	draw := func(text string) {
		status := "100% context left"
		if busy {
			status = "• Working (1s • esc to interrupt)"
		}
		fmt.Print("\x1b[2J\x1b[H› " + text + "\r\n" + status)
	}
	draw("Ask Codex to do anything")
	record("ready", mode)
	keys := make(chan rune)
	go func() {
		reader := bufio.NewReader(os.Stdin)
		for {
			key, _, err := reader.ReadRune()
			if err != nil {
				close(keys)
				return
			}
			keys <- key
		}
	}()
	consume := func(text string) {
		input, _ := json.Marshal(map[string]string{"session_id": session, "cwd": stateDir, "hook_event_name": "UserPromptSubmit"})
		event, err := nativehook.Normalize(bytes.NewReader(input), nativehook.Context{Harness: "codex", HostID: os.Getenv(host.IDVariable)})
		if err == nil {
			err = nativehook.Spool(stateDir, event)
		}
		if err != nil {
			record("error", err.Error())
		}
		record("consumed", text)
		draw("Ask a follow-up question")
	}
	var draft strings.Builder
	var lastCharacter time.Time
	var queued string
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			if queued != "" {
				if _, err := os.Stat(release); err == nil {
					busy = false
					consume(queued)
					queued = ""
				}
			}
		case key, open := <-keys:
			if !open {
				return
			}
			if lastCharacter.IsZero() {
				time.Sleep(600 * time.Millisecond)
			}
			switch key {
			case '\r':
				if time.Since(lastCharacter) <= 120*time.Millisecond {
					draft.WriteByte('\n')
					record("newline", draft.String())
					draw(draft.String())
					continue
				}
				fallthrough
			case '\t':
				record("submitted", draft.String())
				if busy {
					queued = draft.String()
					record("queued", queued)
					draw("Ask Codex to do anything")
				} else {
					consume(draft.String())
				}
				draft.Reset()
			default:
				draft.WriteRune(key)
				lastCharacter = time.Now()
				draw(draft.String())
			}
		}
	}
}

func codexSubmitEvents(t *testing.T, path string) []codexSubmitEvent {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var events []codexSubmitEvent
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var event codexSubmitEvent
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}

func TestACodexBoardDecisionSubmitsTheWholePasteOnceIdleAndBusy(t *testing.T) {
	for _, mode := range []string{"idle", "busy"} {
		t.Run(mode, func(t *testing.T) {
			store, _ := testStore(t)
			t.Setenv("HERDR_PANE_ID", "")
			program, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(t.TempDir(), "events.jsonl")
			release := filepath.Join(t.TempDir(), "turn-ended")
			terminal := hostProgram(t, store.Home.State, "cfo", output, program, "-test.run=^TestNativeCodexSubmitProgram$", "--", "codex-submit-program", store.Home.State, output, release, mode)
			for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
				if events := codexSubmitEvents(t, output); len(events) > 0 && events[0].Kind == "ready" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("fixture never became ready: %+v", codexSubmitEvents(t, output))
				}
			}
			identity := registrationIdentity(t, store.Home.State)
			if err := store.Ingest(); err != nil {
				t.Fatal(err)
			}
			question := Question{ID: "multiline-decision", Identity: identity, Text: "Choose the recovery approach.\nKeep the existing conversation.\nDo not replay its old answer.", Options: []string{"Fix native submission", "Hold it"}, CreatedAt: time.Now().UTC()}
			if err := store.acceptQuestion(question); err != nil {
				t.Fatal(err)
			}
			service := &Service{Store: store, Instance: "instance", Options: Options{CFO: &CFOConnection{State: store.Home.State}}}
			board := NewHTTP(service, "localhost", nil)
			action := Action{ID: "decision-1", Kind: "cfo_answer", Generation: identity, QuestionID: question.ID, Text: "Fix native submission\nPreserve the old evidence.", AnswerKind: "other"}
			post := func() {
				data, err := json.Marshal(map[string]string{"id": action.ID, "kind": action.Kind, "generation": action.Generation, "question_id": action.QuestionID, "text": action.Text, "answer_kind": action.AnswerKind})
				if err != nil {
					t.Fatal(err)
				}
				request := httptest.NewRequest(http.MethodPost, "http://localhost/api/actions", bytes.NewReader(data))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Origin", "http://localhost")
				request.Header.Set("X-CFO-Token", "instance")
				response := httptest.NewRecorder()
				board.ServeHTTP(response, request)
				if response.Code != http.StatusAccepted {
					t.Fatalf("board answer = %d %s", response.Code, response.Body.String())
				}
			}
			post()
			if err := store.ProcessOne(t.Context(), service.execute); err != nil {
				t.Fatal(err)
			}
			want := oneLine(fmt.Sprintf("Overlord: User answer to CFO question %s. Question: %s Answer (Other): %s", question.ID, question.Text, action.Text))
			events := codexSubmitEvents(t, output)
			var submitted []codexSubmitEvent
			for _, event := range events {
				if event.Kind == "submitted" {
					submitted = append(submitted, event)
				}
			}
			if len(submitted) != 1 || submitted[0].Text != want {
				t.Fatalf("actual submissions = %+v, want exactly the whole decision; terminal events = %+v; action = %+v", submitted, events, store.Snapshot().Actions)
			}
			if mode == "busy" {
				if actions := store.Snapshot().Actions; len(actions) != 1 || actions[0].Status != "running" || actions[0].Awaiting == nil {
					t.Fatalf("queued decision prematurely acknowledged: %+v", actions)
				}
				if err := os.WriteFile(release, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
				events = codexSubmitEvents(t, output)
				var consumed []codexSubmitEvent
				for _, event := range events {
					if event.Kind == "consumed" {
						consumed = append(consumed, event)
					}
				}
				if len(consumed) == 1 && consumed[0].Text == want {
					record, err := host.ReadRecord(terminal.stateDir, terminal.id)
					if err != nil || consumed[0].PID != record.ChildPID || !consumed[0].Start.Equal(submitted[0].Start) || consumed[0].Session != "codex-submit-session" {
						t.Fatalf("recipient changed: %+v, %+v, %v", consumed, record, err)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("exact recipient did not consume the decision: %+v", events)
				}
			}
			if err := store.Ingest(); err != nil {
				t.Fatal(err)
			}
			if err := store.settleDeliveries(time.Now(), service.lookAtTerminal); err != nil {
				t.Fatal(err)
			}
			if actions := store.Snapshot().Actions; len(actions) != 1 || actions[0].Status != "succeeded" || actions[0].Awaiting != nil {
				t.Fatalf("actual prompt consumption not acknowledged: %+v", actions)
			}
			post()
			if err := store.ProcessOne(t.Context(), service.execute); err != nil {
				t.Fatal(err)
			}
			for _, event := range codexSubmitEvents(t, output)[len(events):] {
				if event.Kind == "submitted" || event.Kind == "consumed" {
					t.Fatalf("duplicate decision replayed: %+v", event)
				}
			}
		})
	}
}
