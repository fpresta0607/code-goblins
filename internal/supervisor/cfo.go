package supervisor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type primaryRegistration struct {
	Target    herdr.Target `json:"target"`
	Workspace string       `json:"workspace"`
	Tab       string       `json:"tab"`
	Agent     string       `json:"agent"`
	Terminal  string       `json:"terminal"`
	Process   lock.Info    `json:"process"`
}

type CFOView struct {
	Identity  string    `json:"identity"`
	Harness   string    `json:"harness"`
	Available bool      `json:"available"`
	Reason    string    `json:"reason"`
	Text      string    `json:"text"`
	At        time.Time `json:"at"`
}

// CFOConnection uses the existing operator-owned primary.json registration.
// Reading it grants no permission to guess a different pane or register one.
type CFOConnection struct {
	State string
	Herdr *herdr.Client
}

func decodePrimary(reader io.Reader) (primaryRegistration, string, error) {
	var primary primaryRegistration
	data, err := io.ReadAll(io.LimitReader(reader, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return primary, "", errors.New("primary CFO registration is unreadable or exceeds its limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&primary); err != nil {
		return primary, "", errors.New("primary CFO registration is invalid")
	}
	if decoder.Decode(new(json.RawMessage)) != io.EOF || primary.Target.Session == "" || primary.Target.Pane == "" || primary.Workspace == "" || primary.Tab == "" || primary.Terminal == "" || primary.Agent == "" || primary.Process.PID <= 0 || primary.Process.Start.IsZero() {
		return primary, "", errors.New("primary CFO registration has incomplete identity")
	}
	for _, value := range []string{primary.Target.Session, primary.Target.Pane, primary.Workspace, primary.Tab, primary.Terminal, primary.Agent} {
		if len(value) > 256 || strings.ContainsAny(value, "\x00\r\n") {
			return primary, "", errors.New("invalid primary CFO identity")
		}
	}
	sum := sha256.Sum256(data)
	return primary, hex.EncodeToString(sum[:]), nil
}

func (c *CFOConnection) verify(ctx context.Context, primary primaryRegistration) error {
	if c.Herdr == nil || !primary.Process.VerifiedAlive() {
		return errors.New("the registered CFO process is unavailable; ask the CFO to refresh its registration")
	}
	client := *c.Herdr
	client.Session = primary.Target.Session
	snapshot, err := client.Snapshot(ctx)
	if err != nil {
		return errors.New("Herdr cannot verify the registered CFO")
	}
	found := false
	for _, agent := range snapshot.Agents {
		if agent.PaneID == primary.Target.Pane && agent.TabID == primary.Tab && agent.WorkspaceID == primary.Workspace && agent.Agent == primary.Agent {
			found = true
		}
	}
	if !found {
		return errors.New("the registered CFO agent changed or is missing")
	}
	process, err := client.PaneProcessInfo(ctx, primary.Target)
	if err != nil || process.ForegroundProcessGroupID != primary.Process.PID || process.ForegroundProcessGroupID == process.ShellPID {
		return errors.New("the registered CFO process no longer owns its pane")
	}
	return nil
}

func (c *CFOConnection) Read(ctx context.Context) CFOView {
	out := CFOView{At: time.Now().UTC()}
	file, err := openPrimary(filepath.Join(c.State, "primary.json"))
	if err != nil {
		out.Reason = "Primary CFO registration is unavailable."
		return out
	}
	defer file.Close()
	primary, identity, err := decodePrimary(file)
	if err != nil {
		out.Reason = err.Error()
		return out
	}
	out.Identity, out.Harness = identity, primary.Agent
	if err := c.verify(ctx, primary); err != nil {
		out.Reason = err.Error()
		return out
	}
	text, err := c.Herdr.Capture(ctx, primary.Target, 120, false)
	if err != nil {
		out.Reason = "Native CFO output could not be read."
		return out
	}
	if err := c.verify(ctx, primary); err != nil {
		out.Reason = err.Error()
		return out
	}
	out.Available, out.Text = true, redact(bounded(text, 64<<10))
	out.Reason = "Native session output and verified message submission"
	return out
}

type primaryResolver struct {
	connection *CFOConnection
	primary    primaryRegistration
}

func (r primaryResolver) Resolve(ctx context.Context, _ string) (herdr.Target, state.TaskMeta, error) {
	if err := r.connection.verify(ctx, r.primary); err != nil {
		return herdr.Target{}, state.TaskMeta{}, fmt.Errorf("%w: %v", ErrRejected, err)
	}
	// Nonempty metadata makes registration mandatory in fleet.Sender. It can
	// never switch to the explicit-target raw shell typing fallback.
	return r.primary.Target, state.TaskMeta{HerdrPaneID: r.primary.Target.Pane}, nil
}

func (c *CFOConnection) Send(ctx context.Context, identity, text string) (Evaluation, error) {
	file, err := openPrimary(filepath.Join(c.State, "primary.json"))
	if err != nil {
		return Evaluation{}, fmt.Errorf("%w: primary CFO is unavailable", ErrRejected)
	}
	defer file.Close()
	primary, current, err := decodePrimary(file)
	if err != nil || current != identity {
		return Evaluation{}, fmt.Errorf("%w: the primary CFO changed; refresh before sending", ErrRejected)
	}
	guard := func(ctx context.Context, target herdr.Target, agent herdr.AgentDetail) error {
		if target != primary.Target || agent.Agent != primary.Agent {
			return errors.New("primary CFO agent identity changed")
		}
		return c.verify(ctx, primary)
	}
	sender := fleet.Sender{Herdr: c.Herdr, Resolve: primaryResolver{c, primary}, Guard: guard}
	if err := sender.Text(ctx, "primary-cfo", "Overlord: "+text); err != nil {
		return Evaluation{}, err
	}
	return Evaluation{Reason: "Accepted by the registered CFO through Herdr."}, nil
}
