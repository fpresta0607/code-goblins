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

// fakeStartCounters is where the fake's monotonic counters begin: well above
// zero, like a goblin that has already been working for a while. A
// confirmation that measured against a guessed zero baseline would read the
// very first post-submit number as an advance, so that regression fails these
// tests loudly instead of passing them.
const fakeStartCounters = 42

var fakeTarget = herdr.Target{Session: "fleet", Pane: "pane-7"}

// agentFake is a stateful herdr whose agent counters advance ONLY for a prompt
// it actually accepted. That is the load-bearing property of every test below:
// a fake that advanced them on any read would report an undelivered message as
// delivered, and each of these tests would pass while proving nothing.
//
// It tells the three kinds of `agent get` apart the way herdr's own calls do:
// a registration probe always follows a `pane get`, a read before the prompt
// is the baseline, and a read after it is a confirmation. Each kind can be
// refused independently, so no refusal shape is routed around a call by
// accident.
type agentFake struct {
	requests [][]string

	// unregistered answers every `agent get` with herdr's own agent_not_found,
	// the shape of a pane holding no registered agent.
	unregistered bool
	// deregisterAfterPrompt keeps the agent registered until the prompt is
	// accepted and then loses it, the shape of a harness that exited on the
	// message it was just given.
	deregisterAfterPrompt bool
	// promptRefusals refuses that many `agent prompt` calls before accepting.
	promptRefusals int
	// probeRefusals refuses that many registration probes.
	probeRefusals int
	// baselineRefusals refuses that many pre-submit baseline reads.
	baselineRefusals int
	// baselineUnreadable refuses every baseline read, so the baseline can
	// never be established at all.
	baselineUnreadable bool
	// confirmRefusals refuses that many post-submit confirmation reads,
	// modelling a herdr that is momentarily busy rather than a lost message.
	confirmRefusals int
	// acceptAfterGets delays the advance until that many confirmation reads
	// have happened.
	acceptAfterGets int
	// inert accepts the prompt but never moves - the shape a harness that
	// swallowed the text would produce.
	inert bool
	// revisionOnly moves revision without state_change_seq and without
	// leaving idle. This is the observed kimi shape, not a hypothetical.
	revisionOnly bool

	probing         bool
	accepted        bool
	promptCalls     int
	probeGets       int
	baselineGets    int
	confirmGets     int
	notFoundReplies int
	seq             int64
	revision        int64
	status          string
}

func newAgentFake(shape agentFake) *agentFake {
	shape.seq, shape.revision = fakeStartCounters, fakeStartCounters
	return &shape
}

