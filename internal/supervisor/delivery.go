package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

type Primary struct {
	Target    herdr.Target `json:"target"`
	Workspace string       `json:"workspace"`
	Tab       string       `json:"tab"`
	Agent     string       `json:"agent"`
	Terminal  string       `json:"terminal"`
	Process   lock.Info    `json:"process"`
}

type Delivery struct {
	Primary          *Primary       `json:"primary,omitempty"`
	CheckedAt        time.Time      `json:"checked_at"`
	State            string         `json:"state"`
	Detail           string         `json:"detail,omitempty"`
	Sequence         int            `json:"sequence"`
	Attempts         int            `json:"attempts"`
	ConfirmedAt      time.Time      `json:"confirmed_at,omitempty"`
	Receipt          string         `json:"receipt,omitempty"`
	WaitingSince     time.Time      `json:"waiting_since,omitempty"`
	DeferredSequence int            `json:"deferred_sequence,omitempty"`
	Unconfirmed      bool           `json:"unconfirmed"`
	Uncertain        map[int]string `json:"uncertain,omitempty"`
}

func ReadPrimary(dir string) (Primary, error) {
	var p Primary
	data, err := os.ReadFile(filepath.Join(dir, "primary.json"))
	if err != nil {
		return p, err
	}
	err = json.Unmarshal(data, &p)
	if err == nil && (p.Target.Session == "" || p.Target.Pane == "" || p.Terminal == "" || p.Process.Start.IsZero()) {
		err = errors.New("primary: incomplete registration")
	}
	return p, err
}

func Register(ctx context.Context, dir string, target herdr.Target, client *herdr.Client) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	release, err := deliveryLock(ctx, dir)
	if err != nil {
		return err
	}
	defer release()
	p, _, err := inspectPrimary(ctx, target, client)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return writeDeliveryFile(dir, "primary.json", p)
}

func inspectPrimary(ctx context.Context, target herdr.Target, client *herdr.Client) (Primary, string, error) {
	if client.EffectiveSession() != target.Session {
		return Primary{}, "", errors.New("primary: client session mismatch")
	}
	snapshot, err := client.Snapshot(ctx)
	if err != nil {
		return Primary{}, "", err
	}
	agents, err := client.AgentList(ctx)
	if err != nil {
		return Primary{}, "", err
	}
	count := 0
	for _, agent := range agents {
		if agent.PaneID == target.Pane {
			count++
		}
	}
	if count != 1 {
		return Primary{}, "", errors.New("primary: missing or ambiguous registered agent identity")
	}
	for _, agent := range agents {
		if agent.PaneID != target.Pane {
			continue
		}
		switch agent.Agent {
		case "claude", "codex", "pi", "kimi":
		default:
			return Primary{}, "", errors.New("primary: unsupported harness")
		}
		matched := 0
		for _, pane := range snapshot.Panes {
			if pane.ID == target.Pane && pane.TabID == agent.TabID && pane.WorkspaceID == agent.WorkspaceID {
				matched++
			}
		}
		if matched != 1 || agent.TerminalID == "" {
			return Primary{}, "", errors.New("primary: pane association or terminal identity unavailable")
		}
		info, err := client.PaneProcessInfo(ctx, target)
		if err != nil {
			return Primary{}, "", err
		}
		if info.ShellPID == info.ForegroundProcessGroupID {
			return Primary{}, "", errors.New("primary: restored shell has no live foreground harness")
		}
		process, err := lock.VerifiedProcess(info.ForegroundProcessGroupID)
		if err != nil {
			return Primary{}, "", err
		}
		return Primary{Target: target, Workspace: agent.WorkspaceID, Tab: agent.TabID, Agent: agent.Agent, Terminal: agent.TerminalID, Process: process}, agent.Status, nil
	}
	return Primary{}, "", errors.New("primary: registered agent missing")
}

func samePrimary(a, b Primary) bool {
	return a.Target == b.Target && a.Workspace == b.Workspace && a.Tab == b.Tab && a.Agent == b.Agent && a.Terminal == b.Terminal && a.Process.PID == b.Process.PID && a.Process.Start.Equal(b.Process.Start) && a.Process.Hostname == b.Process.Hostname
}

