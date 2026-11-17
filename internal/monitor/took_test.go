package monitor

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const steer = "CEO: stop the steps now and run no more of them. Reply with exactly the word STEER461700 and end your turn."

// The entries below are the shapes each harness wrote live on 2026-10-06,
// when a steer typed during a tool call reached a goblin at its next one.
func claudeQueued(at string, prompt any) map[string]any {
	return map[string]any{"type": "attachment", "timestamp": at, "attachment": map[string]any{"type": "queued_command", "prompt": prompt, "commandMode": "prompt", "origin": map[string]any{"kind": "human"}}}
}

func claudeEnqueued(at, text string) map[string]any {
	return map[string]any{"type": "queue-operation", "operation": "enqueue", "timestamp": at, "content": text}
}

func claudePrompt(at string, content any) map[string]any {
	return map[string]any{"type": "user", "timestamp": at, "message": map[string]any{"role": "user", "content": content}}
}

func codexMessage(at, role, text string) map[string]any {
	return map[string]any{"timestamp": at, "type": "response_item", "payload": map[string]any{"type": "message", "role": role, "content": []map[string]any{{"type": "input_text", "text": text}}}}
}

func piMessage(at, role, text string) map[string]any {
	return map[string]any{"type": "message", "timestamp": at, "message": map[string]any{"role": role, "content": []map[string]any{{"type": "text", "text": text}}}}
}

