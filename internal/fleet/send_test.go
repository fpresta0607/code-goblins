package fleet

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
)

func TestSenderTextTypesOnceSettlesAndConfirmsWorking(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{
		rawReply(""),
		jsonReply(`{"result":{"agent":{"agent_status":"idle"}}}`),
		rawReply(""),
		jsonReply(`{"result":{"agent":{"agent_status":"working"}}}`),
	}}
	var clientSleeps []time.Duration
	var senderSleeps []time.Duration
	resolver := &fakeResolver{target: herdr.Target{Session: "fleet", Pane: "pane-7"}, meta: taskMeta("task-7", "claude")}
	sender := Sender{
		Resolve: resolver,
		Herdr:   newHerdrClient(runner, &clientSleeps),
		Sleep: func(_ context.Context, duration time.Duration) error {
			senderSleeps = append(senderSleeps, duration)
			return nil
		},
	}

	if err := sender.Text(context.Background(), "task-7", "do the work"); err != nil {
		t.Fatalf("Text: %v", err)
	}
	if !reflect.DeepEqual(senderSleeps, []time.Duration{300 * time.Millisecond}) {
		t.Errorf("sender sleeps = %v, want plain-message settle", senderSleeps)
	}
	assertRequests(t, runner.requests, [][]string{
		{"pane", "send-text", "pane-7", "do the work", "--session", "fleet"},
		{"agent", "get", "pane-7", "--session", "fleet"},
		{"pane", "send-keys", "pane-7", "enter", "--session", "fleet"},
		{"agent", "get", "pane-7", "--session", "fleet"},
	})
}

func TestSenderTextUsesLongSettleForSlashAndCodexDollarMessages(t *testing.T) {
	for _, test := range []struct {
		name    string
		harness string
		raw     string
		message string
		want    time.Duration
	}{
		{name: "slash", harness: "claude", message: "/compact", want: 1200 * time.Millisecond},
		{name: "codex dollar", harness: "codex", message: "$review", want: 1200 * time.Millisecond},
		{name: "other dollar", harness: "claude", message: "$5", want: 300 * time.Millisecond},
		{name: "explicit target dollar without metadata", raw: "fleet:pane-7", message: "$review", want: 1200 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeRunner{replies: []runnerReply{
				rawReply(""),
				jsonReply(`{"result":{"agent":{"agent_status":"idle"}}}`),
				rawReply(""),
				jsonReply(`{"result":{"agent":{"agent_status":"working"}}}`),
			}}
			var clientSleeps []time.Duration
			var senderSleeps []time.Duration
			sender := Sender{
				Resolve: &fakeResolver{target: herdr.Target{Session: "fleet", Pane: "pane-7"}, meta: taskMeta("task-7", test.harness)},
				Herdr:   newHerdrClient(runner, &clientSleeps),
				Sleep: func(_ context.Context, duration time.Duration) error {
					senderSleeps = append(senderSleeps, duration)
					return nil
				},
			}

			raw := test.raw
			if raw == "" {
				raw = "task-7"
			}
			if err := sender.Text(context.Background(), raw, test.message); err != nil {
				t.Fatalf("Text: %v", err)
			}
			if !reflect.DeepEqual(senderSleeps, []time.Duration{test.want}) {
				t.Errorf("sender sleeps = %v, want %v", senderSleeps, test.want)
			}
		})
	}
}

func TestSenderTextRetriesOnlyEnterUntilWorking(t *testing.T) {
	replies := []runnerReply{
		rawReply(""),
		jsonReply(`{"result":{"agent":{"agent_status":"idle"}}}`),
		rawReply(""),
	}
	for range 6 {
		replies = append(replies, jsonReply(`{"result":{"agent":{"agent_status":"idle"}}}`))
	}
	replies = append(replies,
		rawReply(""),
		jsonReply(`{"result":{"agent":{"agent_status":"working"}}}`),
	)
	runner := &fakeRunner{replies: replies}
	var clientSleeps []time.Duration
	resolver := &fakeResolver{target: herdr.Target{Session: "fleet", Pane: "pane-7"}, meta: taskMeta("task-7", "claude")}
	sender := Sender{Resolve: resolver, Herdr: newHerdrClient(runner, &clientSleeps), Sleep: noSleep}

	if err := sender.Text(context.Background(), "task-7", "retry me"); err != nil {
		t.Fatalf("Text: %v", err)
	}
	sends := 0
	enters := 0
	for _, request := range runner.requests {
		if len(request.Args) >= 2 && request.Args[0] == "pane" && request.Args[1] == "send-text" {
			sends++
		}
		if len(request.Args) >= 4 && request.Args[0] == "pane" && request.Args[1] == "send-keys" && request.Args[3] == "enter" {
			enters++
		}
	}
	if sends != 1 || enters != 2 {
		t.Errorf("send-text calls = %d, Enter calls = %d, want 1 and 2", sends, enters)
	}
}

