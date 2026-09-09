package fleet

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
)

// agentFake is a stateful herdr whose agent counters advance ONLY for a prompt
// it actually accepted. That is the load-bearing property of every test below:
// a fake that advanced them on any read would report an undelivered message as
// delivered, and each of these tests would pass while proving nothing.
type agentFake struct {
	requests [][]string

	// promptRefusals refuses that many `agent prompt` calls before accepting.
	promptRefusals int
	// getRefusals refuses that many `agent get` calls, modelling a herdr that
	// is momentarily busy rather than a delivery failure.
	getRefusals int
	// acceptAfterGets delays the advance until that many reads have happened.
	acceptAfterGets int
	// inert accepts the prompt but never moves - the shape a harness that
	// swallowed the text would produce.
	inert bool

	accepted    bool
	promptCalls int
	getCalls    int
	seq         int64
	revision    int64
	status      string
	// revisionOnly moves revision without state_change_seq and without
	// leaving idle. This is the observed kimi shape, not a hypothetical.
	revisionOnly bool
}

func (f *agentFake) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	f.requests = append(f.requests, request.Args)
	args := request.Args
	switch {
	case len(args) >= 3 && args[0] == "agent" && args[1] == "prompt":
		f.promptCalls++
		if f.promptRefusals > 0 {
			f.promptRefusals--
			return execx.Result{ExitCode: 1, Stderr: []byte("agent prompt: pane busy")}, nil
		}
		f.accepted = true
		return execx.Result{Stdout: []byte(`{"result":{"agent":{"agent":"claude","agent_status":"idle"}}}`)}, nil
	case len(args) >= 3 && args[0] == "agent" && args[1] == "get":
		f.getCalls++
		if f.getRefusals > 0 {
			f.getRefusals--
			return execx.Result{ExitCode: 1, Stderr: []byte("agent get: pane busy")}, nil
		}
		if f.accepted && !f.inert && f.getCalls > f.acceptAfterGets {
			if f.revisionOnly {
				f.revision++
			} else {
				f.seq++
				f.revision++
				f.status = "working"
			}
		}
		status := f.status
		if status == "" {
			status = "idle"
		}
		body := `{"result":{"agent":{"agent":"claude","agent_status":"` + status +
			`","state_change_seq":` + strconv.FormatInt(f.seq, 10) +
			`,"revision":` + strconv.FormatInt(f.revision, 10) + `}}}`
		return execx.Result{Stdout: []byte(body)}, nil
	}
	return execx.Result{}, errors.New("unexpected request: " + strings.Join(args, " "))
}

func newAgentSender(f *agentFake) Sender {
	return Sender{
		Resolve: &fakeResolver{target: herdr.Target{Session: "fleet", Pane: "pane-7"}, meta: taskMeta("task-7", "claude")},
		Herdr:   &herdr.Client{Commands: f, Sleep: func(context.Context, time.Duration) error { return nil }},
		Sleep:   noSleep,
	}
}

// The message goes through the native agent channel and nothing is typed into
// the pane. Pinning the absence matters as much as the presence: typing into a
// composer and reading it back is the defect this replaces, and a harness that
// collapses a paste renders nothing to read back.
func TestSenderTextSubmitsNativelyAndNeverTypesIntoThePane(t *testing.T) {
	fake := &agentFake{}

	if err := newAgentSender(fake).Text(context.Background(), "task-7", "do the work"); err != nil {
		t.Fatalf("Text: %v", err)
	}
	if fake.promptCalls != 1 {
		t.Errorf("prompt submissions = %d, want exactly one", fake.promptCalls)
	}
	var prompted bool
	for _, args := range fake.requests {
		if len(args) >= 2 && args[0] == "pane" && (args[1] == "send-text" || args[1] == "send-keys") {
			t.Errorf("pane was typed into: %v", args)
		}
		if len(args) >= 4 && args[0] == "agent" && args[1] == "prompt" {
			prompted = true
			if args[3] != "do the work" {
				t.Errorf("prompt text = %q, want the message", args[3])
			}
		}
	}
	if !prompted {
		t.Fatal("no agent prompt was submitted, so this proves nothing about delivery")
	}
}