// A goblin has a steer only once its harness's own record shows the steer
// handed to its model after it was sent: Claude Code's queued_command
// attachment or a prompt, Codex's user message, pi's user message. The
// record of the steer waiting in the harness's queue, a tool's result or a
// subagent that holds the same words, the agent quoting it, and an earlier
// copy of it are not.
func TestAGoblinHasASteerOnlyOnceItsHarnessHandedItToTheModel(t *testing.T) {
	sent := time.Date(2026, 10, 6, 21, 51, 45, 0, time.UTC)
	after, before := "2026-10-06T21:51:59.044Z", "2026-10-06T21:40:00Z"
	sidechain := claudePrompt(after, steer)
	sidechain["isSidechain"] = true
	for _, test := range []struct {
		name    string
		harness string
		entries []map[string]any
		want    bool
	}{
		{"Claude Code took it into the running turn", "claude", []map[string]any{claudeEnqueued("2026-10-06T21:51:46.106Z", steer), claudeQueued("2026-10-06T21:51:46.106Z", steer)}, true},
		{"Claude Code started a turn with it", "claude", []map[string]any{claudePrompt(after, steer)}, true},
		{"Claude Code started a turn with it as text blocks", "claude", []map[string]any{claudePrompt(after, []map[string]any{{"type": "text", "text": steer}})}, true},
		{"Claude Code holds it in its queue", "claude", []map[string]any{claudeEnqueued("2026-10-06T21:51:46.106Z", steer)}, false},
		{"a tool's result holds its words", "claude", []map[string]any{claudePrompt(after, []map[string]any{{"type": "tool_result", "content": steer}})}, false},
		{"a subagent was given its words", "claude", []map[string]any{sidechain}, false},
		{"the agent quoted it", "claude", []map[string]any{{"type": "assistant", "timestamp": after, "message": map[string]any{"role": "assistant", "content": claudeText(steer)}}}, false},
		{"an earlier copy was taken before it was sent", "claude", []map[string]any{claudeQueued(before, steer)}, false},
		{"Claude Code took it wrapped across lines", "claude", []map[string]any{claudeQueued(after, strings.Replace(steer, " Reply", "\n  Reply", 1))}, true},
		// Claude Code 2.1.292 kept a 1,136-character answer typed during a
		// turn this way, live on 2026-10-06.
		{"Claude Code took it as a paste", "claude", []map[string]any{claudeQueued(after, "<pasted_content id=\"072c\">\n"+steer+"\n</pasted_content id=\"072c\">")}, true},
		{"Codex took it into the running turn", "codex", []map[string]any{codexMessage("2026-10-06T21:54:55.560Z", "user", steer)}, true},
		{"Codex's agent said it", "codex", []map[string]any{codexMessage(after, "assistant", steer)}, false},
		{"a Codex tool's output holds its words", "codex", []map[string]any{{"timestamp": after, "type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "output": steer}}}, false},
		{"pi took it", "pi", []map[string]any{piMessage(after, "user", steer)}, true},
		{"pi's agent said it", "pi", []map[string]any{piMessage(after, "assistant", steer)}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			home, worktree := t.TempDir(), t.TempDir()
			writeConversation(t, home, test.harness, worktree, test.entries...)

			// Act
			got := (&HostProgress{Home: home}).Took(context.Background(), Conversation{Harness: test.harness, Dir: worktree}, TextDigest(steer), sent)

			// Assert
			if got != test.want {
				t.Errorf("Took = %v, want %v", got, test.want)
			}
		})
	}
}

// The Overlord, 2026-11-12: "thes already sent in termianl but are shwoing
// queued in mdoern caht fix root synch" (#1915). Two messages he pasted for
// the CEO each held three tabs and a word joiner, U+2060, which draws
// nothing. Claude Code 2.1.322 made each tab four spaces and left the word
// joiner out as it took them, live, so its record never held the words the
// board typed character for character, and both read Queued for good. What a
// harness took is the words a reader sees: a character that draws nothing is
// no part of them, and one a reader does see still is.
func TestTextItsHarnessClearedOfWhatDrawsNothingIsStillTaken(t *testing.T) {
	sent := time.Date(2026, 11, 12, 17, 44, 36, 0, time.UTC)
	after := "2026-11-12T17:44:36.624Z"
	recorded := "Level    error: the search for project=fa1 failed"
	for _, tc := range []struct {
		name  string
		typed string
		entry map[string]any
		want  bool
	}{
		{name: "a word joiner, taken into the running turn", typed: "Level\terror: the search for project=f\u2060a1 failed", entry: claudeQueued(after, recorded), want: true},
		{name: "a word joiner, taken as the next turn", typed: "Level\terror: the search for project=f\u2060a1 failed", entry: claudePrompt(after, recorded), want: true},
		{name: "a zero-width space", typed: "Level\terror: the search for\u200b project=fa1 failed", entry: claudeQueued(after, recorded), want: true},
		{name: "a byte order mark", typed: "\ufeffLevel\terror: the search for project=fa1 failed", entry: claudeQueued(after, recorded), want: true},
		{name: "a character that draws nothing between two spaces", typed: "Level\terror: the search for \u2060 project=fa1 failed", entry: claudeQueued(after, recorded), want: true},
		{name: "another letter", typed: "Level\terror: the search for project=f\u2060a1 failed", entry: claudeQueued(after, strings.Replace(recorded, "=fa1", "=fb1", 1)), want: false},
		{name: "words run together", typed: "Level\terror: the search for project=f\u2060a1 failed", entry: claudeQueued(after, strings.Replace(recorded, "search for", "searchfor", 1)), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			home, worktree := t.TempDir(), t.TempDir()
			transcriptLines(t, filepath.Join(claudeProjectFolder(home, worktree), "conversation.jsonl"), time.Now(), tc.entry)

			// Act
			took := (&HostProgress{Home: home}).Took(context.Background(), Conversation{Harness: "claude", Dir: worktree}, TextDigest(tc.typed), sent)

			// Assert
			if took != tc.want {
				t.Errorf("Took = %t, want %t", took, tc.want)
			}
		})
	}
}

// The Overlord, 2026-11-13: "img egettign latency delay and synch isseus in
// modern terminal" (#2541). A message of his with a picture, typed for the CEO
// during a turn, read Queued for hours after the CEO's session took it.
// Claude Code 2.1.323 hands its model the picture a path in typed text names:
// a line it took into a running turn had a list of a text block and the
// picture for its prompt, with [Image #50] right after the path, and a prompt
// that started a turn held [Image #3] where the path was, live on 2026-11-13
// and 2026-11-12. The board read a queued line's prompt only as one text, so
// it skipped that line. Text taken is his words with each mark left out, the
// mark a word of his own he typed is still his, and other words are not. The
// board names a message by its words without the picture's path too
// (internal/supervisor/awaiting.go), which the next turn's case types.
func TestAMessageWithAPictureItsHarnessTookIsTaken(t *testing.T) {
	sent := time.Date(2026, 11, 13, 18, 11, 55, 0, time.UTC)
	after := "2026-11-13T18:12:13.645Z"
	words := "The list draws this row twice, see the picture"
	first, second := `C:\goblins\state\composer-files\pasted-4f2a91c7.png`, `C:\goblins\state\composer-files\pasted-0b8e3d52.jpg`
	picture := map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "iVBORw0KGgo="}}
	text := func(text string) map[string]any { return map[string]any{"type": "text", "text": text} }
	for _, tc := range []struct {
		name  string
		typed string
		entry map[string]any
		want  bool
	}{
		{name: "taken into the running turn", typed: words + " " + first, entry: claudeQueued(after, []map[string]any{text(words + " " + first + "[Image #50]"), picture}), want: true},
		{name: "two taken into the running turn", typed: words + " " + first + " " + second, entry: claudeQueued(after, []map[string]any{text(words + " " + first + "[Image #50] " + second + "[Image #51]"), picture, picture}), want: true},
		{name: "taken as the next turn", typed: words, entry: claudePrompt(after, []map[string]any{text(words + " [Image #3]"), picture}), want: true},
		{name: "a mark he typed himself", typed: words + " [Image #3]", entry: claudeQueued(after, words+" [Image #3]"), want: true},
		{name: "other words with a picture", typed: words + " " + first, entry: claudeQueued(after, []map[string]any{text("Another row " + first + "[Image #50]"), picture}), want: false},
		{name: "a picture alone", typed: words + " " + first, entry: claudeQueued(after, []map[string]any{picture}), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			home, worktree := t.TempDir(), t.TempDir()
			transcriptLines(t, filepath.Join(claudeProjectFolder(home, worktree), "conversation.jsonl"), time.Now(), tc.entry)

			// Act
			took := (&HostProgress{Home: home}).Took(context.Background(), Conversation{Harness: "claude", Dir: worktree}, TextDigest(tc.typed), sent)

			// Assert
			if took != tc.want {
				t.Errorf("Took = %t, want %t", took, tc.want)
			}
		})
	}
}