func TestSenderTextRefusesPendingAndUnknownConfirmation(t *testing.T) {
	for _, test := range []struct {
		name    string
		replies []runnerReply
		want    string
	}{
		{
			name: "pending",
			replies: append([]runnerReply{
				rawReply(""),
				jsonReply(`{"result":{"agent":{"agent_status":"idle"}}}`),
				rawReply(""),
				jsonReply(`{"result":{"agent":{"agent_status":"idle"}}}`),
			}, malformedReplies(5)...),
			want: "pending",
		},
		{
			name: "unknown",
			replies: append([]runnerReply{
				rawReply(""),
				jsonReply(`{"result":{"agent":{"agent_status":"idle"}}}`),
				rawReply(""),
			}, malformedReplies(6)...),
			want: "unknown",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeRunner{replies: test.replies}
			var clientSleeps []time.Duration
			sender := Sender{
				Resolve: &fakeResolver{target: herdr.Target{Session: "fleet", Pane: "pane-7"}, meta: taskMeta("task-7", "claude")},
				Herdr:   newHerdrClient(runner, &clientSleeps),
				Sleep:   noSleep,
			}

			assertErrorContains(t, sender.Text(context.Background(), "task-7", "careful"), test.want)
			enters := 0
			for _, request := range runner.requests {
				if len(request.Args) >= 4 && request.Args[0] == "pane" && request.Args[1] == "send-keys" && request.Args[3] == "enter" {
					enters++
				}
			}
			if enters != 1 {
				t.Errorf("Enter calls = %d, want 1 after %s confirmation", enters, test.want)
			}
		})
	}
}

func TestSenderTextConfirmsBlockedAfterSubmit(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{
		rawReply(""),
		jsonReply(`{"result":{"agent":{"agent_status":"idle"}}}`),
		rawReply(""),
		jsonReply(`{"result":{"agent":{"agent_status":"blocked"}}}`),
	}}
	var clientSleeps []time.Duration
	sender := Sender{
		Resolve: &fakeResolver{target: herdr.Target{Session: "fleet", Pane: "pane-7"}, meta: taskMeta("task-7", "claude")},
		Herdr:   newHerdrClient(runner, &clientSleeps),
		Sleep:   noSleep,
	}

	if err := sender.Text(context.Background(), "task-7", "needs approval"); err != nil {
		t.Fatalf("Text: %v", err)
	}
	assertRequests(t, runner.requests, [][]string{
		{"pane", "send-text", "pane-7", "needs approval", "--session", "fleet"},
		{"agent", "get", "pane-7", "--session", "fleet"},
		{"pane", "send-keys", "pane-7", "enter", "--session", "fleet"},
		{"agent", "get", "pane-7", "--session", "fleet"},
	})
}

func TestSenderTextUsesComposerConfirmationForBlockedBaseline(t *testing.T) {
	replies := []runnerReply{
		rawReply(""),
		jsonReply(`{"result":{"agent":{"agent_status":"blocked"}}}`),
	}
	for range enterRetries {
		replies = append(replies, rawReply(""), rawReply("\n  ❯ draft\n"))
	}
	runner := &fakeRunner{replies: replies}
	var clientSleeps []time.Duration
	sender := Sender{
		Resolve: &fakeResolver{target: herdr.Target{Session: "fleet", Pane: "pane-7"}, meta: taskMeta("task-7", "claude")},
		Herdr:   newHerdrClient(runner, &clientSleeps),
		Sleep:   noSleep,
	}

	assertErrorContains(t, sender.Text(context.Background(), "task-7", "draft"), "pending")
	agentGets := 0
	enters := 0
	for _, request := range runner.requests {
		if len(request.Args) >= 2 && request.Args[0] == "agent" && request.Args[1] == "get" {
			agentGets++
		}
		if len(request.Args) >= 4 && request.Args[0] == "pane" && request.Args[1] == "send-keys" && request.Args[3] == "enter" {
			enters++
		}
	}
	if agentGets != 1 {
		t.Errorf("agent get calls = %d, want only the blocked baseline", agentGets)
	}
	if enters != enterRetries {
		t.Errorf("Enter calls = %d, want %d", enters, enterRetries)
	}
}

