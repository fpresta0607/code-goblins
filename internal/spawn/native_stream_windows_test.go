package spawn

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/claudestream"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// fakeStreamMode picks how the fake Claude Code on stream-json pipes starts
// and works: as a session on a plan sign-in with a SessionStart hook, skills
// and each CLAUDE.md there is by default, as --bare starts one ("bare"), with
// turns that run until an interrupt ends them ("long-turns"), or ending with
// exit code 3 at the first line it is written ("ends-at-a-line"). With
// "no-stream-host" the host of a stream session ends before it serves.
const fakeStreamMode = "SPAWN_TEST_STREAM_MODE"

// fakeStreamClaude answers stream-json lines as Claude Code 2.1.322 does: the
// start's initialize and get_context_usage with no turn, and each line it is
// written by its id, queued, started and completed, holding one written
// during a turn until that turn ends. It records each line it takes
// ("submitted"), each it holds ("queued"), each turn an interrupt ends
// ("interrupted") and the close of its input ("input closed").
func fakeStreamClaude(record func(codexEvent)) {
	mode := os.Getenv(fakeStreamMode)
	out := json.NewEncoder(os.Stdout)
	emit := func(line map[string]any) { _ = out.Encode(line) }
	lifecycle := func(id, phase string) {
		emit(map[string]any{"type": "command_lifecycle", "command_uuid": id, "state": phase})
	}
	answer := func(requestID string, response map[string]any) {
		emit(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": requestID, "response": response}})
	}
	type waiting struct{ id, text string }
	var turn string
	var queued []waiting
	end := func() {
		emit(map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": "Done.", "session_id": "s1"})
		lifecycle(turn, "completed")
		turn = ""
	}
	next := func() {
		for turn == "" && len(queued) > 0 {
			line := queued[0]
			queued = queued[1:]
			record(codexEvent{Event: "submitted", Text: line.text})
			emit(map[string]any{"type": "user", "uuid": line.id, "isReplay": true, "parent_tool_use_id": nil, "session_id": "s1", "message": map[string]any{"role": "user", "content": line.text}})
			lifecycle(line.id, "started")
			turn = line.id
			if mode != "long-turns" {
				end()
			}
		}
	}
	lines := bufio.NewScanner(os.Stdin)
	lines.Buffer(make([]byte, 1<<20), 1<<24)
	for lines.Scan() {
		var line struct {
			Type      string `json:"type"`
			ID        string `json:"uuid"`
			RequestID string `json:"request_id"`
			Message   struct {
				Content string `json:"content"`
			} `json:"message"`
			Request struct {
				Subtype string `json:"subtype"`
			} `json:"request"`
		}
		if json.Unmarshal(lines.Bytes(), &line) != nil {
			continue
		}
		switch {
		case line.Type == "user" && mode == "ends-at-a-line":
			os.Exit(3)
		case line.Type == "user":
			lifecycle(line.ID, "queued")
			if turn != "" {
				record(codexEvent{Event: "queued", Text: line.Message.Content})
			}
			queued = append(queued, waiting{line.ID, line.Message.Content})
			next()
		case line.Request.Subtype == "initialize" && mode == "bare":
			answer(line.RequestID, map[string]any{"claude_code_version": "2.1.322", "account": map[string]any{"tokenSource": "none"}})
		case line.Request.Subtype == "initialize":
			for _, subtype := range []string{"hook_started", "hook_response"} {
				emit(map[string]any{"type": "system", "subtype": subtype, "hook_event": "SessionStart", "hook_name": "SessionStart:startup"})
			}
			answer(line.RequestID, map[string]any{"claude_code_version": "2.1.322", "account": map[string]any{"subscriptionType": "Claude Max"}})
		case line.Request.Subtype == "get_context_usage" && mode == "bare":
			answer(line.RequestID, map[string]any{"memoryFiles": []any{}})
		case line.Request.Subtype == "get_context_usage":
			// Claude Code loads the CLAUDE.md of the folder it runs in and
			// the user's own, each where one exists.
			memory := []map[string]any{}
			userHome, _ := os.UserHomeDir()
			for _, path := range []string{filepath.Join(mustGetwd(), "CLAUDE.md"), filepath.Join(userHome, ".claude", "CLAUDE.md")} {
				if _, err := os.Stat(path); err == nil {
					memory = append(memory, map[string]any{"path": path, "type": "Project"})
				}
			}
			answer(line.RequestID, map[string]any{"memoryFiles": memory, "skills": map[string]any{"totalSkills": 1}})
		case line.Request.Subtype == "interrupt":
			answer(line.RequestID, map[string]any{})
			if turn != "" {
				record(codexEvent{Event: "interrupted"})
				end()
			}
			next()
		}
	}
	record(codexEvent{Event: "input closed"})
}

// newStreamFixture readies a native spawn of task-7 in a fake Claude Code
// that runs on stream-json pipes in streamMode, in a home that turns
// claude_goblins_on_stream on, and in its terminal at Claude Code's
// composer. Whatever host runs task-7 when the test ends is closed.
func newStreamFixture(t *testing.T, streamMode string) *nativeFixture {
	t.Helper()
	// The fake reads its mode from the environment its host gives it, which
	// a native fixture takes from this process as it is readied.
	t.Setenv(fakeStreamMode, streamMode)
	f := newNativeFixture(t, harness.Claude, "claude-ready")
	if err := os.MkdirAll(filepath.Join(f.service.HomeRoot, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.service.HomeRoot, "config", "fleet.json"), []byte(`{"claude_goblins_on_stream": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if running, err := host.ReadRecord(f.stateDir, "task-7"); err == nil {
			if err := host.Close(f.stateDir, running, nativeCloseWait); err != nil {
				t.Errorf("close task-7's host: %v", err)
			}
		}
	})
	return f
}

// statusLines are the lines of task-7's status log.
func statusLines(t *testing.T, f *nativeFixture) []string {
	t.Helper()
	lines, err := state.TailStatus(f.stateDir, "task-7", 50)
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

// assertFallbackReported checks that task-7's status log, and a notify that
// wakes the CEO, say its start runs in its terminal rather than on
// stream-json pipes, with each of reasons.
func assertFallbackReported(t *testing.T, f *nativeFixture, reasons ...string) {
	t.Helper()
	says := func(line string) bool {
		if !strings.Contains(line, "runs in its terminal rather than on stream-json pipes") {
			return false
		}
		for _, reason := range reasons {
			if !strings.Contains(line, reason) {
				return false
			}
		}
		return true
	}
	if lines := statusLines(t, f); !slices.ContainsFunc(lines, says) {
		t.Errorf("status does not say the start runs in its terminal because %q; status:\n%s", reasons, strings.Join(lines, "\n"))
	}
	pending, err := wake.Pending(f.stateDir)
	if err != nil || !slices.ContainsFunc(pending, func(record wake.Record) bool {
		return record.Kind == "notify" && record.Key == "task-7" && says(record.Detail)
	}) {
		t.Errorf("wake queue = %+v, %v; want a notify for task-7 saying it runs in its terminal because %q", pending, err, reasons)
	}
}

func TestAStreamGoblinProvesItsStartAndTakesItsWholeInstructionOnStreamPipes(t *testing.T) {
	// Arrange
	f := newStreamFixture(t, "")

	// Act
	_, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	record, err := host.ReadRecord(f.stateDir, "task-7")
	if err != nil || !record.Stream {
		t.Fatalf("task-7's host = %+v, %v; want a stream session", record, err)
	}
	submitted := named(f.events(t), "submitted")
	if len(submitted) != 1 || submitted[0].Text == "" || strings.Contains(submitted[0].Text, "instruction.md") {
		t.Errorf("submitted = %+v, want the whole instruction taken once as one line", submitted)
	}
	for _, line := range statusLines(t, f) {
		if strings.Contains(line, "rather than on stream-json pipes") {
			t.Errorf("status says %q, want no fallback", line)
		}
	}
}

func TestAStreamGoblinThatStartsBareRunsInItsTerminalAndSaysWhy(t *testing.T) {
	// Arrange
	f := newStreamFixture(t, "bare")

	// Act
	_, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	record, err := host.ReadRecord(f.stateDir, "task-7")
	if err != nil || record.Stream {
		t.Fatalf("task-7's host = %+v, %v; want its terminal", record, err)
	}
	if submitted := named(f.events(t), "submitted"); len(submitted) != 1 {
		t.Errorf("submitted = %+v, want the instruction taken once, in the terminal", submitted)
	}
	if closed := named(f.events(t), "input closed"); len(closed) != 1 {
		t.Errorf("the stream session's input closed %d times, want once, before the terminal started", len(closed))
	}
	assertFallbackReported(t, f, "not signed in to a Claude plan", "no SessionStart hook ran", "loaded no skills")
}

func TestAStartThatCannotRunOnStreamPipesRunsInItsTerminalAndWakesTheCEOWithWhy(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		streamMode string
		lastEnd    *host.End
		wantReason string
	}{
		{name: "after a stream session that ended by itself", lastEnd: &host.End{ID: "task-7", ChildPID: 42, Ended: time.Now().Add(-time.Hour).UTC(), Code: 3, Stream: true}, wantReason: "its last stream session ended by itself with exit code 3"},
		{name: "when its stream host cannot start", streamMode: "no-stream-host", wantReason: "start the stream session of task-7"},
		{name: "when Claude Code ends before it takes the instruction", streamMode: "ends-at-a-line", wantReason: "its stream session failed: spawn: Claude Code in stream session task-7 ended with exit code 3 before it took the line"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			f := newStreamFixture(t, testCase.streamMode)
			if testCase.lastEnd != nil {
				data, err := json.Marshal(testCase.lastEnd)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(f.stateDir, "hosts"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(f.stateDir, "hosts", "task-7.end"), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			_, err := f.service.Spawn(context.Background(), f.request)

			// Assert
			if err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			record, err := host.ReadRecord(f.stateDir, "task-7")
			if err != nil || record.Stream {
				t.Fatalf("task-7's host = %+v, %v; want its terminal", record, err)
			}
			if submitted := named(f.events(t), "submitted"); len(submitted) != 1 {
				t.Errorf("submitted = %+v, want the instruction taken once, in the terminal", submitted)
			}
			assertFallbackReported(t, f, testCase.wantReason)
		})
	}
}

func TestAStartGoesOnStreamPipesOnlyWhereTurnedOnAndNotAfterASessionThatEndedByItself(t *testing.T) {
	ended := time.Date(2026, 11, 12, 18, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		name         string
		kind         harness.Kind
		setting      string
		end          *host.End
		wantStream   bool
		wantFallback string
	}{
		{name: "a home that never set it", kind: harness.Claude},
		{name: "a home that turned it off", kind: harness.Claude, setting: `{"claude_goblins_on_stream": false}`},
		{name: "a Codex goblin", kind: harness.Codex, setting: `{"claude_goblins_on_stream": true}`},
		{name: "a first start", kind: harness.Claude, setting: `{"claude_goblins_on_stream": true}`, wantStream: true},
		{name: "after a terminal ended", kind: harness.Claude, setting: `{"claude_goblins_on_stream": true}`, end: &host.End{ID: "task-7", ChildPID: 42, Ended: ended, Code: 1}, wantStream: true},
		{name: "after the terminal a stream session's own end left it in was closed", kind: harness.Claude, setting: `{"claude_goblins_on_stream": true}`, end: &host.End{ID: "task-7", ChildPID: 42, Ended: ended, Code: 1, Closed: true}, wantStream: true},
		{name: "after a stream session was closed", kind: harness.Claude, setting: `{"claude_goblins_on_stream": true}`, end: &host.End{ID: "task-7", ChildPID: 42, Ended: ended, Code: 1, Closed: true, Stream: true}, wantStream: true},
		{name: "after a stream session ended by itself", kind: harness.Claude, setting: `{"claude_goblins_on_stream": true}`, end: &host.End{ID: "task-7", ChildPID: 42, Ended: ended, Code: 3, Stream: true}, wantFallback: "ended by itself with exit code 3 at 2026-11-12T18:00:00Z"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			service := Service{HomeRoot: root, StateDir: filepath.Join(root, "state")}
			if testCase.setting != "" {
				if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "config", "fleet.json"), []byte(testCase.setting), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if testCase.end != nil {
				data, err := json.Marshal(testCase.end)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(service.StateDir, "hosts"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(service.StateDir, "hosts", "task-7.end"), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			isStream, fallback, err := service.onStream("task-7", testCase.kind)

			// Assert
			if err != nil || isStream != testCase.wantStream || !strings.Contains(fallback, testCase.wantFallback) || (testCase.wantFallback == "") != (fallback == "") {
				t.Errorf("onStream = %v, %q, %v; want %v, %q", isStream, fallback, err, testCase.wantStream, testCase.wantFallback)
			}
		})
	}
}

func TestAStreamGoblinHoldsALineSentDuringItsTurnUntilAnInterruptEndsIt(t *testing.T) {
	// Arrange
	previous := nativeQueuedProof
	nativeQueuedProof = 500 * time.Millisecond
	t.Cleanup(func() { nativeQueuedProof = previous })
	f := newStreamFixture(t, "long-turns")
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	meta := state.TaskMeta{ID: "task-7", Harness: string(harness.Claude)}
	refusing := f.service
	refusing.ShouldRefuseTurn = true

	// Act
	refusedErr := refusing.SendNative(context.Background(), meta, "a wake line")
	queuedErr := f.service.SendNative(context.Background(), meta, "one more thing")
	enterErr := f.service.SendNativeKey(meta, "Enter")
	escapeErr := f.service.SendNativeKey(meta, "Escape")

	// Assert
	if !errors.Is(refusedErr, ErrInTurn) || !errors.Is(refusedErr, fleet.ErrNotTyped) {
		t.Errorf("a send that refuses a turn = %v, want nothing written into the turn", refusedErr)
	}
	if !errors.Is(queuedErr, fleet.ErrQueuedForToolCall) {
		t.Errorf("a send during the turn = %v, want it held for the turn's next step", queuedErr)
	}
	if enterErr == nil {
		t.Error("Enter was sent to a session with no composer")
	}
	if escapeErr != nil {
		t.Errorf("Escape: %v", escapeErr)
	}
	deadline := time.Now().Add(10 * time.Second)
	var submitted []codexEvent
	for time.Now().Before(deadline) {
		if submitted = named(f.events(t), "submitted"); len(submitted) == 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(submitted) != 2 || submitted[1].Text != "one more thing" {
		t.Errorf("submitted = %+v, want the held line taken once the interrupt ended the turn", submitted)
	}
	if queued := named(f.events(t), "queued"); len(queued) != 1 || queued[0].Text != "one more thing" {
		t.Errorf("queued = %+v, want only the line sent during the turn, and never the refused one", queued)
	}
	if interrupted := named(f.events(t), "interrupted"); len(interrupted) != 1 {
		t.Errorf("interrupted = %+v, want one turn ended by Escape", interrupted)
	}
}

func TestAStreamDeliveryReadsItsSessionAgainWhileAReadFailsForAMoment(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		failures int
		wantErr  string
	}{
		{name: "reads that fail for a moment", failures: 3},
		{name: "reads that fail past the grace", failures: math.MaxInt32, wantErr: "whether Claude Code took it cannot be read"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			previous := nativeReadGrace
			nativeReadGrace = time.Second
			t.Cleanup(func() { nativeReadGrace = previous })
			f := newStreamFixture(t, "")
			if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			reads := 0
			sending := f.service
			sending.ReadStreamState = func(record host.Record) (claudestream.Session, error) {
				reads++
				// The first read comes before the line is written.
				if reads > 1 && reads <= 1+testCase.failures {
					return claudestream.Session{}, errors.New("the host's pipe is busy")
				}
				return host.ReadStreamState(record)
			}

			// Act
			err := sending.SendNative(context.Background(), state.TaskMeta{ID: "task-7", Harness: string(harness.Claude)}, "one more thing")

			// Assert
			if testCase.wantErr == "" && err != nil {
				t.Errorf("SendNative = %v, want the line proved taken once a read succeeds", err)
			}
			if testCase.wantErr != "" && (err == nil || !strings.Contains(err.Error(), testCase.wantErr)) {
				t.Errorf("SendNative = %v, want %q", err, testCase.wantErr)
			}
			deadline := time.Now().Add(10 * time.Second)
			var submitted []codexEvent
			for time.Now().Before(deadline) {
				if submitted = named(f.events(t), "submitted"); len(submitted) >= 2 {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if len(submitted) != 2 || submitted[1].Text != "one more thing" {
				t.Errorf("submitted = %+v, want the line written once and never again", submitted)
			}
		})
	}
}

func TestStoppingAStreamGoblinClosesItsSessionSoItsNextStartGoesOnStreamPipesAgain(t *testing.T) {
	// Arrange
	f := newStreamFixture(t, "")
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Act
	err := f.service.stopNative(context.Background(), "task-7", harness.Control{StopCommand: "/exit"})

	// Assert
	if err != nil {
		t.Fatalf("stopNative: %v", err)
	}
	if nativeTerminalRuns(f.stateDir, "task-7") {
		t.Error("task-7's stream session still runs")
	}
	end, err := host.ReadEnd(f.stateDir, "task-7")
	if err != nil || !end.Stream || !end.Closed {
		t.Errorf("task-7's end = %+v, %v; want a stream session that was closed", end, err)
	}
	if closed := named(f.events(t), "input closed"); len(closed) != 1 {
		t.Errorf("input closed %d times, want once", len(closed))
	}
	if submitted := named(f.events(t), "submitted"); len(submitted) != 1 {
		t.Errorf("submitted = %+v, want the instruction alone, and never /exit typed as a line", submitted)
	}
	if isStream, fallback, err := f.service.onStream("task-7", harness.Claude); err != nil || !isStream {
		t.Errorf("the next start: onStream = %v, %q, %v; want stream pipes again", isStream, fallback, err)
	}
}