// DeliverPending never acknowledges a task decision. It records the attempt
// before submitting once, so a crash cannot silently duplicate the instruction.
// Busy Codex can prove native queue submission; other busy adapters explicitly
// defer without typing, with an overdue state after one minute.
func DeliverPending(ctx context.Context, dir string, client *herdr.Client, send func(context.Context, herdr.Target, string) error) error {
	release, err := deliveryLock(ctx, dir)
	if err != nil {
		return err
	}
	defer release()
	var d Delivery
	data, err := os.ReadFile(filepath.Join(dir, "delivery.json"))
	if err == nil {
		if err = json.Unmarshal(data, &d); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	d.CheckedAt = time.Now().UTC()
	if d.Uncertain == nil {
		d.Uncertain = map[int]string{}
	}
	if d.Unconfirmed {
		d.Uncertain[d.Sequence] = "interrupted submission; inspect primary before confirming"
		d.Unconfirmed = false
	}
	save := func(state, detail string) error {
		d.State = state
		d.Detail = detail
		return writeDeliveryFile(dir, "delivery.json", d)
	}
	registered, err := ReadPrimary(dir)
	if err != nil {
		return save("notification_failed", "primary registration missing or unreadable; run cfo supervisor register <session:pane>")
	}
	current, status, err := inspectPrimary(ctx, registered.Target, client)
	if err != nil {
		return save("notification_failed", err.Error())
	}
	if !samePrimary(registered, current) {
		return save("notification_failed", "primary identity changed; explicitly register the new session")
	}
	if d.Primary == nil && d.Sequence > 0 {
		return save("notification_failed", "delivery receipt lacks primary generation; inspect retained receipt before explicit recovery")
	}
	if d.Primary != nil && !samePrimary(*d.Primary, current) {
		// Explicit registration authorizes delivery to a replacement recipient,
		// not blind retry to the old one. Preserve one prior generation's ledger.
		if err := writeDeliveryFile(dir, "delivery.previous.json", d); err != nil {
			return err
		}
		d = Delivery{CheckedAt: time.Now().UTC(), Uncertain: map[int]string{}}
	}
	d.Primary = &current
	acknowledged, err := wake.AcknowledgedThrough(dir)
	if err != nil {
		return err
	}
	// A recipient can handle a real callback whose wrapped terminal text did
	// not prove submission. Its guarded outcome acknowledgement is stronger
	// evidence than that uncertain terminal receipt, for covered sequences only.
	for seq := range d.Uncertain {
		if seq <= acknowledged {
			delete(d.Uncertain, seq)
			if seq == d.Sequence {
				d.Receipt = "handled: recipient acknowledged through cfo drain"
				d.ConfirmedAt = time.Now().UTC()
			}
		}
	}
	pending, err := wake.Pending(dir)
	if err != nil {
		return err
	}
	var next *wake.Record
	for i := range pending {
		if pending[i].Seq > d.Sequence {
			next = &pending[i]
			break
		}
	}
	if next == nil {
		d.WaitingSince = time.Time{}
		d.DeferredSequence = 0
		if len(d.Uncertain) > 0 {
			return save("submission_unknown", "inspect uncertain sequences and cfo drain; supervisor confirm-delivery <sequence> records verified submission only")
		}
		return save("ready", "")
	}
	if status == "working" && registered.Agent != "codex" {
		if d.DeferredSequence != next.Seq || d.WaitingSince.IsZero() {
			d.DeferredSequence = next.Seq
			d.WaitingSince = time.Now().UTC()
		}
		state := "waiting_for_primary"
		if time.Since(d.WaitingSince) >= time.Minute {
			state = "notification_overdue"
		}
		return save(state, "busy queue submission is unverified for "+registered.Agent+"; wake is retained, not delivered; inspect primary and drain without resending blindly")
	}
	if status != "idle" && status != "done" && !(status == "working" && registered.Agent == "codex") {
		return save("notification_failed", "primary is not ready for input")
	}
	d.Sequence = next.Seq
	d.WaitingSince = time.Time{}
	d.DeferredSequence = 0
	d.Attempts++
	d.Unconfirmed = true
	d.Receipt = "attempting"
	if err = save("sending", ""); err != nil {
		return err
	}
	message := fmt.Sprintf("CFO wake %d (%s), state directory %q. Use cfo drain and cfo context with CFO_STATE_OVERRIDE pointing to that directory before deciding. %s: %s", next.Seq, next.Kind, dir, next.Key, next.Detail)
	if send == nil {
		return save("submission_unknown", "no delivery adapter configured")
	}
	if err = send(ctx, registered.Target, message); err != nil {
		var receipt interface{ SubmissionConfirmed() bool }
		if errors.As(err, &receipt) && receipt.SubmissionConfirmed() {
			d.Unconfirmed = false
			d.Receipt = "submitted: visible in native queue; acceptance unproven"
			d.ConfirmedAt = time.Now().UTC()
			return save("ready", "")
		}
		d.Uncertain[d.Sequence] = err.Error()
		d.Unconfirmed = false
		d.Receipt = "uncertain"
		return save("submission_unknown", err.Error())
	}
	d.ConfirmedAt = time.Now().UTC()
	d.Unconfirmed = false
	d.Receipt = "accepted: idle-agent acceptance observed"
	return save("ready", "")
}

// ConfirmDelivery records the operator's verified delivery receipt, not a
// decision acknowledgement. The unresolved wake queue remains unchanged.
func ConfirmDelivery(dir string, seq int) error {
	release, err := deliveryLock(context.Background(), dir)
	if err != nil {
		return err
	}
	defer release()
	data, err := os.ReadFile(filepath.Join(dir, "delivery.json"))
	if err != nil {
		return err
	}
	var d Delivery
	if err = json.Unmarshal(data, &d); err != nil {
		return err
	}
	if !(d.Sequence == seq && d.Unconfirmed) && d.Uncertain[seq] == "" {
		return errors.New("delivery: no matching uncertain attempt")
	}
	if d.Sequence == seq {
		d.Unconfirmed = false
		d.Detail = "operator verified submission"
		d.Receipt = "submitted: operator verified; acceptance unproven"
		d.ConfirmedAt = time.Now().UTC()
		d.CheckedAt = d.ConfirmedAt
	}
	delete(d.Uncertain, seq)
	d.State = "ready"
	if len(d.Uncertain) > 0 {
		d.State = "submission_unknown"
	}
	return writeDeliveryFile(dir, "delivery.json", d)
}

func deliveryLock(ctx context.Context, dir string) (func(), error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for {
		_, err := lock.AcquireExclusiveNamed(dir, ".primary-delivery.lock")
		if err == nil {
			return func() { _ = lock.ReleaseExclusiveNamed(dir, ".primary-delivery.lock") }, nil
		}
		if !errors.Is(err, lock.ErrHeld) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("primary delivery state busy; retry after the current bounded attempt: %w", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func writeDeliveryFile(dir, name string, value interface{}) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(dir, name), append(data, '\n'))
}
