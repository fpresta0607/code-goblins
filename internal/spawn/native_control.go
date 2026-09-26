package spawn

import (
	"context"
	"fmt"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// SendNative delivers text to a native task's harness the way spawn delivers
// its instruction: typed into its terminal, submitted once the composer shows
// it, and delivered only once the harness shows it working.
func (s Service) SendNative(ctx context.Context, meta state.TaskMeta, text string) error {
	screens, ok := harness.NativeScreens(harness.Kind(meta.Harness))
	if !ok {
		return fmt.Errorf("send: native task %s runs %s, whose screens cfo cannot read", meta.ID, meta.Harness)
	}
	record, err := host.ReadRecord(s.StateDir, meta.ID)
	if err != nil {
		return fmt.Errorf("send: native task %s has no running terminal: %w", meta.ID, err)
	}
	return s.deliverNativeInstruction(ctx, record, screens, text)
}

// nativeKeys are the keys cfo send --key names, as a console reads them.
var nativeKeys = map[string]string{
	"enter": "\r", "escape": "\x1b", "esc": "\x1b",
	"ctrl+c": "\x03", "ctrl-c": "\x03", "c-c": "\x03",
	"ctrl+u": "\x15", "ctrl-u": "\x15", "c-u": "\x15",
}

// SendNativeKey sends one named key to a native task's terminal.
func (s Service) SendNativeKey(meta state.TaskMeta, key string) error {
	sequence, known := nativeKeys[strings.ToLower(key)]
	if !known {
		return fmt.Errorf("send: unsupported key %q; use Enter, Escape, Ctrl-C, or Ctrl-U", key)
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
	return client.Input([]byte(sequence))
}