func TestSenderTextUsesComposerEvidenceWhenTargetWasAlreadyWorking(t *testing.T) {
	for _, test := range []struct {
		name      string
		captures  []string
		wantError string
		wantEnter int
	}{
		{name: "empty composer confirms", captures: []string{"\n  ❯\n"}, wantEnter: 1},
		{name: "pending composer refuses", captures: []string{"\n  ❯ draft\n", "\n  ❯ draft\n", "\n  ❯ draft\n"}, wantError: "pending", wantEnter: 3},
		{name: "unknown composer refuses", captures: []string{"not a known composer"}, wantError: "unknown", wantEnter: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			replies := []runnerReply{
				rawReply(""),
				jsonReply(`{"result":{"agent":{"agent_status":"working"}}}`),
			}
			for _, capture := range test.captures {
				replies = append(replies, rawReply(""), rawReply(capture))
			}
			runner := &fakeRunner{replies: replies}
			var clientSleeps []time.Duration
			var senderSleeps []time.Duration
			sender := Sender{
				Resolve: &fakeResolver{target: herdr.Target{Session: "fleet", Pane: "pane-7"}, meta: taskMeta("task-7", "claude")},
				Herdr:   newHerdrClient(runner, &clientSleeps),
				Sleep: func(_ context.Context, duration time.Duration) error {
					senderSleeps = append(senderSleeps, duration)
					return nil
				},
			}

			err := sender.Text(context.Background(), "task-7", "draft")
			if test.wantError == "" && err != nil {
				t.Fatalf("Text: %v", err)
			}
			if test.wantError != "" {
				assertErrorContains(t, err, test.wantError)
			}
			enters := 0
			for _, request := range runner.requests {
				if len(request.Args) >= 4 && request.Args[0] == "pane" && request.Args[1] == "send-keys" && request.Args[3] == "enter" {
					enters++
				}
			}
			if enters != test.wantEnter {
				t.Errorf("Enter calls = %d, want %d", enters, test.wantEnter)
			}
			if !reflect.DeepEqual(senderSleeps, []time.Duration{300 * time.Millisecond, 400 * time.Millisecond}) && test.wantEnter == 1 {
				t.Errorf("sender sleeps = %v, want settle and composer confirmation wait", senderSleeps)
			}
		})
	}
}

func TestSenderTextConfirmsOnlyCurrentClaudeAndCodexComposer(t *testing.T) {
	for _, test := range []struct {
		name      string
		harness   string
		capture   string
		wantError string
	}{
		{name: "current Claude prompt confirms", harness: "claude", capture: "\n  \u276f\n"},
		{name: "current Codex prompt confirms", harness: "codex", capture: "\n  \u203a\n"},
		{name: "Claude idle composer above footer confirms", harness: "claude", capture: "\n  \u276f\n\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\n  [PONYTAIL]                                                /rc\n  \u2802\u2802 bypass permissions on (shift+tab to cycle) \u00b7 \u2190 for agents\n  \u25cf main\n  \u25ef general-purpose  Confirming layout\n"},
		{name: "Codex idle composer above footer confirms", harness: "codex", capture: "\n  \u203a\n\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\n  28k tokens left\n"},
		{name: "Claude idle composer above footer and trailing blank rows confirms", harness: "claude", capture: "\n  \u276f\n\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\n  \u2802\u2802 bypass permissions on (shift+tab to cycle)\n" + strings.Repeat("\n", 30)},
		{name: "Claude prompt in scrollback beyond the window refuses", harness: "claude", capture: "\n  \u276f old parked draft\n" + strings.Repeat("filled scrollback line\n", 40) + "new terminal output\n", wantError: "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeRunner{replies: []runnerReply{
				rawReply(""),
				jsonReply(`{"result":{"agent":{"agent_status":"working"}}}`),
				rawReply(""),
				rawReply(test.capture),
			}}
			var clientSleeps []time.Duration
			sender := Sender{
				Resolve: &fakeResolver{target: herdr.Target{Session: "fleet", Pane: "pane-7"}, meta: taskMeta("task-7", test.harness)},
				Herdr:   newHerdrClient(runner, &clientSleeps),
				Sleep:   noSleep,
			}

			err := sender.Text(context.Background(), "task-7", "draft")
			if test.wantError == "" && err != nil {
				t.Fatalf("Text: %v", err)
			}
			if test.wantError != "" {
				assertErrorContains(t, err, test.wantError)
			}
		})
	}
}