func (f *agentFake) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	f.requests = append(f.requests, request.Args)
	args := request.Args
	switch {
	case len(args) >= 3 && args[0] == "pane" && args[1] == "get":
		f.probing = true
		return execx.Result{Stdout: []byte(`{"result":{"pane":{"pane_id":"` + args[2] + `"}}}`)}, nil
	case len(args) >= 4 && args[0] == "pane" && (args[1] == "send-text" || args[1] == "send-keys"):
		return execx.Result{Stdout: []byte(`{"result":{}}`)}, nil
	case len(args) >= 3 && args[0] == "agent" && args[1] == "prompt":
		f.promptCalls++
		if f.promptRefusals > 0 {
			f.promptRefusals--
			return execx.Result{ExitCode: 1, Stderr: []byte("agent prompt: pane busy")}, nil
		}
		f.accepted = true
		return execx.Result{Stdout: []byte(`{"result":{"agent":{"agent":"claude","agent_status":"idle"}}}`)}, nil
	case len(args) >= 3 && args[0] == "agent" && args[1] == "get":
		probing := f.probing
		f.probing = false
		switch {
		case probing:
			f.probeGets++
		case f.accepted:
			f.confirmGets++
		default:
			f.baselineGets++
		}
		if f.unregistered || (f.deregisterAfterPrompt && f.accepted) {
			f.notFoundReplies++
			return execx.Result{ExitCode: 1, Stdout: []byte(`{"error":{"code":"agent_not_found"}}`)}, nil
		}
		switch {
		case probing:
			if f.probeRefusals > 0 {
				f.probeRefusals--
				return busyRead(), nil
			}
		case f.accepted:
			if f.confirmRefusals > 0 {
				f.confirmRefusals--
				return busyRead(), nil
			}
			if !f.inert && f.confirmGets > f.acceptAfterGets {
				if f.revisionOnly {
					f.revision++
				} else {
					f.seq++
					f.revision++
					f.status = "working"
				}
			}
		default:
			if f.baselineUnreadable {
				return busyRead(), nil
			}
			if f.baselineRefusals > 0 {
				f.baselineRefusals--
				return busyRead(), nil
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

func busyRead() execx.Result {
	return execx.Result{ExitCode: 1, Stderr: []byte("agent get: pane busy")}
}

func (f *agentFake) typedRequests() (typed bool, submitted bool) {
	for _, args := range f.requests {
		if len(args) >= 4 && args[0] == "pane" && args[1] == "send-text" {
			typed = true
		}
		if len(args) >= 4 && args[0] == "pane" && args[1] == "send-keys" {
			submitted = true
		}
	}
	return typed, submitted
}

func senderFor(f *agentFake, resolve TargetResolver) Sender {
	return Sender{
		Resolve: resolve,
		Herdr:   &herdr.Client{Commands: f, Sleep: noSleep},
		Sleep:   noSleep,
	}
}

// newAgentSender addresses the pane through a task selector, which is how the
// CFO steers a goblin.
func newAgentSender(f *agentFake) Sender {
	return senderFor(f, &fakeResolver{target: fakeTarget, meta: taskMeta("task-7", "claude")})
}

// newExplicitPaneSender addresses the pane directly as <session>:<pane-id>,
// which carries no task metadata.
func newExplicitPaneSender(f *agentFake) Sender {
	return senderFor(f, &fakeResolver{target: fakeTarget})
}

// The message goes through the native agent channel and nothing is typed into
// the pane. Pinning the absence matters as much as the presence: typing into a
// composer and reading it back is the defect this replaces, and a harness that
// collapses a paste renders nothing to read back.
func TestSenderTextSubmitsNativelyAndNeverTypesIntoThePane(t *testing.T) {
	fake := newAgentFake(agentFake{})

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
	// Premise: acceptance was measured against a real read of the agent's own
	// counters, not against a guessed zero.
	if fake.baselineGets == 0 {
		t.Error("no baseline read was taken, so the confirmation had nothing to measure against")
	}
}

// Acceptance is proven, not assumed. A herdr that takes the prompt but whose
// agent never moves must be reported unconfirmed, or the CFO believes a
// message landed that the goblin never saw.
func TestSenderTextRefusesWhenTheAgentNeverAccepts(t *testing.T) {
	fake := newAgentFake(agentFake{inert: true})

	err := newAgentSender(fake).Text(context.Background(), "task-7", "do the work")
	if err == nil {
		t.Fatal("Text = nil, want an unconfirmed delivery")
	}
	if !strings.Contains(err.Error(), "unconfirmed") {
		t.Errorf("err = %v, want it reported unconfirmed", err)
	}
	// Premise: the prompt really was submitted against a real baseline, so the
	// refusal is about acceptance rather than about a submit that never
	// happened or a baseline that was guessed.
	if fake.promptCalls == 0 {
		t.Error("no prompt was submitted, so the refusal proves nothing")
	}
	if fake.baselineGets == 0 {
		t.Error("no baseline read was taken, so the refusal proves nothing")
	}
	if fake.confirmGets < 2 {
		t.Errorf("confirmation reads = %d, want the confirmation to have polled", fake.confirmGets)
	}
}

// A revision advance alone is acceptance. This is the observed kimi shape: a
// prompt it accepted moved revision while state_change_seq stayed put and the
// status never reached working. A check pinned on state_change_seq would read
// that delivered message as lost.
func TestSenderTextAcceptsARevisionOnlyAdvance(t *testing.T) {
	fake := newAgentFake(agentFake{revisionOnly: true})

	if err := newAgentSender(fake).Text(context.Background(), "task-7", "do the work"); err != nil {
		t.Fatalf("Text: %v", err)
	}
	// Premise: the fake really did withhold the other two signals, so this
	// passed on the revision arm rather than on a status change.
	if fake.seq != fakeStartCounters {
		t.Errorf("state_change_seq = %d, want it held at %d for this shape", fake.seq, fakeStartCounters)
	}
	if fake.status == "working" {
		t.Error("status reached working, so this no longer tests a revision-only advance")
	}
}

// The prompt submits on success, so a re-send delivers the message twice. A
// slow agent is not a reason to repeat it.
func TestSenderTextSubmitsOnlyOnceWhileAcceptanceIsDelayed(t *testing.T) {
	fake := newAgentFake(agentFake{acceptAfterGets: 3})

	if err := newAgentSender(fake).Text(context.Background(), "task-7", "do the work"); err != nil {
		t.Fatalf("Text: %v", err)
	}
	if fake.promptCalls != 1 {
		t.Errorf("prompt submissions = %d, want one - a re-send delivers the message twice", fake.promptCalls)
	}
	if fake.confirmGets <= 3 {
		t.Errorf("confirmation reads = %d, want acceptance to have been genuinely delayed", fake.confirmGets)
	}
}

// A confirmation read refused while the harness is busy is transient, not a
// lost message: the prompt was already accepted by herdr, and the baseline it
// is measured against was read before the submit.
func TestSenderTextSurvivesATransientConfirmationRead(t *testing.T) {
	fake := newAgentFake(agentFake{confirmRefusals: 2})

	if err := newAgentSender(fake).Text(context.Background(), "task-7", "do the work"); err != nil {
		t.Fatalf("Text: %v", err)
	}
	if fake.promptCalls != 1 {
		t.Errorf("prompt submissions = %d, want one - an unreadable agent is not a failed submit", fake.promptCalls)
	}
	// Premise: the refusals landed on the confirmation, not on the baseline,
	// so this proves a real counter advance rather than a free pass from a
	// baseline that was never read.
	if fake.baselineGets == 0 {
		t.Error("no baseline read was taken, so the advance was measured against nothing")
	}
	if fake.confirmGets < 3 {
		t.Errorf("confirmation reads = %d, want the refusals to have been survived", fake.confirmGets)
	}
}

// The registration probe is the first thing a send does and it faces the same
// momentarily busy herdr as every later read, so it gets the same retries. A
// probe refused once must not cost the whole send.
func TestSenderTextSurvivesATransientRegistrationProbe(t *testing.T) {
	fake := newAgentFake(agentFake{probeRefusals: 2})

	if err := newAgentSender(fake).Text(context.Background(), "task-7", "do the work"); err != nil {
		t.Fatalf("Text: %v", err)
	}
	if fake.probeGets < 3 {
		t.Errorf("registration probes = %d, want the refusals to have been retried through", fake.probeGets)
	}
	if fake.promptCalls != 1 {
		t.Errorf("prompt submissions = %d, want one", fake.promptCalls)
	}
}

// A momentarily busy herdr costs the baseline read a retry, not its baseline.
func TestSenderTextRetriesABusyBaselineRead(t *testing.T) {
	fake := newAgentFake(agentFake{baselineRefusals: 2})

	if err := newAgentSender(fake).Text(context.Background(), "task-7", "do the work"); err != nil {
		t.Fatalf("Text: %v", err)
	}
	if fake.baselineGets < 3 {
		t.Errorf("baseline reads = %d, want the refusals to have been retried through", fake.baselineGets)
	}
	if fake.promptCalls != 1 {
		t.Errorf("prompt submissions = %d, want one", fake.promptCalls)
	}
}

// A baseline that can never be read leaves nothing to measure acceptance
// against, and a guessed zero would confirm anything a long-running goblin
// reports. Nothing is submitted at all: refusing a send the caller can repeat
// beats delivering one that cannot be proven.
func TestSenderTextRefusesAnUnreadableBaselineWithoutSubmitting(t *testing.T) {
	fake := newAgentFake(agentFake{baselineUnreadable: true})

	err := newAgentSender(fake).Text(context.Background(), "task-7", "do the work")
	if err == nil {
		t.Fatal("Text = nil, want the unprovable send refused")
	}
	if !strings.Contains(err.Error(), "nothing was sent") {
		t.Errorf("err = %v, want it to say the message was not sent", err)
	}
	if fake.promptCalls != 0 {
		t.Errorf("prompt submissions = %d, want none - acceptance could not be proven", fake.promptCalls)
	}
	if fake.baselineGets < 2 {
		t.Errorf("baseline reads = %d, want the read retried before giving up", fake.baselineGets)
	}
}

// A goblin addressed by its task selector is addressed through its agent or
// not at all. When herdr holds no agent for the pane the steer is refused,
// because typing it into whatever the pane now holds - a bash prompt, or a
// live harness whose detection manifest went stale - would report a delivery
// nothing can back up.
func TestSenderTextRefusesATaskSelectorWhoseAgentIsGone(t *testing.T) {
	fake := newAgentFake(agentFake{unregistered: true})

	err := newAgentSender(fake).Text(context.Background(), "task-7", "do the work")
	if err == nil {
		t.Fatal("Text = nil, want the send refused")
	}
	if !strings.Contains(err.Error(), "no registered agent") {
		t.Errorf("err = %v, want it to name the missing agent", err)
	}
	if fake.promptCalls != 0 {
		t.Errorf("prompt submissions = %d, want none", fake.promptCalls)
	}
	typed, submitted := fake.typedRequests()
	if typed || submitted {
		t.Errorf("the task pane was typed into: requests=%v", fake.requests)
	}
	// Premise: the fake really did report the agent gone, so this exercised
	// the unregistered branch rather than falling through to the native one.
	if fake.notFoundReplies == 0 {
		t.Error("the fake never reported the agent missing, so this proves nothing")
	}
}

// A pane the caller addressed directly as <session>:<pane-id> has no goblin
// behind it, so the text is typed and Enter submits it - the shape left behind
// by `cfo send <id> "/exit"`. It is reported unconfirmed rather than sent: an
// unregistered pane has nothing to prove acceptance with, and the pane is
// never read back to invent a proof.
func TestSenderTextTypesIntoAnExplicitPaneAndReportsUnconfirmed(t *testing.T) {
	fake := newAgentFake(agentFake{unregistered: true})

	err := newExplicitPaneSender(fake).Text(context.Background(), "fleet:pane-7", "/exit")
	if err == nil {
		t.Fatal("Text = nil, want the typed delivery reported unconfirmed")
	}
	if !strings.Contains(err.Error(), "unconfirmed") || !strings.Contains(err.Error(), "no registered agent") {
		t.Errorf("err = %v, want an unconfirmed delivery naming the missing agent", err)
	}
	if fake.promptCalls != 0 {
		t.Errorf("prompt submissions = %d, want none - the pane holds no agent to prompt", fake.promptCalls)
	}
	var typed, submitted bool
	for _, args := range fake.requests {
		if len(args) >= 2 && args[0] == "pane" && args[1] == "read" {
			t.Errorf("the pane was read back: %v", args)
		}
		if len(args) >= 4 && args[0] == "pane" && args[1] == "send-text" {
			typed = true
			if args[3] != "/exit" {
				t.Errorf("typed text = %q, want the message", args[3])
			}
		}
		if len(args) >= 4 && args[0] == "pane" && args[1] == "send-keys" {
			submitted = true
			if args[3] != "enter" {
				t.Errorf("submit key = %q, want enter", args[3])
			}
		}
	}
	if !typed {
		t.Error("nothing was typed into the pane, so the message was never delivered")
	}
	if !submitted {
		t.Error("the typed text was never submitted")
	}
	// Premise: the fake really did report the agent gone, so this exercised
	// the unregistered branch rather than falling through to the native one.
	if fake.notFoundReplies == 0 {
		t.Error("the fake never reported the agent missing, so this proves nothing")
	}
}

// A message that opens a harness completion popup gets a longer wait before
// Enter. A pane with no registered agent is not necessarily at a shell prompt,
// and an Enter that lands while the popup is open selects the highlighted
// completion - so `/exit` would run a different command, with nothing in the
// unconfirmed result to distinguish that from an ordinary typed delivery.
func TestSenderTextWaitsForACompletionPopupBeforeSubmitting(t *testing.T) {
	for _, test := range []struct {
		name    string
		message string
		want    time.Duration
	}{
		{name: "slash command", message: "/exit", want: completionSettle},
		{name: "dollar prefix", message: "$env:FOO", want: completionSettle},
		{name: "plain text", message: "status please", want: typeSettle},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := newAgentFake(agentFake{unregistered: true})
			var sleeps []time.Duration
			sender := newExplicitPaneSender(fake)
			sender.Sleep = func(_ context.Context, waited time.Duration) error {
				sleeps = append(sleeps, waited)
				return nil
			}

			if err := sender.Text(context.Background(), "fleet:pane-7", test.message); err == nil {
				t.Fatal("Text = nil, want the typed delivery reported unconfirmed")
			}
			// Premise: the typed path ran, so the single wait recorded is the
			// one between typing and Enter rather than a read retry.
			typed, submitted := fake.typedRequests()
			if !typed || !submitted {
				t.Fatalf("the message was not typed and submitted: requests=%v", fake.requests)
			}
			if len(sleeps) != 1 {
				t.Fatalf("waits = %v, want exactly the one before Enter", sleeps)
			}
			if sleeps[0] != test.want {
				t.Errorf("wait before Enter = %v, want %v", sleeps[0], test.want)
			}
		})
	}
}

// An agent that is gone will never report accepting anything, so the
// confirmation says so instead of spending its whole budget on a certain
// answer and then blaming a refused read.
func TestSenderTextReportsAGoneAgentInsteadOfBurningTheConfirmBudget(t *testing.T) {
	fake := newAgentFake(agentFake{deregisterAfterPrompt: true})

	err := newAgentSender(fake).Text(context.Background(), "task-7", "do the work")
	if err == nil {
		t.Fatal("Text = nil, want the lost agent reported")
	}
	if !strings.Contains(err.Error(), "no longer holds a registered agent") {
		t.Errorf("err = %v, want the lost agent named as the reason", err)
	}
	// Premise: the prompt was submitted against a live agent, and the
	// confirmation bailed out rather than polling the budget away.
	if fake.promptCalls != 1 {
		t.Errorf("prompt submissions = %d, want one", fake.promptCalls)
	}
	if fake.confirmGets != 1 {
		t.Errorf("confirmation reads = %d, want the loop to stop on the first proof the agent is gone", fake.confirmGets)
	}
}

// A refused submit is a delivery failure and must carry herdr's own reason.
// Reporting it as unconfirmed would hide a message that was never sent behind
// language that suggests it might have been.
func TestSenderTextSurfacesARefusedSubmit(t *testing.T) {
	fake := newAgentFake(agentFake{promptRefusals: 1})

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
			sender := Sender{Resolve: &fakeResolver{target: fakeTarget}, Herdr: newHerdrClient(runner, &clientSleeps)}

			if err := sender.Key(context.Background(), "task-7", test.key); err != nil {
				t.Fatalf("Key: %v", err)
			}
			assertRequests(t, runner.requests, [][]string{{"pane", "send-keys", "pane-7", test.want, "--session", "fleet"}})
		})
	}

	runner := &fakeRunner{}
	var clientSleeps []time.Duration
	sender := Sender{Resolve: &fakeResolver{target: fakeTarget}, Herdr: newHerdrClient(runner, &clientSleeps)}
	assertErrorContains(t, sender.Key(context.Background(), "task-7", "F1"), "unsupported key")
	if len(runner.requests) != 0 {
		t.Errorf("unsupported key made Herdr requests: %#v", runner.requests)
	}
}

func noSleep(context.Context, time.Duration) error {
	return nil
}

func TestSenderRequiresCollaborators(t *testing.T) {
	assertErrorContains(t, (Sender{}).Text(context.Background(), "task-7", "message"), "resolver")
	assertErrorContains(t, (Sender{Resolve: &fakeResolver{}}).Text(context.Background(), "task-7", "message"), "Herdr")
}