// sized is an entry that takes exactly size bytes of a record, its line break
// counted: words of the turn stamped at, or with no at a request Claude Code
// 2.1.322 keeps with no time on it.
func sized(t *testing.T, at string, size int) map[string]any {
	t.Helper()
	entry := map[string]any{"type": "api-request-blob", "body": ""}
	if at != "" {
		entry = map[string]any{"type": "assistant", "timestamp": at, "body": ""}
	}
	line, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	entry["body"] = strings.Repeat("x", size-len(line)-1)
	return entry
}

// The Overlord, 2026-11-12 (#1915): four messages of his read Queued or Not
// confirmed for hours after the CEO's session took them. Its record had grown
// 10 MiB since it took two of them and 40 MiB since it took the other two,
// and the look read only the record's last 4 MiB. A take is found however
// much its harness wrote after it: the later entries of its turn, requests
// kept with no time on them, or so much that the take's own entry begins in
// one read of the record and ends in the next. A copy taken before the
// message was sent is still none.
func TestATakeIsFoundHoweverMuchItsHarnessWroteAfterIt(t *testing.T) {
	sent := time.Date(2026, 11, 12, 12, 33, 54, 0, time.UTC)
	before, after, later := "2026-11-12T12:20:00Z", "2026-11-12T12:34:10Z", "2026-11-12T13:00:00Z"
	for _, tc := range []struct {
		name    string
		tookAt  string
		stamp   string
		entries int
		size    int
		want    bool
	}{
		{name: "the later entries of its turn", tookAt: after, stamp: later, entries: 40, size: 256 << 10, want: true},
		{name: "requests kept with no time on them", tookAt: after, entries: 8, size: 1280 << 10, want: true},
		{name: "its own entry cut by the edge of one read", tookAt: after, stamp: later, entries: 1, size: transcriptEntryReach - 100, want: true},
		{name: "a copy taken before it was sent", tookAt: before, stamp: later, entries: 40, size: 256 << 10, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			home, worktree := t.TempDir(), t.TempDir()
			entries := []map[string]any{claudeQueued(tc.tookAt, steer)}
			for range tc.entries {
				entries = append(entries, sized(t, tc.stamp, tc.size))
			}
			transcriptLines(t, filepath.Join(claudeProjectFolder(home, worktree), "conversation.jsonl"), time.Now(), entries...)

			// Act
			took := (&HostProgress{Home: home}).Took(context.Background(), Conversation{Harness: "claude", Dir: worktree}, TextDigest(steer), sent)

			// Assert
			if took != tc.want {
				t.Errorf("Took = %t, want %t", took, tc.want)
			}
		})
	}
}