func TestSenderTextAcceptsCurrentPiComposerWithFooter(t *testing.T) {
	var currentPiIdleFixture = "\u001b[0m\u001b[38;2;129;162;190m\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\u001b[0m\n\u001b[0m\u001b[7m \u001b[0m                                                    \n\u001b[0m\u001b[38;2;129;162;190m\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\\u2500\u001b[0m\n\u001b[0m\u001b[38;2;102;102;102m~/synthetic-primary (main)\u001b[0m\n"
	currentPiIdleFixture = strings.ReplaceAll(currentPiIdleFixture, "\\u2500", "\u2500")
	runner := &fakeRunner{replies: []runnerReply{
		rawReply(""),
		jsonReply(`{"result":{"agent":{"agent_status":"working"}}}`),
		rawReply(""),
		rawReply(currentPiIdleFixture),
		jsonReply(`{"result":{"agent":{"agent":"pi","agent_status":"idle"}}}`),
	}}
	var clientSleeps []time.Duration
	sender := Sender{
		Resolve: &fakeResolver{target: herdr.Target{Session: "fleet", Pane: "pane-7"}, meta: taskMeta("task-7", "pi")},
		Herdr:   newHerdrClient(runner, &clientSleeps),
		Sleep:   noSleep,
	}

	if err := sender.Text(context.Background(), "task-7", "draft"); err != nil {
		t.Fatalf("Text: %v", err)
	}
}

func TestSenderTextPermitsOnlyOneImmediatePiFooter(t *testing.T) {
	separator := strings.Repeat("\u2500", 8)
	for _, test := range []struct {
		name      string
		capture   string
		wantError string
	}{
		{
			name:    "current pair with one immediate footer confirms",
			capture: "\n" + separator + "\n\n" + separator + "\n~/project (main)\n",
		},
		{
			name:      "stale terminal footer before current footer refuses",
			capture:   "\n" + separator + "\n\n" + separator + "\n~/terminal-output (old)\n~/project (main)\n",
			wantError: "unknown",
		},
		{
			name:      "footer with later output refuses",
			capture:   "\n" + separator + "\n\n" + separator + "\n~/project (main)\n~/later-output (main)\n",
			wantError: "unknown",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeRunner{replies: []runnerReply{
				rawReply(""),
				jsonReply(`{"result":{"agent":{"agent_status":"working"}}}`),
				rawReply(""),
				rawReply(test.capture),
				jsonReply(`{"result":{"agent":{"agent":"pi","agent_status":"idle"}}}`),
			}}
			var clientSleeps []time.Duration
			sender := Sender{
				Resolve: &fakeResolver{target: herdr.Target{Session: "fleet", Pane: "pane-7"}, meta: taskMeta("task-7", "pi")},
				Herdr:   newHerdrClient(runner, &clientSleeps),
				Sleep:   noSleep,
			}

			err := sender.Text(context.Background(), "task-7", "draft")
			if test.wantError == "" && err != nil {
				t.Fatalf("Text: %v", err)
			}
			if test.wantError != "" {
				assertErrorContains(t, err, test.wantError)
			}
		})
	}
}

