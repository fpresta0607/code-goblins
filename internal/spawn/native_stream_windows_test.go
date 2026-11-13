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
			// the user's own, each where one exists: the user's in the
			// folder CLAUDE_CONFIG_DIR names, and in .claude in the user's
			// profile where it names none.
			memory := []map[string]any{}
			userFolder := os.Getenv("CLAUDE_CONFIG_DIR")
			if userFolder == "" {
				userHome, _ := os.UserHomeDir()
				userFolder = filepath.Join(userHome, ".claude")
			}
			for _, path := range []string{filepath.Join(mustGetwd(), "CLAUDE.md"), filepath.Join(userFolder, "CLAUDE.md")} {
				if _, err := os.Stat(path); err == nil {
					memory = append(memory, map[string]any{"path": path, "type": "Project"})
				}
			}
			answer(line.RequestID, map[string]any{"memoryFiles": memory, "skills": map[string]any{"totalSkills": 1, "includedSkills": 1}})
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

// Claude Code reads the user's CLAUDE.md in the folder CLAUDE_CONFIG_DIR
// names, where the goblin's environment names one, and in .claude in the
// user's profile otherwise, as install.UserSettingsPath finds the user's
// settings. A stream
// session's proof asks for that one file, so a stale one in the profile
// neither refuses a session that loaded the one Claude Code reads nor stands
// in for it.
func TestAStreamGoblinsProofAsksForTheUsersCLAUDEFileWhereClaudeCodeReadsIt(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		streamMode    string
		isFolderNamed bool
		wantStream    bool
	}{
		{name: "a session under a named folder", isFolderNamed: true, wantStream: true},
		{name: "a bare session under a named folder", streamMode: "bare", isFolderNamed: true},
		{name: "a bare session with no folder named", streamMode: "bare"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			profile := t.TempDir()
			t.Setenv("USERPROFILE", profile)
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			if err := os.MkdirAll(filepath.Join(profile, ".claude"), 0o755); err != nil {
				t.Fatal(err)
			}
			profileMemory := filepath.Join(profile, ".claude", "CLAUDE.md")
			writeFile(t, profileMemory, "a stale CLAUDE.md")
			folder := t.TempDir()
			folderMemory := filepath.Join(folder, "CLAUDE.md")
			writeFile(t, folderMemory, "the user's CLAUDE.md")
			f := newStreamFixture(t, testCase.streamMode)
			read, unread := profileMemory, folderMemory
			if testCase.isFolderNamed {
				f.userEnv = append(f.userEnv, "CLAUDE_CONFIG_DIR="+folder)
				read, unread = folderMemory, profileMemory
			}

			// Act
			_, err := f.service.Spawn(context.Background(), f.request)

			// Assert
			if err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			record, err := host.ReadRecord(f.stateDir, "task-7")
			if err != nil || record.Stream != testCase.wantStream {
				t.Fatalf("task-7's host = %+v, %v; want a stream session %v", record, err, testCase.wantStream)
			}
			lines := statusLines(t, f)
			if testCase.wantStream {
				if slices.ContainsFunc(lines, func(line string) bool { return strings.Contains(line, "rather than on stream-json pipes") }) {
					t.Errorf("status = %q, want no fallback", lines)
				}
				return
			}
			assertFallbackReported(t, f, "did not load "+read)
			if slices.ContainsFunc(lines, func(line string) bool { return strings.Contains(line, unread) }) {
				t.Errorf("status = %q, want %s, which Claude Code does not read, never asked for", lines, unread)
			}
		})
	}
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

// The terminal a stream session falls back to is the goblin's host from then
// on: one that cannot take the instruction either, as one whose composer
// holds a draft cannot, is kept running with its task and worktree, and named
// for goblins peek, as a start in a terminal alone keeps its terminal, never
// torn down as though the closed stream session were all the start ran.
func TestAStreamGoblinsFallbackTerminalThatCannotTakeItsInstructionIsKept(t *testing.T) {
	// Arrange
	f := newStreamFixture(t, "bare")
	f.userEnv = append(f.userEnv, fakeCodexDraft+"=An unfinished user draft")

	// Act
	_, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "holds a draft") || !strings.Contains(err.Error(), "goblins peek task-7") {
		t.Fatalf("Spawn = %v, want the fallback terminal's draft named and that terminal kept for goblins peek", err)
	}
	record, err := host.ReadRecord(f.stateDir, "task-7")
	if err != nil || record.Stream || !host.Running(record) {
		t.Fatalf("task-7's host = %+v, %v; want its fallback terminal still running", record, err)
	}
	if _, err := state.ReadTaskMeta(f.stateDir, "task-7"); err != nil || f.git.returned != 0 {
		t.Errorf("task record %v, worktree returned %d times; want the task and its worktree kept", err, f.git.returned)
	}
	if submitted := named(f.events(t), "submitted"); len(submitted) != 0 {
		t.Errorf("submitted = %+v, want nothing typed over the draft", submitted)
	}
	assertFallbackReported(t, f, "not signed in to a Claude plan")
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

