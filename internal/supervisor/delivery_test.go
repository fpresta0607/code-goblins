package supervisor_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/wake"
	"github.com/fpresta0607/code-goblins/internal/watch"
)

// The executable adapter sees the same structural Herdr envelopes as Windows.
// Its process identity is this test process, never a live operator endpoint.
type primaryRunner struct {
	kind, status, terminal string
	prompts                int
	broken                 bool
	messages               []string
}

func (f *primaryRunner) Run(ctx context.Context, r execx.Request) (execx.Result, error) {
	if err := ctx.Err(); err != nil {
		return execx.Result{}, err
	}
	if f.broken {
		return execx.Result{}, errors.New("backend unavailable")
	}
	command := strings.Join(r.Args, " ")
	body := ""
	switch {
	case strings.HasPrefix(command, "api snapshot"):
		body = `{"result":{"type":"session_snapshot","snapshot":{"protocol":1,"panes":[{"pane_id":"p","tab_id":"t","workspace_id":"w"}]}}}`
	case strings.HasPrefix(command, "agent list"):
		body = fmt.Sprintf(`{"result":{"type":"agent_list","agents":[{"pane_id":"p","tab_id":"t","workspace_id":"w","agent":%q,"agent_status":%q,"terminal_id":%q}]}}`, f.kind, f.status, f.terminal)
	case strings.HasPrefix(command, "pane process-info"):
		body = fmt.Sprintf(`{"result":{"process_info":{"shell_pid":1,"foreground_process_group_id":%d}}}`, os.Getpid())
	case strings.HasPrefix(command, "pane get"):
		body = `{"result":{"pane":{"pane_id":"p"}}}`
	case strings.HasPrefix(command, "agent get"):
		body = fmt.Sprintf(`{"result":{"agent":{"agent":%q,"agent_status":%q,"revision":%d,"state_change_seq":%d}}}`, f.kind, f.status, 42+f.prompts, 42+f.prompts)
	case strings.HasPrefix(command, "agent prompt"):
		f.prompts++
		f.messages = append(f.messages, r.Args[3])
		f.status = "working"
		body = `{"result":{}}`
	case strings.HasPrefix(command, "pane read"):
		body = "Working"
		if f.kind == "codex" {
			body = "\n\x1b[1m›\x1b[0m\x1b[38;5;45m⠁\x1b[0m\x1b[2mAsk Codex to do anything\x1b[0m\n"
		}
		if f.kind == "codex" && len(f.messages) > 0 {
			body = "\nMessages to be submitted after next tool call\n  ↳ " + f.messages[len(f.messages)-1] + "\n\x1b[1m›\x1b[0m\x1b[2mAsk Codex to do anything\x1b[0m\n"
		}
	default:
		return execx.Result{}, fmt.Errorf("unexpected mutation: %s", command)
	}
	return execx.Result{Stdout: []byte(body)}, nil
}

