package spawn

import (
	"context"
	"fmt"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// SendNative delivers text to a native task's harness the way spawn delivers
// its instruction: typed into its terminal, submitted once the composer shows
// it, and delivered only once the harness shows it working. A slash or dollar
// command waits out the harness's completion popup before it is submitted,
// and is reported unconfirmed: /exit ends the harness and /model opens a
// picker, so no screen afterwards proves it ran.
func (s Service) SendNative(ctx context.Context, meta state.TaskMeta, text string) error {
	screens, ok := harness.NativeScreens(harness.Kind(meta.Harness))
	if !ok {
		return fmt.Errorf("send: native task %s runs %s, whose screens cfo cannot read", meta.ID, meta.Harness)
	}
	record, err := host.ReadRecord(s.StateDir, meta.ID)
	if err != nil {
		return fmt.Errorf("send: native task %s has no running terminal: %w", meta.ID, err)
	}
	if !fleet.IsCommand(text) {
		return s.deliverNativeInstruction(ctx, record, screens, text, meta.SpawnGen)
	}
	if err := s.submitNative(ctx, record, screens, text, fleet.TypeSettleFor(text)); err != nil {
		return err
	}
	return fmt.Errorf("send: %q was typed into native terminal %s and submitted once, but nothing on its screen confirms a command ran; check it with cfo peek %s rather than sending it again", text, meta.ID, meta.ID)
}

// nativeKeys are the keys cfo send --key names, as a console reads them.
var nativeKeys = map[string]string{"Enter": "\r", "Escape": "\x1b", "Ctrl+C": "\x03", "Ctrl+U": "\x15"}

// SendNativeKey sends one named key to a native task's terminal.
func (s Service) SendNativeKey(meta state.TaskMeta, key string) error {
	name, err := fleet.NormalizeKey(key)
	if err != nil {
		return err
	}
	record, err := host.ReadRecord(s.StateDir, meta.ID)
	if err != nil {
		return fmt.Errorf("send: native task %s has no running terminal: %w", meta.ID, err)
	}
	client, err := host.Dial(record)
	if err != nil {
		return fmt.Errorf("send: native terminal %s does not answer: %w", meta.ID, err)
	}
	defer client.Close()
	return client.Input([]byte(nativeKeys[name]))
}