// A delivery is watched for a day and its record gains megabytes an hour, so
// a look that has searched a record back to the submit reads at each later
// one only what the record gained: whole entries, so one its harness was
// still writing at a look is read at the next, and nothing of the part
// searched before, which a harness never writes again.
func TestALaterLookReadsOnlyWhatItsRecordGained(t *testing.T) {
	sent := time.Date(2026, 11, 12, 17, 44, 36, 0, time.UTC)
	after := "2026-11-12T17:44:40Z"
	line := func(entry map[string]any) string {
		data, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		return string(data) + "\n"
	}
	queued, taken, other := line(claudeEnqueued(after, steer)), line(claudeQueued(after, steer)), line(claudeQueued(after, strings.ToUpper(steer)))
	for _, tc := range []struct {
		name   string
		first  string
		gained string
		later  string
		want   bool
	}{
		{name: "an entry written since", first: queued, gained: taken, want: true},
		{name: "an entry it was still writing", first: queued + taken[:len(taken)/2], gained: taken[len(taken)/2:], want: true},
		{name: "nothing, where only the part searched before holds the words", first: other, later: taken, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			home, worktree := t.TempDir(), t.TempDir()
			path := filepath.Join(claudeProjectFolder(home, worktree), "conversation.jsonl")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.first), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.later != "" && len(tc.later) != len(tc.first) {
				t.Fatalf("the record written again is %d bytes, was %d; want it no longer", len(tc.later), len(tc.first))
			}
			progress := &HostProgress{Home: home}
			look := func() bool {
				return progress.Took(context.Background(), Conversation{Harness: "claude", Dir: worktree}, TextDigest(steer), sent)
			}

			// Act
			first := look()
			if err := os.WriteFile(path, []byte(cmp.Or(tc.later, tc.first+tc.gained)), 0o600); err != nil {
				t.Fatal(err)
			}
			second := look()

			// Assert
			if first || second != tc.want {
				t.Errorf("Took = %t, then %t; want false, then %t", first, second, tc.want)
			}
		})
	}
}

// Claude Code cuts a folder name past 200 characters and adds a hash of the
// path: a goblin's worktree under a long scratch path was kept here, live on
// 2026-10-06, and its record must be found there.
func TestClaudeCodesFolderForALongWorktreeIsTheOneItMade(t *testing.T) {
	for _, test := range []struct{ dir, want string }{
		{`C:\dev\code-goblins\.worktrees\gb-cg-send-lands-now`, "C--dev-code-goblins--worktrees-gb-cg-send-lands-now"},
		{`C:\Users\fpres\AppData\Local\Temp\claude\C--dev-code-goblins--worktrees-gb-cg-send-lands-now\a56e84f9-16d1-47cc-a093-4d4164fc59a0\scratchpad\steer-proof\claude-goblin\home\worktrees\project\steer-claude`,
			"C--Users-fpres-AppData-Local-Temp-claude-C--dev-code-goblins--worktrees-gb-cg-send-lands-now-a56e84f9-16d1-47cc-a093-4d4164fc59a0-scratchpad-steer-proof-claude-goblin-home-worktrees-project-steer-clau-xrudd0"},
	} {
		if got := claudeFolder(test.dir); got != test.want {
			t.Errorf("claudeFolder(%q) = %q, want %q", test.dir, got, test.want)
		}
	}
}

