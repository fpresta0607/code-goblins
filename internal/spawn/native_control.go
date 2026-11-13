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
// reported queued if its harness queued it after all. With ShouldSendNow, a
// command a refused turn would hold is run at once instead where its
// harness's send-now key can (RanNow, InterruptTurn), and is refused as
// before where it cannot.
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
	read := func(ctx context.Context) ([]string, error) { return s.readNativeScreen(ctx, record) }
	press := func() error { return s.pressNativeKeys(record, "send now", screens.SendNow) }
	// Send now on a command its harness would queue reaches the turn through
	// that harness's send-now key, once (#1915). Claude Code takes the command
	// into its turn's queue and runs it at once with the key, but only while
	// nothing else waits there, since the key takes that queue from its head.
	// Codex refuses such a command in a turn and pi expands one into a steer,
	// so their key, which ends the turn, goes first, and the command goes at
	// the prompt line the turn's end leaves.
	isSentNow := isBusy && !isBesideTurn && s.ShouldSendNow && screens.SendNow != ""
	if isSentNow && screens.Queued == nil {
		isBusy = InterruptTurn(ctx, read, press, screens, before) == nil
	}
	isRunNow := isSentNow && screens.Queued != nil && !screens.OffersQueue(before)
	if isBusy && !isBesideTurn && !isRunNow && s.ShouldRefuseTurn {
		return fmt.Errorf("spawn: native terminal %s: %w: %w", record.ID, fleet.ErrNotTyped, ErrInTurn)
	}
	if _, err := s.submitNative(ctx, record, screens, text, fleet.TypeSettleFor(text), false); err != nil {
		return err
	}
	if isRunNow {
		ran, isPressed, err := RanNow(ctx, read, press, screens, text)
		switch {
		case err != nil:
			return fmt.Errorf("spawn: native terminal %s has the command submitted, and its send-now key could not be pressed: %w: %w", record.ID, err, fleet.ErrQueuedForToolCall)
		case ran:
			return nil
		case isPressed:
			return fmt.Errorf("spawn: native terminal %s has the command submitted: %w", record.ID, ErrSentNow)
		}
		return fmt.Errorf("spawn: native terminal %s has the command submitted: %w", record.ID, fleet.ErrQueuedForToolCall)
	}
	if isBesideTurn {
		ran, isQueued := RanBesideTurn(ctx, read, screens, text, screens.OffersQueue(before))
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

// RanNow reports whether a harness in a turn ran command, submitted into that
// turn's queue while it held nothing else, at once with its send-now key,
// which press presses: the key goes once, as soon as the prompt line shows
// the command gone into the queue with no screen of the harness's own open
// over it, and the command ran once that line shows it gone with no queue
// offered on two reads in a row, within nativeCommandTaken, as
// RanBesideTurn reads it. isPressed says whether the key went, and a
// harness that ran the command beside its turn after all, never queueing it,
// reads ran with no key pressed.
func RanNow(ctx context.Context, read func(context.Context) ([]string, error), press func() error, screens harness.Screens, command string) (ran, isPressed bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, nativeCommandTaken)
	defer cancel()
	for gone := 0; ; {
		screen, err := read(ctx)
		switch {
		case err != nil:
			return false, isPressed, nil
		case !screens.TookCommand(screen, command):
			gone = 0
		case screens.OffersQueue(screen):
			gone = 0
			if !isPressed && !screens.IsOpen(screen) {
				if err := press(); err != nil {
					return false, false, err
				}
				isPressed = true
			}
		default:
			if gone++; gone == 2 {
				return true, isPressed, nil
			}
		}
		select {
		case <-time.After(nativePoll):
		case <-ctx.Done():
			return false, isPressed, nil
		}
	}
}

// InterruptTurn presses, with press, the send-now key of a harness in a turn,
// the Escape that ends a Codex's or a pi's turn, once over screen, its empty
// prompt line with no screen of its own open, and returns the screen that
// shows the turn over at an empty, ready prompt line on two reads in a row
// within nativeCommandTaken, as RanBesideTurn reads a prompt line. It returns
// none when screen took no key, the key could not be pressed, or the turn did
// not end in that time: a Codex's own queue then starts a turn of its own,
// and a pi's comes back onto its prompt line.
func InterruptTurn(ctx context.Context, read func(context.Context) ([]string, error), press func() error, screens harness.Screens, screen []string) []string {
	if screens.IsOpen(screen) || !screens.ComposerEmpty(screen) || press() != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, nativeCommandTaken)
	defer cancel()
	for idle := 0; ; {
		select {
		case <-time.After(nativePoll):
		case <-ctx.Done():
			return nil
		}
		screen, err := read(ctx)
		if err != nil {
			return nil
		}
		if !screens.IsReady(screen) || !screens.ComposerEmpty(screen) || screens.IsOpen(screen) {
			idle = 0
			continue
		}
		if idle++; idle == 2 {
			return screen
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
