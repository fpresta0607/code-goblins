package spawn

import (
	"context"
	"fmt"
	"time"

	"github.com/fpresta0607/code-goblins/internal/claudestream"
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
// ends the harness, and one typed into a turn waits in its queue. A command
// its harness runs at once beside a turn (harness.RunsBesideTurn) is never
// refused one, since the Overlord types it into a turn in the terminal and it
// runs there (2026-11-11: "cant type / commands into modern chat like
// terminal", #2372): it is delivered once the prompt line shows it ran, and
// reported queued if its harness queued it after all.
// Text nobody says is the Overlord's own, a wake line, a notice or a key,
// clears the terminal's mark of his input. A stream session is written the
// text, a command too, as one line, and proves it took it by its own report
// (deliverStream).
func (s Service) SendNative(ctx context.Context, meta state.TaskMeta, text string) error {
	screens, ok := harness.NativeScreens(harness.Kind(meta.Harness))
	if !ok {
		return fmt.Errorf("send: native task %s runs %s, whose screens goblins cannot read", meta.ID, meta.Harness)
	}
	record, err := host.ReadRecord(s.StateDir, meta.ID)
	if err != nil {
		return fmt.Errorf("send: native task %s has no running terminal: %w: %w", meta.ID, fleet.ErrNotTyped, err)
	}
	if record.Stream {
		return s.deliverStream(ctx, record, text)
	}
	if !fleet.IsCommand(text) {
		return s.deliverNativeInstruction(ctx, record, screens, meta, "", text, false)
	}
	before, err := s.readNativeScreen(ctx, record)
	if err != nil {
		return fmt.Errorf("%w: %w", fleet.ErrNotTyped, err)
	}
	isBusy := screens.IsWorking(before)
	isBesideTurn := isBusy && harness.RunsBesideTurn(harness.Kind(meta.Harness), text)
	if isBusy && !isBesideTurn && s.ShouldRefuseTurn {
		return fmt.Errorf("spawn: native terminal %s: %w: %w", record.ID, fleet.ErrNotTyped, ErrInTurn)
	}
	if _, err := s.submitNative(ctx, record, screens, text, fleet.TypeSettleFor(text), false); err != nil {
		return err
	}
	if isBesideTurn {
		ran, isQueued := RanBesideTurn(ctx, func(ctx context.Context) ([]string, error) { return s.readNativeScreen(ctx, record) }, screens, text, screens.OffersQueue(before))
		if ran {
			return nil
		}
		if isQueued {
			return fmt.Errorf("spawn: native terminal %s has the command submitted: %w", record.ID, fleet.ErrQueuedForToolCall)
		}
	}
	// Any other command typed into a turn waits in the harness's queue, so
	// only one submitted while no turn ran can show itself taken.
	if !isBusy && s.tookCommand(ctx, record, screens, text) {
		return nil
	}
	return fmt.Errorf("send: %q was typed into native terminal %s and submitted once, but nothing on its screen confirms a command ran; check it with goblins peek %s rather than sending it again", text, meta.ID, meta.ID)
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

// RanBesideTurn reports whether a harness in a turn ran command, submitted
// into that turn, at once beside it. Such a command starts no turn and can
// leave no record, so the prompt line is the only sign of it: within
// nativeCommandTaken it shows the command gone, on two reads in a row, since
// a harness can empty its prompt line a moment before it draws what it
// queued. A prompt line that offers its queue, and did not before the command
// was typed, as offeredQueue says, shows the harness queued the command for
// the turn's end after all, as a release whose own list of such commands has
// moved on would, and isQueued says so. Each read is given that same window,
// since a harness the command ended, as /exit does, shows nothing, and a read
// that waits out its own grace for a screen would hold the send past it.
func RanBesideTurn(ctx context.Context, read func(context.Context) ([]string, error), screens harness.Screens, command string, offeredQueue bool) (ran, isQueued bool) {
	ctx, cancel := context.WithTimeout(ctx, nativeCommandTaken)
	defer cancel()
	for gone := 0; ; {
		screen, err := read(ctx)
		switch {
		case err != nil:
			return false, false
		case !screens.TookCommand(screen, command):
			gone = 0
		case screens.OffersQueue(screen) && !offeredQueue:
			return false, true
		default:
			if gone++; gone == 2 {
				return true, false
			}
		}
		select {
		case <-time.After(nativePoll):
		case <-ctx.Done():
			return false, false
		}
	}
}

// nativeKeys are the keys goblins send --key names, as a console reads them.
var nativeKeys = map[string]string{"Enter": "\r", "Escape": "\x1b", "Ctrl+C": "\x03", "Ctrl+U": "\x15"}

// SendNativeKey sends one named key to a native task's terminal. A stream
// session has no terminal: Escape and Ctrl+C stop its turn, as either does in
// Claude Code's terminal, and the keys that act on a composer it has not got
// are refused.
func (s Service) SendNativeKey(meta state.TaskMeta, key string) error {
	name, err := fleet.NormalizeKey(key)
	if err != nil {
		return err
	}
	record, err := host.ReadRecord(s.StateDir, meta.ID)
	if err != nil {
		return fmt.Errorf("send: native task %s has no running terminal: %w", meta.ID, err)
	}
	if record.Stream {
		if name != "Escape" && name != "Ctrl+C" {
			return fmt.Errorf("send: native task %s runs on stream-json pipes, with no composer for %s to act on", meta.ID, name)
		}
		writer, err := host.DialStreamWriter(record)
		if err != nil {
			return fmt.Errorf("send: stream session %s does not answer: %w", meta.ID, err)
		}
		defer writer.Close()
		return writer.Write(claudestream.RequestLine(lineID(), "interrupt", nil))
	}
	client, err := host.Dial(record)
	if err != nil {
		return fmt.Errorf("send: native terminal %s does not answer: %w", meta.ID, err)
	}
	defer client.Close()
	return client.Input([]byte(nativeKeys[name]))
}