// Every record kept for a goblin's folder is searched: a newer conversation
// in the same folder, such as one the goblin started itself, does not hide
// the steer its own conversation took. A Codex message handing over several
// texts at once holds each of them.
func TestASteerIsFoundInEveryRecordAndEveryTextHandedOver(t *testing.T) {
	sent := time.Date(2026, 10, 6, 21, 51, 45, 0, time.UTC)
	t.Run("an older conversation in the folder", func(t *testing.T) {
		home, worktree := t.TempDir(), t.TempDir()
		folder := filepath.Join(home, ".claude", "projects", claudeFolder(worktree))
		transcriptLines(t, filepath.Join(folder, "goblin.jsonl"), time.Now(), claudeQueued("2026-10-06T21:51:46Z", steer))
		transcriptLines(t, filepath.Join(folder, "helper.jsonl"), time.Now().Add(time.Minute), claudePrompt("2026-10-06T21:52:30Z", "summarize the diff"))

		if !(&HostProgress{Home: home}).Took(context.Background(), Conversation{Harness: "claude", Dir: worktree}, TextDigest(steer), sent) {
			t.Error("Took = false, want the goblin's own conversation found beside a newer one")
		}
	})
	t.Run("several texts in one message", func(t *testing.T) {
		home, worktree := t.TempDir(), t.TempDir()
		opening := map[string]any{"type": "session_meta", "payload": map[string]any{"cwd": worktree}}
		both := map[string]any{"timestamp": "2026-10-06T21:54:55Z", "type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": "CEO: main moved, merge it first."}, {"type": "input_text", "text": steer}}}}
		transcriptLines(t, filepath.Join(home, ".codex", "sessions", "2026", "10", "06", "rollout-2026-10-06T16-53-30-session.jsonl"), time.Now(), opening, both)

		if !(&HostProgress{Home: home}).Took(context.Background(), Conversation{Harness: "codex", Dir: worktree}, TextDigest(steer), sent) {
			t.Error("Took = false, want the steer found among the texts one message handed over")
		}
	})
	t.Run("a harness it does not know", func(t *testing.T) {
		if (&HostProgress{Home: t.TempDir()}).Took(context.Background(), Conversation{Harness: "aider", Session: "0be78a0f-1dfb-4139-8326-064c28e6a843"}, TextDigest(steer), sent) {
			t.Error("Took = true for a harness whose record is not read")
		}
	})
}

// A machine keeps thousands of Codex rollouts, whose openings are read to
// find a goblin's, and a fresh goblins send read them all for minutes: only the
// rollouts Codex filed since the day the goblin started are read.
func TestOnlyRolloutsFiledSinceTheGoblinStartedAreRead(t *testing.T) {
	sent := time.Date(2026, 10, 6, 21, 54, 52, 0, time.UTC)
	for _, test := range []struct {
		day  string
		want bool
	}{{"06", true}, {"01", false}} {
		t.Run("filed on day "+test.day, func(t *testing.T) {
			// Arrange
			home, worktree := t.TempDir(), t.TempDir()
			opening := map[string]any{"type": "session_meta", "payload": map[string]any{"cwd": worktree}}
			transcriptLines(t, filepath.Join(home, ".codex", "sessions", "2026", "10", test.day, "rollout-2026-10-"+test.day+"T16-53-39-session.jsonl"), time.Now(), opening, codexMessage("2026-10-06T21:54:55Z", "user", steer))

			// Act
			got := (&HostProgress{Home: home}).Took(context.Background(), Conversation{Harness: "codex", Dir: worktree, Started: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)}, TextDigest(steer), sent)

			// Assert
			if got != test.want {
				t.Errorf("Took = %v, want %v", got, test.want)
			}
		})
	}
}