func TestSenderTextConfirmsAndRefusesPiComposerState(t *testing.T) {
	for _, test := range []struct {
		name          string
		baseline      string
		captures      []string
		postAgent     string
		wantError     string
		wantEnter     int
		wantAgentGets int
	}{
		{
			name:          "current blank composer with post-send Pi identity confirms",
			baseline:      `{"result":{"agent":{"agent_status":"working"}}}`,
			captures:      []string{"\n────────\n\n────────\n"},
			postAgent:     `{"result":{"agent":{"agent":"pi","agent_status":"idle"}}}`,
			wantEnter:     1,
			wantAgentGets: 2,
		},
		{
			name:          "working Pi alone cannot confirm blank composer",
			baseline:      `{"result":{"agent":{"agent_status":"working"}}}`,
			captures:      []string{"\n────────\n\n────────\n"},
			postAgent:     `{"result":{"agent":{"agent":"pi","agent_status":"working"}}}`,
			wantError:     "unknown",
			wantEnter:     1,
			wantAgentGets: 2,
		},
		{
			name:          "non Pi identity cannot confirm blank composer",
			baseline:      `{"result":{"agent":{"agent_status":"working"}}}`,
			captures:      []string{"\n────────\n\n────────\n"},
			postAgent:     `{"result":{"agent":{"agent":"claude","agent_status":"idle"}}}`,
			wantError:     "unknown",
			wantEnter:     1,
			wantAgentGets: 2,
		},
		{
			name:          "stale blank pair above terminal content refuses",
			baseline:      `{"result":{"agent":{"agent_status":"working"}}}`,
			captures:      []string{"\n────────\n\n────────\nPS C:\\work>"},
			wantError:     "unknown",
			wantEnter:     1,
			wantAgentGets: 1,
		},
		{
			name:          "working baseline with typed composer refuses",
			baseline:      `{"result":{"agent":{"agent_status":"working"}}}`,
			captures:      []string{"\n────────\nactual text\n────────\n", "\n────────\nactual text\n────────\n", "\n────────\nactual text\n────────\n"},
			wantError:     "pending",
			wantEnter:     enterRetries,
			wantAgentGets: 1,
		},
		{
			name:          "unreadable baseline confirms only from post-send Pi identity",
			baseline:      "{",
			captures:      []string{"\n────────\n\n────────\n"},
			postAgent:     `{"result":{"agent":{"agent":"pi","agent_status":"blocked"}}}`,
			wantEnter:     1,
			wantAgentGets: 2,
		},
		{
			name:          "unreadable baseline retains typed composer as pending",
			baseline:      "{",
			captures:      []string{"\n────────\nactual text\n────────\n", "\n────────\nactual text\n────────\n", "\n────────\nactual text\n────────\n"},
			wantError:     "pending",
			wantEnter:     enterRetries,
			wantAgentGets: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			replies := []runnerReply{rawReply(""), jsonReply(test.baseline)}
			for index, capture := range test.captures {
				replies = append(replies, rawReply(""), rawReply(capture))
				if index == len(test.captures)-1 && test.postAgent != "" {
					replies = append(replies, jsonReply(test.postAgent))
				}
			}
			runner := &fakeRunner{replies: replies}
			var clientSleeps []time.Duration
			sender := Sender{
				Resolve: &fakeResolver{target: herdr.Target{Session: "fleet", Pane: "pane-7"}, meta: taskMeta("task-7", "pi")},
				Herdr:   newHerdrClient(runner, &clientSleeps),
				Sleep:   noSleep,
			}

			err := sender.Text(context.Background(), "task-7", "actual text")
			if test.wantError == "" && err != nil {
				t.Fatalf("Text: %v", err)
			}
			if test.wantError != "" {
				assertErrorContains(t, err, test.wantError)
			}
			enters := 0
			agentGets := 0
			for _, request := range runner.requests {
				if len(request.Args) >= 4 && request.Args[0] == "pane" && request.Args[1] == "send-keys" && request.Args[3] == "enter" {
					enters++
				}
				if len(request.Args) >= 2 && request.Args[0] == "agent" && request.Args[1] == "get" {
					agentGets++
				}
			}
			if enters != test.wantEnter {
				t.Errorf("Enter calls = %d, want %d", enters, test.wantEnter)
			}
			if agentGets != test.wantAgentGets {
				t.Errorf("agent get calls = %d, want %d", agentGets, test.wantAgentGets)
			}
		})
	}
}

func TestSenderTextAutoSubmitsParkedKimiComposerWithCtrlS(t *testing.T) {
	kimiParkedBox := "\nprevious turn output\n╭────────────────────────────────╮\n│ > fix the thing                │\n╰────────────────────────────────╯\nstatus footer\n"
	replies := []runnerReply{
		rawReply(""),
		jsonReply(`{"result":{"agent":{"agent_status":"idle"}}}`),
		rawReply(""),
	}
	for range confirmPolls {
		replies = append(replies, jsonReply(`{"result":{"agent":{"agent_status":"idle"}}}`))
	}
	replies = append(replies,
		rawReply(kimiParkedBox),
		rawReply(""),
		jsonReply(`{"result":{"agent":{"agent_status":"working"}}}`),
	)
	runner := &fakeRunner{replies: replies}
	var clientSleeps []time.Duration
	sender := Sender{
		Resolve:    &fakeResolver{target: herdr.Target{Session: "fleet", Pane: "pane-7"}, meta: taskMeta("task-7", "kimi")},
		Herdr:      newHerdrClient(runner, &clientSleeps),
		Sleep:      noSleep,
		AutoSubmit: true,
	}

	if err := sender.Text(context.Background(), "task-7", "fix the thing"); err != nil {
		t.Fatalf("Text: %v", err)
	}
	var keys []string
	for _, request := range runner.requests {
		if len(request.Args) >= 4 && request.Args[0] == "pane" && request.Args[1] == "send-keys" {
			keys = append(keys, request.Args[3])
		}
	}
	if !reflect.DeepEqual(keys, []string{"enter", "ctrl+s"}) {
		t.Errorf("submit keys = %v, want Enter then ctrl+s for the parked kimi composer", keys)
	}
}