func TestWorkerBlockerWakesPrimaryAfterTurnAcrossHarnesses(t *testing.T) {
	for _, kind := range []string{"claude", "codex", "pi", "kimi"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			h := home.Home{Root: dir, State: dir, Data: filepath.Join(dir, "data")}
			runner := &primaryRunner{kind: kind, status: "working", terminal: "fixture-terminal"}
			client := &herdr.Client{Commands: runner, Session: "fixture"}
			target := herdr.Target{Session: "fixture", Pane: "p"}
			if err := supervisor.Register(context.Background(), dir, target, client); err != nil {
				t.Fatal(err)
			}
			if err := state.AppendStatus(dir, "worker", "blocked: choose a supported gate path"); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			cycles := 0
			sender := fleet.Sender{Resolve: fleet.Resolver{StateDir: dir}, Herdr: client, RequireAgent: true, Sleep: func(context.Context, time.Duration) error { return nil }}
			svc := supervisor.Service{Home: h, Config: func() watch.Config {
				return watch.Config{Home: h, Poll: time.Millisecond, SignalGrace: time.Millisecond}
			}, Deliver: func(ctx context.Context) error {
				cycles++
				err := supervisor.DeliverPending(ctx, dir, client, func(ctx context.Context, target herdr.Target, message string) error {
					return sender.Text(ctx, target.String(), message)
				})
				if cycles == 1 {
					if runner.prompts != 0 && kind != "codex" {
						t.Error("typed into busy primary")
					}
					if kind != "codex" {
						runner.status = "idle"
					}
				}
				if cycles == 40 {
					cancel()
				}
				return err
			}}
			if err := svc.Run(ctx); err != nil {
				t.Fatal(err)
			}
			if runner.prompts != 1 || len(runner.messages) != 1 || !strings.Contains(runner.messages[0], "CFO wake") {
				receipt, _ := os.ReadFile(filepath.Join(dir, "delivery.json"))
				t.Fatalf("delivery %+v receipt %s", runner, receipt)
			}
			if kind == "codex" {
				data, err := os.ReadFile(filepath.Join(dir, "delivery.json"))
				var d supervisor.Delivery
				if err != nil || json.Unmarshal(data, &d) != nil || !strings.HasPrefix(d.Receipt, "submitted:") || runner.status != "working" {
					t.Fatalf("busy submission was deferred or called accepted: %s %v", data, err)
				}
			}
			pending, err := wake.Pending(dir)
			if err != nil || len(pending) != 1 {
				t.Fatalf("delivery acknowledged unresolved decision: %+v %v", pending, err)
			}
			// A process restart reads the same confirmed receipt and never resends it.
			runner.status = "idle"
			if err := supervisor.DeliverPending(context.Background(), dir, client, func(context.Context, herdr.Target, string) error { t.Fatal("duplicate after restart"); return nil }); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUncertainSubmissionAndReboundIdentityNeverResend(t *testing.T) {
	dir := t.TempDir()
	runner := &primaryRunner{kind: "codex", status: "idle", terminal: "original"}
	client := &herdr.Client{Commands: runner, Session: "fixture"}
	target := herdr.Target{Session: "fixture", Pane: "p"}
	if err := supervisor.Register(context.Background(), dir, target, client); err != nil {
		t.Fatal(err)
	}
	if _, err := wake.Append(dir, "notify", "worker", "blocked: preserve custody"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	send := func(context.Context, herdr.Target, string) error {
		calls++
		return errors.New("typed but not confirmed")
	}
	if err := supervisor.DeliverPending(context.Background(), dir, client, send); err != nil {
		t.Fatal(err)
	}
	runner.terminal = "replacement"
	if err := supervisor.DeliverPending(context.Background(), dir, client, send); err != nil {
		t.Fatal(err)
	}
	runner.terminal = "original"
	if err := supervisor.DeliverPending(context.Background(), dir, client, send); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("uncertain attempt resent %d times", calls)
	}
	data, err := os.ReadFile(filepath.Join(dir, "delivery.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d supervisor.Delivery
	if err = json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	if d.State != "submission_unknown" || len(d.Uncertain) != 1 {
		t.Fatalf("uncertain receipt lost: %+v", d)
	}
	if err := supervisor.ConfirmDelivery(dir, d.Sequence+1); err == nil {
		t.Fatal("confirmed another wake")
	}
	if err := supervisor.ConfirmDelivery(dir, d.Sequence); err != nil {
		t.Fatal(err)
	}
	pending, _ := wake.Pending(dir)
	if len(pending) != 1 {
		t.Fatal("confirmation acknowledged task decision")
	}
}

func TestUnsupportedBusyQueueBecomesOverdueWithoutSending(t *testing.T) {
	for _, kind := range []string{"claude", "pi", "kimi"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			runner := &primaryRunner{kind: kind, status: "working", terminal: "fixture"}
			client := &herdr.Client{Commands: runner, Session: "fixture"}
			if err := supervisor.Register(context.Background(), dir, herdr.Target{Session: "fixture", Pane: "p"}, client); err != nil {
				t.Fatal(err)
			}
			if _, err := wake.Append(dir, "notify", "worker", "blocked: choose next action"); err != nil {
				t.Fatal(err)
			}
			send := func(context.Context, herdr.Target, string) error {
				t.Fatal("unverified busy queue submission")
				return nil
			}
			if err := supervisor.DeliverPending(context.Background(), dir, client, send); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "delivery.json")
			data, err := os.ReadFile(path)
			var d supervisor.Delivery
			if err != nil || json.Unmarshal(data, &d) != nil || d.State != "waiting_for_primary" || d.WaitingSince.IsZero() {
				t.Fatalf("missing immediate deferred receipt: %s %v", data, err)
			}
			d.WaitingSince = time.Now().Add(-2 * time.Minute)
			data, _ = json.Marshal(d)
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			// A restarted observer retains the original delay and cannot call it healthy.
			if err := supervisor.DeliverPending(context.Background(), dir, client, send); err != nil {
				t.Fatal(err)
			}
			data, err = os.ReadFile(path)
			if err != nil || json.Unmarshal(data, &d) != nil || d.State != "notification_overdue" || d.Attempts != 0 {
				t.Fatalf("unbounded silent deferral: %s %v", data, err)
			}
			pending, err := wake.Pending(dir)
			if err != nil || len(pending) != 1 {
				t.Fatalf("lost blocked decision: %v %v", pending, err)
			}
		})
	}
}

func TestUncertainReceiptDoesNotBlockLaterWakeAndConcurrentConfirmation(t *testing.T) {
	dir := t.TempDir()
	runner := &primaryRunner{kind: "codex", status: "idle", terminal: "fixture"}
	client := &herdr.Client{Commands: runner, Session: "fixture"}
	target := herdr.Target{Session: "fixture", Pane: "p"}
	if err := supervisor.Register(context.Background(), dir, target, client); err != nil {
		t.Fatal(err)
	}
	first, err := wake.Append(dir, "notify", "first", "blocked: first decision")
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.DeliverPending(context.Background(), dir, client, func(context.Context, herdr.Target, string) error { return errors.New("receipt unknown") }); err != nil {
		t.Fatal(err)
	}
	second, err := wake.Append(dir, "notify", "second", "blocked: second decision")
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	delivered := make(chan error, 1)
	go func() {
		delivered <- supervisor.DeliverPending(context.Background(), dir, client, func(context.Context, herdr.Target, string) error {
			close(entered)
			<-release
			return nil
		})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("uncertain earlier receipt blocked independent wake")
	}
	confirmed, registered := make(chan error, 1), make(chan error, 1)
	go func() { confirmed <- supervisor.ConfirmDelivery(dir, first.Seq) }()
	go func() { registered <- supervisor.Register(context.Background(), dir, target, client) }()
	select {
	case err := <-confirmed:
		t.Fatalf("confirmation escaped active delivery lock: %v", err)
	case err := <-registered:
		t.Fatalf("registration escaped active delivery lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	for _, result := range []<-chan error{delivered, confirmed, registered} {
		if err := <-result; err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "delivery.json"))
	var d supervisor.Delivery
	if err != nil || json.Unmarshal(data, &d) != nil || d.Sequence != second.Seq || len(d.Uncertain) != 0 || d.Attempts != 2 {
		t.Fatalf("concurrent receipt update lost: %s %v", data, err)
	}
	pending, err := wake.Pending(dir)
	if err != nil || len(pending) != 2 {
		t.Fatalf("receipt confirmation acknowledged decisions: %v %v", pending, err)
	}
}

func TestConfirmingOlderUncertaintyPreservesCurrentAcceptedReceipt(t *testing.T) {
	dir := t.TempDir()
	confirmed := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	delivery := supervisor.Delivery{
		State:       "ready",
		Sequence:    2,
		Receipt:     "accepted: idle-agent acceptance observed",
		Detail:      "",
		ConfirmedAt: confirmed,
		CheckedAt:   confirmed,
		Uncertain:   map[int]string{1: "typed receipt unknown"},
	}
	data, err := json.Marshal(delivery)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "delivery.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := supervisor.ConfirmDelivery(dir, 1); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(dir, "delivery.json"))
	var reloaded supervisor.Delivery
	if err != nil || json.Unmarshal(data, &reloaded) != nil {
		t.Fatalf("delivery unreadable: %v", err)
	}
	if reloaded.Receipt != "accepted: idle-agent acceptance observed" || reloaded.Detail != "" || !reloaded.ConfirmedAt.Equal(confirmed) || !reloaded.CheckedAt.Equal(confirmed) {
		t.Fatalf("older confirmation relabeled current receipt: %+v", reloaded)
	}
	if len(reloaded.Uncertain) != 0 || reloaded.State != "ready" {
		t.Fatalf("older uncertainty was not cleared: %+v", reloaded)
	}
}

func TestHandledWakeAcknowledgementClearsOnlyCoveredUncertainty(t *testing.T) {
	dir := t.TempDir()
	runner := &primaryRunner{kind: "codex", status: "working", terminal: "fixture"}
	client := &herdr.Client{Commands: runner, Session: "fixture"}
	if err := supervisor.Register(context.Background(), dir, herdr.Target{Session: "fixture", Pane: "p"}, client); err != nil {
		t.Fatal(err)
	}
	calls := 0
	send := func(context.Context, herdr.Target, string) error {
		calls++
		return errors.New("callback delivered but terminal text was truncated")
	}
	var records []wake.Record
	for _, id := range []string{"first", "second"} {
		record, err := wake.Append(dir, "notify", id, "blocked: synthetic decision")
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
		if err := supervisor.DeliverPending(context.Background(), dir, client, send); err != nil {
			t.Fatal(err)
		}
	}
	read := func() supervisor.Delivery {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, "delivery.json"))
		var d supervisor.Delivery
		if err != nil || json.Unmarshal(data, &d) != nil {
			t.Fatalf("delivery unreadable: %v", err)
		}
		return d
	}
	if err := wake.AckThrough(dir, records[0].Seq); err != nil {
		t.Fatal(err)
	}
	runner.terminal = "replacement"
	if err := supervisor.DeliverPending(context.Background(), dir, client, send); err != nil {
		t.Fatal(err)
	}
	if d := read(); d.State != "notification_failed" || len(d.Uncertain) != 2 {
		t.Fatalf("ack bypassed primary identity validation: %+v", d)
	}
	runner.terminal = "fixture"
	if err := supervisor.DeliverPending(context.Background(), dir, client, send); err != nil {
		t.Fatal(err)
	}
	if d := read(); d.State != "submission_unknown" || len(d.Uncertain) != 1 || d.Uncertain[records[1].Seq] == "" {
		t.Fatalf("partial ack cleared a later uncertainty: %+v", d)
	}
	if err := wake.AckThrough(dir, records[1].Seq); err != nil {
		t.Fatal(err)
	}
	if err := supervisor.DeliverPending(context.Background(), dir, client, send); err != nil {
		t.Fatal(err)
	}
	if d := read(); d.State != "ready" || len(d.Uncertain) != 0 || !strings.HasPrefix(d.Receipt, "handled:") || calls != 2 {
		t.Fatalf("handled callback remained uncertain or resent: %+v calls=%d", d, calls)
	}
}