// The CEO's record is found by the session its registration names, in
// whichever project folder holds it; a goblin's by the folder it works in.
func TestTheCEOsRecordIsFoundByItsSession(t *testing.T) {
	// Arrange
	home := t.TempDir()
	sent := time.Date(2026, 10, 6, 21, 51, 45, 0, time.UTC)
	transcriptLines(t, filepath.Join(home, ".claude", "projects", "C--dev-code-goblins", "0be78a0f-1dfb-4139-8326-064c28e6a843.jsonl"), time.Now(), claudeQueued("2026-10-06T21:52:00Z", steer))
	progress := &HostProgress{Home: home}

	// Act
	own := progress.Took(context.Background(), Conversation{Harness: "claude", Session: "0be78a0f-1dfb-4139-8326-064c28e6a843"}, TextDigest(steer), sent)
	other := progress.Took(context.Background(), Conversation{Harness: "claude", Session: "11111111-1dfb-4139-8326-064c28e6a843"}, TextDigest(steer), sent)

	// Assert
	if !own || other {
		t.Errorf("Took for its own session = %v and another = %v, want true and false", own, other)
	}
}

// A Codex session's record is the rollout whose name carries its whole id,
// never a newer one of a session whose id ends with it (#1901).
func TestACodexSessionsRecordIsNotOneWhoseIdEndsWithIt(t *testing.T) {
	// Arrange
	home := t.TempDir()
	sent := time.Date(2026, 10, 6, 21, 51, 45, 0, time.UTC)
	day := filepath.Join(home, ".codex", "sessions", "2026", "10", "06")
	transcriptLines(t, filepath.Join(day, "rollout-2026-10-06T21-50-00-document-task-session.jsonl"), time.Now(), codexMessage("2026-10-06T21:50:00Z", "user", "an earlier text"))
	transcriptLines(t, filepath.Join(day, "rollout-2026-10-06T21-50-30-other-document-task-session.jsonl"), time.Now().Add(time.Minute), codexMessage("2026-10-06T21:52:00Z", "user", steer))
	progress := &HostProgress{Home: home}

	// Act
	own := progress.Took(context.Background(), Conversation{Harness: "codex", Session: "document-task-session"}, TextDigest(steer), sent)
	other := progress.Took(context.Background(), Conversation{Harness: "codex", Session: "other-document-task-session"}, TextDigest(steer), sent)

	// Assert
	if own || !other {
		t.Errorf("Took for the session = %v and for the one whose id ends with it = %v, want false and true", own, other)
	}
}