func TestSenderTextAutoSubmitKeepsEnterWhenComposerIsClear(t *testing.T) {
	kimiEmptyBox := "\nprevious turn output\n╭────────────────────────────────╮\n│ >                              │\n╰────────────────────────────────╯\nstatus footer\n"
	replies := []runnerReply{
		rawReply(""),
		jsonReply(`{"result":{"agent":{"agent_status":"idle"}}}`),
		rawReply(""),
	}
	for range confirmPolls {
		replies = append(replies, jsonReply(`{"result":{"agent":{"agent_status":"idle"}}}`))
	}
	replies = append(replies,
		rawReply(kimiEmptyBox),
		rawReply(""),
		jsonReply(`{"result":{"agent":{"agent_status":"working"}}}`),
	)
	runner := &fakeRunner{replies: replies}
	var clientSleeps []time.Duration
	sender := Sender{
		Resolve:    &fakeResolver{target: herdr.Target{Session: "fleet", Pane: "pane-7"}, meta: taskMeta("task-7", "kimi")},
		Herdr:      newHerdrClient(runner, &clientSleeps),
		Sleep:      noSleep,
		AutoSubmit: true,
	}

	if err := sender.Text(context.Background(), "task-7", "fix the thing"); err != nil {
		t.Fatalf("Text: %v", err)
	}
	for _, request := range runner.requests {
		if len(request.Args) >= 4 && request.Args[0] == "pane" && request.Args[1] == "send-keys" && request.Args[3] == "ctrl+s" {
			t.Error("ctrl+s sent although the composer did not clearly hold the delivered text")
		}
	}
}

func TestSenderTextAutoSubmitVerifiesParkedPiComposer(t *testing.T) {
	separator := strings.Repeat("─", 8)
	tests := []struct {
		name      string
		capture   string
		wantKeys  []string
		wantReads int
	}{
		{
			name:      "parked pi composer reads back and resubmits Enter",
			capture:   "\n" + separator + "\nfix the thing\n" + separator + "\n~/project (main)\n",
			wantKeys:  []string{"enter", "enter"},
			wantReads: 1,
		},
		{
			name:      "clear pi composer stays conservative",
			capture:   "\n" + separator + "\n\n" + separator + "\n~/project (main)\n",
			wantKeys:  []string{"enter", "enter"},
			wantReads: 1,
		},
		{
			name:      "absent pi composer stays conservative",
			capture:   "\nterminal output\n",
			wantKeys:  []string{"enter", "enter"},
			wantReads: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			replies := []runnerReply{
				rawReply(""),
				jsonReply(`{"result":{"agent":{"agent_status":"idle"}}}`),
				rawReply(""),
			}
			for range confirmPolls {
				replies = append(replies, jsonReply(`{"result":{"agent":{"agent_status":"idle"}}}`))
			}
			replies = append(replies,
				rawReply(test.capture),
				rawReply(""),
				jsonReply(`{"result":{"agent":{"agent_status":"working"}}}`),
			)
			runner := &fakeRunner{replies: replies}
			var clientSleeps []time.Duration
			sender := Sender{
				Resolve:    &fakeResolver{target: herdr.Target{Session: "fleet", Pane: "pane-7"}, meta: taskMeta("task-7", "pi")},
				Herdr:      newHerdrClient(runner, &clientSleeps),
				Sleep:      noSleep,
				AutoSubmit: true,
			}

			if err := sender.Text(context.Background(), "task-7", "fix the thing"); err != nil {
				t.Fatalf("Text: %v", err)
			}
			var keys []string
			reads := 0
			for _, request := range runner.requests {
				if len(request.Args) >= 4 && request.Args[0] == "pane" && request.Args[1] == "send-keys" {
					keys = append(keys, request.Args[3])
				}
				if len(request.Args) >= 2 && request.Args[0] == "pane" && request.Args[1] == "read" {
					reads++
				}
			}
			if !reflect.DeepEqual(keys, test.wantKeys) {
				t.Errorf("submit keys = %v, want %v", keys, test.wantKeys)
			}
			if reads != test.wantReads {
				t.Errorf("pane read calls = %d, want %d", reads, test.wantReads)
			}
		})
	}
}

func TestSenderTextWithoutAutoSubmitNeverReadsThePaneBack(t *testing.T) {
	replies := []runnerReply{
		rawReply(""),
		jsonReply(`{"result":{"agent":{"agent_status":"idle"}}}`),
		rawReply(""),
	}
	for range confirmPolls {
		replies = append(replies, jsonReply(`{"result":{"agent":{"agent_status":"idle"}}}`))
	}
	replies = append(replies,
		rawReply(""),
		jsonReply(`{"result":{"agent":{"agent_status":"working"}}}`),
	)
	runner := &fakeRunner{replies: replies}
	var clientSleeps []time.Duration
	sender := Sender{
		Resolve: &fakeResolver{target: herdr.Target{Session: "fleet", Pane: "pane-7"}, meta: taskMeta("task-7", "kimi")},
		Herdr:   newHerdrClient(runner, &clientSleeps),
		Sleep:   noSleep,
	}

	if err := sender.Text(context.Background(), "task-7", "fix the thing"); err != nil {
		t.Fatalf("Text: %v", err)
	}
	for _, request := range runner.requests {
		if len(request.Args) >= 2 && request.Args[0] == "pane" && request.Args[1] == "read" {
			t.Error("pane read requested without AutoSubmit")
		}
	}
}