func TestReplacementPrimaryReceivesPendingWakeOnce(t *testing.T) {
	dir := t.TempDir()
	runner := &primaryRunner{kind: "codex", status: "idle", terminal: "primary-a"}
	client := &herdr.Client{Commands: runner, Session: "fixture"}
	target := herdr.Target{Session: "fixture", Pane: "p"}
	if err := supervisor.Register(context.Background(), dir, target, client); err != nil {
		t.Fatal(err)
	}
	if _, err := wake.Append(dir, "notify", "worker", "blocked: still needs a decision"); err != nil {
		t.Fatal(err)
	}
	var recipients []string
	send := func(context.Context, herdr.Target, string) error {
		recipients = append(recipients, runner.terminal)
		return nil // accepted, but the task decision is still unacknowledged
	}
	if err := supervisor.DeliverPending(context.Background(), dir, client, send); err != nil {
		t.Fatal(err)
	}
	// Re-registering and restarting the observer for A must not duplicate input.
	if err := supervisor.Register(context.Background(), dir, target, client); err != nil {
		t.Fatal(err)
	}
	if err := supervisor.DeliverPending(context.Background(), dir, client, send); err != nil {
		t.Fatal(err)
	}
	runner.terminal = "primary-b"
	if err := supervisor.Register(context.Background(), dir, target, client); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := supervisor.DeliverPending(context.Background(), dir, client, send); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(recipients, ",") != "primary-a,primary-b" {
		t.Fatalf("replacement delivery lost or duplicated: %v", recipients)
	}
	data, err := os.ReadFile(filepath.Join(dir, "delivery.previous.json"))
	var prior supervisor.Delivery
	if err != nil || json.Unmarshal(data, &prior) != nil || prior.Primary == nil || prior.Primary.Terminal != "primary-a" {
		t.Fatalf("prior receipt custody lost: %s %v", data, err)
	}
	pending, err := wake.Pending(dir)
	if err != nil || len(pending) != 1 {
		t.Fatalf("replacement acknowledged decision: %v %v", pending, err)
	}
}
