package claudestream

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// userMemory is the user's CLAUDE.md as testdata/start.jsonl names it.
const userMemory = `C:\Users\owner\.claude\CLAUDE.md`

// replayStart reads testdata/<name>: the lines Claude Code 2.1.322 wrote to
// an initialize and a get_context_usage request, in a session started with
// Arguments and, for start-bare.jsonl, --bare, recorded on 2026-11-12 with
// the account and its paths taken out. It returns ProveStart's write and next
// over them and the lines written.
func replayStart(t *testing.T, name string) (func(json.RawMessage) error, func() (json.RawMessage, error), *[]string) {
	t.Helper()
	file, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	var out []json.RawMessage
	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		var line fixtureLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		if line.Direction == "out" {
			out = append(out, line.Line)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	var written []string
	write := func(line json.RawMessage) error {
		written = append(written, string(line))
		return nil
	}
	next := func() (json.RawMessage, error) {
		if len(out) == 0 {
			return nil, io.EOF
		}
		line := out[0]
		out = out[1:]
		return line, nil
	}
	return write, next, &written
}

func TestProveStartPassesASessionWithHisSignInHooksSkillsAndMemory(t *testing.T) {
	// Arrange
	write, next, written := replayStart(t, "start.jsonl")

	// Act
	start, err := ProveStart(write, next, []string{userMemory})

	// Assert
	if err != nil {
		t.Fatalf("ProveStart = %v, want a session a goblin runs on", err)
	}
	if start.Version != "2.1.322" || start.Subscription != "Claude Max" || start.SessionStartHooks != 2 || !start.HasSkills {
		t.Errorf("ProveStart read %+v", start)
	}
	if len(*written) != 2 || !strings.Contains((*written)[0], `"initialize"`) || !strings.Contains((*written)[1], `"get_context_usage"`) {
		t.Errorf("ProveStart wrote %q, want initialize and then get_context_usage", *written)
	}
}

// A bare session is what every -p session becomes once Claude Code makes
// --bare its default, as its docs say it will: ProveStart refuses it with
// each thing it went without.
func TestProveStartRefusesABareSession(t *testing.T) {
	// Arrange
	write, next, _ := replayStart(t, "start-bare.jsonl")

	// Act
	_, err := ProveStart(write, next, []string{userMemory})

	// Assert
	if err == nil {
		t.Fatal("ProveStart passed a bare session")
	}
	for _, want := range []string{"2.1.322", "not signed in to a Claude plan", "no SessionStart hook ran", "no skills", "did not load " + userMemory} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ProveStart = %v, want it to say %q", err, want)
		}
	}
}

// Claude Code's answer to get_context_usage counts the skills it gave the
// session in its skills member, which it writes either way: a session it gave
// none loaded none.
func TestProveStartRefusesASessionGivenNoSkills(t *testing.T) {
	for _, testCase := range []struct{ name, counts string }{
		{"none found", `"totalSkills":0,"includedSkills":0`},
		{"none of those found included", `"totalSkills":129,"includedSkills":0`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			write, next, _ := replayStart(t, "start.jsonl")
			counted := func() (json.RawMessage, error) {
				line, err := next()
				return json.RawMessage(strings.Replace(string(line), `"totalSkills":129,"includedSkills":129`, testCase.counts, 1)), err
			}

			// Act
			start, err := ProveStart(write, counted, []string{userMemory})

			// Assert
			if err == nil || !strings.Contains(err.Error(), "loaded no skills") || start.HasSkills {
				t.Errorf("ProveStart = %+v, %v; want it refused for loading no skills", start, err)
			}
		})
	}
}

// A goblin hook that cannot run leaves the supervisor knowing nothing of the
// session, while the person's own SessionStart hooks run beside it, so one
// hook's failure refuses the start however many others succeeded. Claude
// Code 2.1.322 reports a hook it could not start, and one that exited with an
// error, as these do.
func TestProveStartRefusesASessionWhoseSessionStartHookFailed(t *testing.T) {
	for _, testCase := range []struct{ name, failure, want string }{
		{"its program is missing", `"stderr":"Error occurred while executing hook command: ENOENT: no such file or directory, uv_spawn 'goblins.exe'","exit_code":1,"outcome":"error"`, "a SessionStart hook failed: error Error occurred while executing hook command: ENOENT"},
		{"it exits with an error", `"stderr":"","exit_code":3,"outcome":"error"`, "a SessionStart hook failed: error"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			write, next, _ := replayStart(t, "start.jsonl")
			isFailed := false
			failing := func() (json.RawMessage, error) {
				line, err := next()
				if text := string(line); !isFailed && strings.Contains(text, `"hook_event":"SessionStart"`) && strings.Contains(text, `"subtype":"hook_response"`) {
					isFailed = true
					line = json.RawMessage(strings.Replace(text, `"stderr":"","exit_code":0,"outcome":"success"`, testCase.failure, 1))
				}
				return line, err
			}

			// Act
			start, err := ProveStart(write, failing, []string{userMemory})

			// Assert
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("ProveStart = %v, want it refused saying %q", err, testCase.want)
			}
			if start.SessionStartHooks != 1 || len(start.FailedHooks) != 1 {
				t.Errorf("ProveStart read %d hooks that succeeded and %q that failed, want 1 and 1", start.SessionStartHooks, start.FailedHooks)
			}
		})
	}
}

func TestProveStartRefusesASessionMissingAnExpectedCLAUDEFile(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		expected []string
		isPassed bool
	}{
		{"the loaded one in another case", []string{strings.ToUpper(userMemory)}, true},
		{"none expected", nil, true},
		{"the worktree's, not loaded", []string{userMemory, `D:\worktrees\task\CLAUDE.md`}, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			write, next, _ := replayStart(t, "start.jsonl")

			// Act
			_, err := ProveStart(write, next, testCase.expected)

			// Assert
			if (err == nil) != testCase.isPassed {
				t.Errorf("ProveStart(%q) = %v, want passed %v", testCase.expected, err, testCase.isPassed)
			}
		})
	}
}

func TestProveStartFailsASessionThatEndsOrRefusesBeforeItAnswers(t *testing.T) {
	refusal := json.RawMessage(`{"type":"control_response","response":{"subtype":"error","request_id":"goblins-initialize","error":"Unknown request"}}`)
	for _, testCase := range []struct {
		name  string
		lines []json.RawMessage
		want  string
	}{
		{"ended", nil, "read the session's answer to initialize"},
		{"refused", []json.RawMessage{refusal}, "Unknown request"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			lines := testCase.lines
			next := func() (json.RawMessage, error) {
				if len(lines) == 0 {
					return nil, io.EOF
				}
				line := lines[0]
				lines = lines[1:]
				return line, nil
			}

			// Act
			_, err := ProveStart(func(json.RawMessage) error { return nil }, next, nil)

			// Assert
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("ProveStart = %v, want it to say %q", err, testCase.want)
			}
		})
	}
}

func TestProveStartFailsWhenItCannotAsk(t *testing.T) {
	// Arrange
	refused := errors.New("the pipe is closed")

	// Act
	_, err := ProveStart(func(json.RawMessage) error { return refused }, nil, nil)

	// Assert
	if !errors.Is(err, refused) {
		t.Errorf("ProveStart = %v, want %v", err, refused)
	}
}