func TestComposerPending(t *testing.T) {
	separator := strings.Repeat("─", 8)
	for _, test := range []struct {
		name     string
		captured string
		harness  string
		message  string
		want     bool
	}{
		{name: "kimi parked message", harness: "kimi", message: "fix the thing", captured: "\n╭────╮\n│ > fix the thing │\n╰────╯\nfooter\n", want: true},
		{name: "kimi empty composer", harness: "kimi", message: "fix the thing", captured: "\n╭────╮\n│ >  │\n╰────╯\nfooter\n", want: false},
		{name: "kimi message only in scrollback", harness: "kimi", message: "fix the thing", captured: "\nfix the thing\nworking on it\n", want: false},
		{name: "kimi wrapped parked message", harness: "kimi", message: "please fix the thing", captured: "\n╭────╮\n│ > please fix │\n│ the thing    │\n╰────╯\nfooter\n", want: true},
		{name: "pi parked message", harness: "pi", message: "fix the thing", captured: "\n" + separator + "\nfix the thing\n" + separator + "\n~/project (main)\n", want: true},
		{name: "pi empty composer", harness: "pi", message: "fix the thing", captured: "\n" + separator + "\n\n" + separator + "\n~/project (main)\n", want: false},
		{name: "pi stale composer above output", harness: "pi", message: "fix the thing", captured: "\n" + separator + "\nfix the thing\n" + separator + "\n~/project (main)\nnew output\n", want: false},
		{name: "claude parked message", harness: "claude", message: "fix the thing", captured: "\n  ❯ fix the thing\n", want: true},
		{name: "claude empty prompt", harness: "claude", message: "fix the thing", captured: "\n  ❯\n", want: false},
		{name: "codex parked message", harness: "codex", message: "fix the thing", captured: "\n  › fix the thing\n", want: true},
		{name: "unknown harness never pending", harness: "", message: "fix the thing", captured: "\nfix the thing\n", want: false},
		{name: "claude parked message under footer", harness: "claude", message: "fix the thing", captured: "\n  ❯ fix the thing\n──────────────────────────────\n  [PONYTAIL]                                                /rc\n  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents\n  ● main\n  ◯ general-purpose  Confirming layout  11m 2s\n", want: true},
		{name: "codex parked message under footer", harness: "codex", message: "fix the thing", captured: "\n  › fix the thing\n──────────────────────────────\n  28k tokens left\n  · 4 revisions\n", want: true},
		{name: "claude queued message is not pending", harness: "claude", message: "fix the thing", captured: "\n  ❯ fix the thing\n❯ Press up to edit queued messages\n──────────────────────────────\n  [PONYTAIL]                                                /rc\n", want: false},
		{name: "claude parked message under footer and trailing blank rows", harness: "claude", message: "fix the thing", captured: "\n  ❯ fix the thing\n──────────────────────────────\n  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents\n" + strings.Repeat("\n", 30), want: true},
		{name: "claude message only in scrollback beyond the window", harness: "claude", message: "fix the thing", captured: "\n  ❯ fix the thing\n" + strings.Repeat("filled scrollback line\n", 40) + "new terminal output\n", want: false},
		{name: "claude message just inside the non-empty window", harness: "claude", message: "fix the thing", captured: "\n  ❯ fix the thing\n" + strings.Repeat("\nfooter row\n", 19), want: true},
		{name: "claude message just outside the non-empty window", harness: "claude", message: "fix the thing", captured: "\n  ❯ fix the thing\n" + strings.Repeat("\nfooter row\n", 20), want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := composerPending(test.captured, test.harness, test.message); got != test.want {
				t.Errorf("composerPending = %v, want %v", got, test.want)
			}
		})
	}
}

func TestSubmitKeyIsHarnessSpecific(t *testing.T) {
	if got := submitKey("kimi"); got != "ctrl+s" {
		t.Errorf("submitKey(kimi) = %q, want ctrl+s", got)
	}
	for _, name := range []string{"pi", "claude", "codex", ""} {
		if got := submitKey(name); got != "Enter" {
			t.Errorf("submitKey(%q) = %q, want Enter", name, got)
		}
	}
}

