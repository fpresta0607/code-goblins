package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

const nativeSendPasteComposer = "native-send-paste-composer"

type nativeSendPasteEvent struct {
	Kind    string    `json:"kind"`
	Text    string    `json:"text,omitempty"`
	PID     int       `json:"pid"`
	Start   time.Time `json:"start"`
	Session string    `json:"session"`
}

// This model-free console keeps the installed Codex paste contract: Enter in
// an active burst adds a newline, while End flushes the burst without editing
// its text. The consumer's pause lets separate terminal writes arrive together.
func runNativeSendPasteComposer(args []string) int {
	stateDir, mode, prior := args[0], args[1], args[2]
	start, ok := proc.StartTime(os.Getpid())
	if !ok {
		return 2
	}
	const session = "native-send-paste-session"
	record := func(kind, text string) {
		file, err := os.OpenFile(filepath.Join(stateDir, "paste-events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			os.Exit(2)
		}
		err = json.NewEncoder(file).Encode(nativeSendPasteEvent{Kind: kind, Text: text, PID: os.Getpid(), Start: start, Session: session})
		file.Close()
		if err != nil {
			os.Exit(2)
		}
	}
	meta, err := state.ReadTaskMeta(stateDir, "paste-task")
	if err != nil {
		return 2
	}
	for name, value := range map[string]string{"CFO_ROLE": "goblin", "CFO_TASK_ID": meta.ID, "CFO_SPAWN_GEN": meta.SpawnGen} {
		if err := os.Setenv(name, value); err != nil {
			return 2
		}
	}
	if err := windows.SetConsoleMode(windows.Handle(os.Stdin.Fd()), windows.ENABLE_VIRTUAL_TERMINAL_INPUT); err != nil {
		return 2
	}
	isBusy := mode != "idle"
	draw := func(text string) {
		status := "100% context left"
		if isBusy {
			status = "• Working (1s • esc to interrupt)"
		}
		fmt.Print("\x1b[2J\x1b[HPrevious reply: " + prior + "\r\n› " + text + "\r\n" + status)
	}
	draw("Ask Codex to do anything")
	record("ready", prior)
	consume := func(text string) {
		input, _ := json.Marshal(map[string]string{"hook_event_name": "UserPromptSubmit", "session_id": session, "cwd": stateDir})
		if exit := runNativeHook([]string{"codex", "--home", filepath.Dir(stateDir), "--state", stateDir}, bytes.NewReader(input), io.Discard, os.Stderr, commandRuntime{}); exit != 0 {
			os.Exit(2)
		}
		// Codex records what it takes in the rollout its session opened for
		// the folder it works in, as a user message.
		if err := appendCodexRollout(stateDir, session, text); err != nil {
			os.Exit(2)
		}
		record("consumed", text)
		draw("Ask a follow-up question")
	}
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
	var draft strings.Builder
	var lastCharacter time.Time
	var pending, endKey string
	hasReadInput := false
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			if pending != "" {
				if _, err := os.Stat(filepath.Join(stateDir, "release-turn")); err == nil {
					isBusy = false
					consume(pending)
					pending = ""
				}
			}
		case key, open := <-keys:
			if !open {
				return 0
			}
			if !hasReadInput {
				time.Sleep(600 * time.Millisecond)
				hasReadInput = true
			}
			if key == '\x1b' || endKey != "" {
				endKey += string(key)
				if endKey == "\x1b[F" {
					lastCharacter = time.Time{}
					record("paste-end", draft.String())
					endKey = ""
				} else if !strings.HasPrefix("\x1b[F", endKey) {
					record("error", "unexpected terminal key "+endKey)
					return 2
				}
				continue
			}
			if key == '\r' || key == '\n' {
				if time.Since(lastCharacter) <= 120*time.Millisecond {
					draft.WriteByte('\n')
					record("newline", draft.String())
				} else if text := strings.TrimSpace(draft.String()); text != "" {
					record("submitted", text)
					draft.Reset()
					if mode == "busy-late" {
						pending = text
					} else {
						consume(text)
					}
				}
			} else {
				draft.WriteRune(key)
				lastCharacter = time.Now()
			}
			draw(draft.String())
		}
	}
}

// appendCodexRollout writes what a Codex session working in dir records as
// it takes text: its rollout's opening, the first time, then the text as a
// user message, in this user's home.
func appendCodexRollout(dir, session, text string) error {
	profile, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	now := time.Now()
	path := filepath.Join(profile, ".codex", "sessions", now.Format("2006"), now.Format("01"), now.Format("02"), "rollout-"+now.Format("2006-01-02T15-04-05")+"-"+session+fmt.Sprint(os.Getpid())+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if info, err := file.Stat(); err == nil && info.Size() == 0 {
		if err := json.NewEncoder(file).Encode(map[string]any{"type": "session_meta", "payload": map[string]any{"id": session, "cwd": dir}}); err != nil {
			return err
		}
	}
	return json.NewEncoder(file).Encode(map[string]any{"timestamp": now.UTC().Format(time.RFC3339Nano), "type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": text}}}})
}

