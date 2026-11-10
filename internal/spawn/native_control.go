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
// it, and delivered only once the harness is proven to have it; text typed
// while the harness is in a turn is otherwise reported queued for its next
// tool call. A slash or dollar command waits out the harness's completion
// popup before it is submitted, and is delivered once a harness that ran no
// turn shows it gone from its input line, its own panel, such as /model's
// picker, open over it or not. Otherwise it is reported unconfirmed: /exit
// ends the harness, and one typed into a turn waits in its queue.
// Text nobody says is the Overlord's own, a wake line, a notice or a key,
// clears the terminal's mark of his input.
func (s Service) SendNative(ctx context.Context, meta state.TaskMeta, text string) error {
	screens, ok := harness.NativeScreens(harness.Kind(meta.Harness))
	if !ok {
		return fmt.Errorf("send: native task %s runs %s, whose screens cfo cannot read", meta.ID, meta.Harness)
	}
	record, err := host.ReadRecord(s.StateDir, meta.ID)
	if err != nil {
		return fmt.Errorf("send: native task %s has no running terminal: %w: %w", meta.ID, fleet.ErrNotTyped, err)
	}
	if !fleet.IsCommand(text) {
		return s.deliverNativeInstruction(ctx, record, screens, meta, "", text, false)
	}
	before, err := s.readNativeScreen(ctx, record)
	if err != nil {
		return fmt.Errorf("%w: %w", fleet.ErrNotTyped, err)
	}
	isBusy := screens.IsWorking(before)
	if isBusy && s.ShouldRefuseTurn {
		return fmt.Errorf("spawn: native terminal %s: %w: %w", record.ID, fleet.ErrNotTyped, ErrInTurn)
	}
	if _, err := s.submitNative(ctx, record, screens, text, fleet.TypeSettleFor(text), false); err != nil {
		return err
	}
	// A command typed into a turn waits in the harness's queue, so only one
	// submitted while no turn ran can show itself taken.
	if !isBusy && s.tookCommand(ctx, record, screens, text) {
		return nil
	}
	return fmt.Errorf("send: %q was typed into native terminal %s and submitted once, but nothing on its screen confirms a command ran; check it with cfo peek %s rather than sending it again", text, meta.ID, meta.ID)
}

// tookCommand reports whether the harness took command, submitted while no
// turn ran, within nativeCommandTaken: its screen shows it working on it, or
// gone from the input line, as a command the harness runs itself leaves it
// with no turn and, in Codex and pi, no record (#2193). A harness the command
// ended shows nothing, so its screen is read for no longer than that.
func (s Service) tookCommand(ctx context.Context, record host.Record, screens harness.Screens, command string) bool {
	ctx, cancel := context.WithTimeout(ctx, nativeCommandTaken)
	defer cancel()
	for {
		screen, err := s.readNativeScreen(ctx, record)
		if err != nil {
			return false
		}
		if screens.IsWorking(screen) || screens.TookCommand(screen, command) {
			return true
		}
		if s.sleep(ctx, nativePoll) != nil {
			return false
		}
	}
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