func TestSenderKeyNormalizesOnlySupportedKeys(t *testing.T) {
	for _, test := range []struct {
		name string
		key  string
		want string
	}{
		{name: "Enter", key: "enter", want: "enter"},
		{name: "Escape", key: "Esc", want: "escape"},
		{name: "Ctrl C", key: "Ctrl-C", want: "ctrl+c"},
		{name: "Ctrl U", key: "c-u", want: "ctrl+u"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeRunner{replies: []runnerReply{rawReply("")}}
			var clientSleeps []time.Duration
			sender := Sender{Resolve: &fakeResolver{target: herdr.Target{Session: "fleet", Pane: "pane-7"}}, Herdr: newHerdrClient(runner, &clientSleeps)}

			if err := sender.Key(context.Background(), "task-7", test.key); err != nil {
				t.Fatalf("Key: %v", err)
			}
			assertRequests(t, runner.requests, [][]string{{"pane", "send-keys", "pane-7", test.want, "--session", "fleet"}})
		})
	}

	runner := &fakeRunner{}
	var clientSleeps []time.Duration
	sender := Sender{Resolve: &fakeResolver{target: herdr.Target{Session: "fleet", Pane: "pane-7"}}, Herdr: newHerdrClient(runner, &clientSleeps)}
	assertErrorContains(t, sender.Key(context.Background(), "task-7", "F1"), "unsupported key")
	if len(runner.requests) != 0 {
		t.Errorf("unsupported key made Herdr requests: %#v", runner.requests)
	}
}

func malformedReplies(count int) []runnerReply {
	replies := make([]runnerReply, count)
	for index := range replies {
		replies[index] = jsonReply("{")
	}
	return replies
}

func noSleep(context.Context, time.Duration) error {
	return nil
}

func TestSenderRequiresCollaborators(t *testing.T) {
	assertErrorContains(t, (Sender{}).Text(context.Background(), "task-7", "message"), "resolver")
	assertErrorContains(t, (Sender{Resolve: &fakeResolver{}}).Text(context.Background(), "task-7", "message"), "Herdr")
	if strings.Contains("", "not used") {
		t.Fatal("unreachable")
	}
}

func TestSenderTextAutoSubmitResubmitsParkedClaudeAndCodexComposerUnderFooter(t *testing.T) {
	claudeFooter := "──────────────────────────────\n  [PONYTAIL]                                                /rc\n  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents\n  ● main\n  ◯ general-purpose  Confirming layout  11m 2s\n"
	codexFooter := "──────────────────────────────\n  28k tokens left\n  · 4 revisions\n"
	for _, test := range []struct {
		name    string
		harness string
		parked  string
		cleared string
	}{
		{name: "claude", harness: "claude", parked: "\n  ❯ fix the thing\n" + claudeFooter, cleared: "\n  ❯\n" + claudeFooter},
		{name: "codex", harness: "codex", parked: "\n  › fix the thing\n" + codexFooter, cleared: "\n  ›\n" + codexFooter},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeRunner{replies: []runnerReply{
				rawReply(""),
				jsonReply(`{"result":{"agent":{"agent_status":"working"}}}`),
				rawReply(""),
				rawReply(test.parked),
				rawReply(test.parked),
				rawReply(""),
				rawReply(test.cleared),
			}}
			var clientSleeps []time.Duration
			sender := Sender{
				Resolve:    &fakeResolver{target: herdr.Target{Session: "fleet", Pane: "pane-7"}, meta: taskMeta("task-7", test.harness)},
				Herdr:      newHerdrClient(runner, &clientSleeps),
				Sleep:      noSleep,
				AutoSubmit: true,
			}

			if err := sender.Text(context.Background(), "task-7", "fix the thing"); err != nil {
				t.Fatalf("Text: %v", err)
			}
			var keys []string
			reads := 0
			for _, request := range runner.requests {
				if len(request.Args) >= 4 && request.Args[0] == "pane" && request.Args[1] == "send-keys" {
					keys = append(keys, request.Args[3])
				}
				if len(request.Args) >= 2 && request.Args[0] == "pane" && request.Args[1] == "read" {
					reads++
				}
			}
			if !reflect.DeepEqual(keys, []string{"enter", "enter"}) {
				t.Errorf("submit keys = %v, want Enter resubmitted once for the parked composer under the footer", keys)
			}
			if reads != 3 {
				t.Errorf("pane read calls = %d, want 3 (composer state, pending check, composer state)", reads)
			}
		})
	}
}