// writeConversation writes entries where harness keeps the record of an
// agent working in worktree, beside one for another folder that holds the
// same entries, which must never count.
func writeConversation(t *testing.T, home, harness, worktree string, entries ...map[string]any) {
	t.Helper()
	other := t.TempDir()
	taken := map[string]map[string]any{"claude": claudeQueued("2026-10-06T21:59:00Z", steer), "codex": codexMessage("2026-10-06T21:59:00Z", "user", steer), "pi": piMessage("2026-10-06T21:59:00Z", "user", steer)}[harness]
	for i, dir := range []string{worktree, other} {
		// stall-accepted: a file's written time, which the code compares only with other files' times, never with the clock.
		written := time.Now().Add(time.Duration(i) * time.Minute)
		if dir == other {
			entries = []map[string]any{taken}
		}
		switch harness {
		case "claude":
			transcriptLines(t, filepath.Join(claudeProjectFolder(home, dir), "conversation.jsonl"), written, entries...)
		case "codex":
			opening := map[string]any{"type": "session_meta", "payload": map[string]any{"cwd": dir}}
			transcriptLines(t, filepath.Join(home, ".codex", "sessions", "2026", "10", "06", fmt.Sprintf("rollout-2026-10-06T16-53-3%d-session-%d.jsonl", i, i)), written, append([]map[string]any{opening}, entries...)...)
		case "pi":
			folder := "--" + strings.NewReplacer(`\`, "-", ":", "-").Replace(dir) + "--"
			opening := map[string]any{"type": "session", "cwd": dir}
			transcriptLines(t, filepath.Join(home, ".pi", "agent", "sessions", folder, "2026-10-06T21-50-00-000Z_session.jsonl"), written, append([]map[string]any{opening}, entries...)...)
		}
	}
}

func claudeSystem(at, subtype, content string) map[string]any {
	return map[string]any{"type": "system", "subtype": subtype, "timestamp": at, "content": content, "isMeta": false}
}

// A ! command typed during a turn is handed over once Claude Code's record
// shows it run as a shell command at the turn's end, typed with or without a
// space after the !; what it printed and another command are not (#2242).
func TestAShellCommandClaudeCodeRanIsTaken(t *testing.T) {
	sent := time.Date(2026, 11, 9, 21, 1, 19, 0, time.UTC)
	after := "2026-11-09T21:01:48Z"
	for _, tc := range []struct {
		name  string
		typed string
		entry map[string]any
		want  bool
	}{
		{name: "typed with a space", typed: "! claude doctor", entry: claudePrompt(after, "<bash-input>claude doctor</bash-input>"), want: true},
		{name: "typed without a space", typed: "!claude doctor", entry: claudePrompt(after, "<bash-input>claude doctor</bash-input>"), want: true},
		{name: "what it printed", typed: "! claude doctor", entry: claudePrompt(after, "<bash-stdout>Claude Code doctor</bash-stdout><bash-stderr></bash-stderr>"), want: false},
		{name: "another command", typed: "! claude doctor", entry: claudePrompt(after, "<bash-input>claude update</bash-input>"), want: false},
		{name: "only queued", typed: "! claude doctor", entry: claudeEnqueued(after, "claude doctor"), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			home, worktree := t.TempDir(), t.TempDir()
			transcriptLines(t, filepath.Join(claudeProjectFolder(home, worktree), "conversation.jsonl"), time.Now(), tc.entry)

			// Act
			took := (&HostProgress{Home: home}).Took(context.Background(), Conversation{Harness: "claude", Dir: worktree}, TextDigest(tc.typed), sent)

			// Assert
			if took != tc.want {
				t.Errorf("Took = %t, want %t", took, tc.want)
			}
		})
	}
}

// A slash command Claude Code runs itself is handed over once its record
// shows the command: as its tags in a user message, as a system
// local_command record (2.1.309), or for /remote-control as only the
// bridge_status record its status opens with (2.1.304 to 2.1.319). What the
// command printed, another command's status, and a status from before the
// send are not (#2193).
func TestASlashCommandClaudeCodeRanItselfIsTaken(t *testing.T) {
	sent := time.Date(2026, 11, 9, 14, 0, 0, 0, time.UTC)
	after, before := "2026-11-09T14:00:03Z", "2026-11-09T13:40:00Z"
	for _, tc := range []struct {
		name    string
		command string
		entry   map[string]any
		want    bool
	}{
		{name: "its tags in a user message", command: "/model opus", entry: claudePrompt(after, "<command-name>/model</command-name>\n            <command-message>model</command-message>\n            <command-args>opus</command-args>"), want: true},
		{name: "a system local_command record", command: "/remote-control", entry: claudeSystem(after, "local_command", "/remote-control"), want: true},
		{name: "only its bridge status", command: "/remote-control", entry: claudeSystem(after, "bridge_status", "/remote-control is active · Continue here, on your phone, or at https://claude.ai/code/session_1"), want: true},
		{name: "what it printed", command: "/remote-control", entry: claudeSystem(after, "local_command", "<local-command-stdout>/remote-control isn't available in this environment.</local-command-stdout>"), want: false},
		{name: "another command's status", command: "/remote", entry: claudeSystem(after, "bridge_status", "/remote-control is active · Continue here"), want: false},
		{name: "a status from before the send", command: "/remote-control", entry: claudeSystem(before, "bridge_status", "/remote-control is active · Continue here"), want: false},
		{name: "another system record", command: "/remote-control", entry: claudeSystem(after, "informational", "/remote-control"), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			home, worktree := t.TempDir(), t.TempDir()
			transcriptLines(t, filepath.Join(claudeProjectFolder(home, worktree), "conversation.jsonl"), time.Now(), tc.entry)

			// Act
			took := (&HostProgress{Home: home}).Took(context.Background(), Conversation{Harness: "claude", Dir: worktree}, TextDigest(tc.command), sent)

			// Assert
			if took != tc.want {
				t.Errorf("Took = %t, want %t", took, tc.want)
			}
		})
	}
}