// A switch of a goblin on stream-json pipes closes its stream session, which
// then ended closed rather than by itself, so the new harness starts on
// stream-json pipes again, proves itself anew and takes its handoff as one
// line, once. The exit command a terminal is typed never becomes a line.
func TestAStreamGoblinSwitchesOnStreamPipes(t *testing.T) {
	// Arrange
	f := newStreamFixture(t, "")
	f.service.Commands = cleanWorktree{f.service.Worktrees.Commands}
	f.service.Harness = harness.Registry{Adapters: map[harness.Kind]harness.Adapter{harness.Claude: nativeAdapter{kind: harness.Claude, control: harness.Control{StopCommand: "/exit"}}}}
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	first, err := host.ReadRecord(f.stateDir, "task-7")
	if err != nil || !first.Stream {
		t.Fatalf("task-7's host = %+v, %v; want a stream session", first, err)
	}

	// Act
	result, err := f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", Model: "opus"})

	// Assert
	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	end, err := host.ReadEnd(f.stateDir, "task-7")
	if err != nil || end.HostPID != first.HostPID || !end.Stream || !end.Closed {
		t.Errorf("task-7's last end = %+v, %v; want the first stream session, host pid %d, closed", end, err, first.HostPID)
	}
	second, err := host.ReadRecord(f.stateDir, "task-7")
	if err != nil || !second.Stream || second.HostPID == first.HostPID || !host.Running(second) {
		t.Errorf("task-7's host after the switch = %+v, %v; want a new running stream session", second, err)
	}
	submitted := submittedLines(t, f, 2)
	if len(submitted) != 2 || result.Handoff == "" || !strings.Contains(submitted[1], "Read the handoff at "+result.Handoff) {
		t.Errorf("submitted = %q with handoff %q; want the instruction, then the new session told its handoff once, and never /exit", submitted, result.Handoff)
	}
	if lines := statusLines(t, f); slices.ContainsFunc(lines, func(line string) bool { return strings.Contains(line, "rather than on stream-json pipes") }) {
		t.Errorf("status = %q, want no fallback", lines)
	}
	if closed := named(f.events(t), "input closed"); len(closed) != 1 {
		t.Errorf("input closed %d times, want once, the first session's", len(closed))
	}
}

// A switch proves its new stream session against the user's environment, as
// a spawn does, and falls back to a terminal started from that environment
// and the task's credentials: a Claude Code folder the user named since the
// spawn is the one asked for, never a stale one in the profile, and a session
// that starts bare leaves the handoff to a terminal that takes it once.
func TestAStreamGoblinsSwitchProvesAndFallsBackFromTheUsersEnvironment(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		isFolderNamed bool
		streamMode    string
		wantStream    bool
		wantReason    string
	}{
		{name: "a Claude Code folder named since the spawn", isFolderNamed: true, wantStream: true},
		{name: "a session that starts bare", streamMode: "bare", wantReason: "not signed in to a Claude plan"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			profile := t.TempDir()
			t.Setenv("USERPROFILE", profile)
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			if err := os.MkdirAll(filepath.Join(profile, ".claude"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(profile, ".claude", "CLAUDE.md"), "a stale CLAUDE.md")
			folder := t.TempDir()
			writeFile(t, filepath.Join(folder, "CLAUDE.md"), "the user's CLAUDE.md")
			f := newStreamFixture(t, "")
			f.service.Commands = cleanWorktree{f.service.Worktrees.Commands}
			f.service.Harness = harness.Registry{Adapters: map[harness.Kind]harness.Adapter{harness.Claude: nativeAdapter{kind: harness.Claude, control: harness.Control{StopCommand: "/exit"}}}}
			if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			if testCase.isFolderNamed {
				f.userEnv = append(f.userEnv, "CLAUDE_CONFIG_DIR="+folder)
			}
			if testCase.streamMode != "" {
				f.userEnv = append(f.userEnv, fakeStreamMode+"="+testCase.streamMode)
			}

			// Act
			result, err := f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", Model: "opus"})

			// Assert
			if err != nil {
				t.Fatalf("Switch: %v", err)
			}
			record, err := host.ReadRecord(f.stateDir, "task-7")
			if err != nil || record.Stream != testCase.wantStream || !host.Running(record) {
				t.Fatalf("task-7's host after the switch = %+v, %v; want a running host, a stream session %v", record, err, testCase.wantStream)
			}
			submitted := submittedLines(t, f, 2)
			if len(submitted) != 2 || result.Handoff == "" || !strings.Contains(delivered(t, submitted[1]), "Read the handoff at "+result.Handoff) {
				t.Errorf("submitted = %q with handoff %q; want the instruction, then the handoff taken once", submitted, result.Handoff)
			}
			if testCase.wantReason == "" {
				if lines := statusLines(t, f); slices.ContainsFunc(lines, func(line string) bool { return strings.Contains(line, "rather than on stream-json pipes") }) {
					t.Errorf("status = %q, want no fallback", lines)
				}
				return
			}
			assertFallbackReported(t, f, testCase.wantReason)
		})
	}
}
