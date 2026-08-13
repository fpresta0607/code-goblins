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
		{"agent", "get", "pane-7", "--json", "--session", "fleet"},
		{"pane", "send-keys", "pane-7", "enter", "--session", "fleet"},
		{"agent", "get", "pane-7", "--json", "--session", "fleet"},
	})
}

func TestSenderTextUsesLongSettleForSlashAndCodexDollarMessages(t *testing.T) {
	for _, test := range []struct {
		name    string
		harness string
		message string
		want    time.Duration
	}{
		{name: "slash", harness: "claude", message: "/compact", want: 1200 * time.Millisecond},
		{name: "codex dollar", harness: "codex", message: "$review", want: 1200 * time.Millisecond},
		{name: "other dollar", harness: "claude", message: "$5", want: 300 * time.Millisecond},
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

			if err := sender.Text(context.Background(), "task-7", test.message); err != nil {
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
		{"agent", "get", "pane-7", "--json", "--session", "fleet"},
		{"pane", "send-keys", "pane-7", "enter", "--session", "fleet"},
		{"agent", "get", "pane-7", "--json", "--session", "fleet"},
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