func TestNativeCLISendSubmitsDelayedMultilinePasteOnce(t *testing.T) {
	// Resolving a mistaken Herdr route must fail locally, never reach the fleet.
	t.Setenv("PATH", t.TempDir())
	// The fixture's rollouts go to a user home of the test's own.
	t.Setenv("USERPROFILE", t.TempDir())
	const text = "Keep the original decision.\nDo not replay it.\nPreserve this exact matching suffix for the next turn."
	for _, mode := range []string{"idle", "busy-accepted", "busy-late"} {
		for _, prior := range []string{"Preserve this exact matching suffix for the next turn.", "[Pasted Content #1]"} {
			t.Run(mode+"/"+prior, func(t *testing.T) {
				root := t.TempDir()
				stateDir := filepath.Join(root, "state")
				if err := os.MkdirAll(stateDir, 0o700); err != nil {
					t.Fatal(err)
				}
				h := home.Home{Root: root, State: stateDir, Data: filepath.Join(root, "data")}
				meta := state.TaskMeta{ID: "paste-task", Window: "native", Worktree: stateDir, Harness: "codex", Kind: "ship", Backend: "native", SpawnGen: "paste-generation"}
				if err := state.WriteTaskMeta(stateDir, meta); err != nil {
					t.Fatal(err)
				}
				program, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				ended := make(chan error, 1)
				go func() {
					ended <- host.Run(stateDir, host.Spec{ID: meta.ID, Args: []string{program, nativeSendPasteComposer, stateDir, mode, prior}, Dir: root, Cols: 120, Rows: 30})
				}()
				t.Cleanup(func() {
					if record, err := host.ReadRecord(stateDir, meta.ID); err == nil {
						if err := host.Close(stateDir, record, 10*time.Second); err != nil {
							t.Errorf("close fixture terminal: %v", err)
						}
					}
					select {
					case err := <-ended:
						if err != nil {
							t.Errorf("fixture host: %v", err)
						}
					case <-time.After(15 * time.Second):
						t.Error("fixture host did not end")
					}
				})
				awaitNativeSendPasteEvent(t, stateDir, "ready")
				record, err := host.ReadRecord(stateDir, meta.ID)
				if err != nil {
					t.Fatal(err)
				}
				runtime := defaultCommandRuntime()
				runtime.resolveHome = func() (home.Home, error) { return h, nil }
				var stdout, stderr bytes.Buffer
				since := time.Now()

				exit := runWithRuntime([]string{"send", "gb-" + meta.ID, text}, &stdout, &stderr, runtime)

				wantExit := 0
				if mode == "busy-late" {
					if !strings.HasPrefix(stdout.String(), "queued for gb-"+meta.ID+": ") || !strings.Contains(stdout.String(), "next tool call") || stderr.Len() != 0 {
						t.Errorf("pending output: stdout=%q stderr=%q", stdout.String(), stderr.String())
					}
					if taken, err := supervisor.NativePromptSince(stateDir, meta.ID, meta.SpawnGen, since); err != nil || taken {
						t.Errorf("before release, prompt taken=%v err=%v, want false", taken, err)
					}
				} else if stdout.String() != "sent gb-"+meta.ID+"\n" || stderr.Len() != 0 {
					t.Errorf("confirmed output: stdout=%q stderr=%q", stdout.String(), stderr.String())
				}
				if exit != wantExit {
					t.Errorf("send exit=%d, want %d", exit, wantExit)
				}
				events := readNativeSendPasteEvents(t, stateDir)
				var submitted []nativeSendPasteEvent
				for _, event := range events {
					if event.Kind == "submitted" {
						submitted = append(submitted, event)
					}
				}
				want := fleet.Stamp(text)
				if len(submitted) != 1 || submitted[0].Text != want {
					t.Fatalf("submissions=%+v, want exactly %q; events=%+v", submitted, want, events)
				}
				if mode == "busy-late" {
					if err := os.WriteFile(filepath.Join(stateDir, "release-turn"), nil, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				consumed := awaitNativeSendPasteEvent(t, stateDir, "consumed")
				if consumed.Text != want || consumed.PID != record.ChildPID || !consumed.Start.Equal(record.ChildStart) || consumed.Session != "native-send-paste-session" {
					t.Errorf("consumed=%+v, want full payload at native pid %d created %s", consumed, record.ChildPID, record.ChildStart)
				}
				if taken, err := supervisor.NativePromptSince(stateDir, meta.ID, meta.SpawnGen, since); err != nil || !taken {
					t.Errorf("after consumption, prompt taken=%v err=%v, want true", taken, err)
				}
				counts := make(map[string]int)
				for _, event := range readNativeSendPasteEvents(t, stateDir) {
					counts[event.Kind]++
				}
				if counts["submitted"] != 1 || counts["consumed"] != 1 || counts["error"] != 0 {
					t.Errorf("events by kind=%v, want one submission and consumption with no errors", counts)
				}
			})
		}
	}
}

func readNativeSendPasteEvents(t *testing.T, stateDir string) []nativeSendPasteEvent {
	t.Helper()
	file, err := os.Open(filepath.Join(stateDir, "paste-events.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var events []nativeSendPasteEvent
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event nativeSendPasteEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

func awaitNativeSendPasteEvent(t *testing.T, stateDir, kind string) nativeSendPasteEvent {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		for _, event := range readNativeSendPasteEvents(t, stateDir) {
			if event.Kind == kind {
				return event
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("fixture never recorded %s; events=%+v", kind, readNativeSendPasteEvents(t, stateDir))
		}
	}
}
