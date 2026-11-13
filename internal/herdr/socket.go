package herdr

import "context"

// PaneInput types into a session's panes and reads their history with no
// process per call, for a view that types key by key.
type PaneInput interface {
	// SendText types unsubmitted literal text into a pane, and a complete
	// bracketed paste with the program's own paste mode.
	SendText(ctx context.Context, pane, text string) error
	// History returns up to lines of the pane's most recent output, history
	// included, with its colors.
	History(ctx context.Context, pane string, lines int) (string, error)
}