// Acceptance is proven, not assumed. A herdr that takes the prompt but whose
// agent never moves must be reported unconfirmed, or the CFO believes a
// message landed that the goblin never saw.
func TestSenderTextRefusesWhenTheAgentNeverAccepts(t *testing.T) {
	fake := &agentFake{inert: true}

	err := newAgentSender(fake).Text(context.Background(), "task-7", "do the work")
	if err == nil {
		t.Fatal("Text = nil, want an unconfirmed delivery")
	}
	if !strings.Contains(err.Error(), "unconfirmed") {
		t.Errorf("err = %v, want it reported unconfirmed", err)
	}
	// Premise: the prompt really was submitted, so the refusal is about
	// acceptance rather than about a submit that never happened.
	if fake.promptCalls == 0 {
		t.Error("no prompt was submitted, so the refusal proves nothing")
	}
	if fake.getCalls < 2 {
		t.Errorf("agent reads = %d, want the confirmation to have polled", fake.getCalls)
	}
}

// A revision advance alone is acceptance. This is the observed kimi shape: a
// prompt it accepted moved revision while state_change_seq stayed put and the
// status never reached working. A check pinned on state_change_seq would read
// that delivered message as lost.
func TestSenderTextAcceptsARevisionOnlyAdvance(t *testing.T) {
	fake := &agentFake{revisionOnly: true}

	if err := newAgentSender(fake).Text(context.Background(), "task-7", "do the work"); err != nil {
		t.Fatalf("Text: %v", err)
	}
	// Premise: the fake really did withhold the other two signals, so this
	// passed on the revision arm rather than on a status change.
	if fake.seq != 0 {
		t.Errorf("state_change_seq = %d, want it held at zero for this shape", fake.seq)
	}
	if fake.status == "working" {
		t.Error("status reached working, so this no longer tests a revision-only advance")
	}
}

// The prompt submits on success, so a re-send delivers the message twice. A
// slow agent is not a reason to repeat it.
func TestSenderTextSubmitsOnlyOnceWhileAcceptanceIsDelayed(t *testing.T) {
	fake := &agentFake{acceptAfterGets: 3}

	if err := newAgentSender(fake).Text(context.Background(), "task-7", "do the work"); err != nil {
		t.Fatalf("Text: %v", err)
	}
	if fake.promptCalls != 1 {
		t.Errorf("prompt submissions = %d, want one - a re-send delivers the message twice", fake.promptCalls)
	}
	if fake.getCalls <= 3 {
		t.Errorf("agent reads = %d, want acceptance to have been genuinely delayed", fake.getCalls)
	}
}

// An agent read refused while the harness is busy is transient, not a lost
// message: the prompt was already accepted by herdr.
func TestSenderTextSurvivesATransientAgentRead(t *testing.T) {
	fake := &agentFake{getRefusals: 2}

	if err := newAgentSender(fake).Text(context.Background(), "task-7", "do the work"); err != nil {
		t.Fatalf("Text: %v", err)
	}
	if fake.promptCalls != 1 {
		t.Errorf("prompt submissions = %d, want one - an unreadable agent is not a failed submit", fake.promptCalls)
	}
}

// A refused submit is a delivery failure and must carry herdr's own reason.
// Reporting it as unconfirmed would hide a message that was never sent behind
// language that suggests it might have been.
func TestSenderTextSurfacesARefusedSubmit(t *testing.T) {
	fake := &agentFake{promptRefusals: 1}

	err := newAgentSender(fake).Text(context.Background(), "task-7", "do the work")
	if err == nil {
		t.Fatal("Text = nil, want the refused submit surfaced")
	}
	if !strings.Contains(err.Error(), "pane busy") {
		t.Errorf("err = %v, want herdr's own reason", err)
	}
	if strings.Contains(err.Error(), "unconfirmed") {
		t.Errorf("err = %v, want a submit failure rather than an unconfirmed delivery", err)
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
