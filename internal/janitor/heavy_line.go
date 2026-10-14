package janitor

import "errors"

// noTurnExit is the exit of a command Wrap wrapped that took no turn and ran
// nothing, as cfo gate turn's 125 when a run takes the line within its wait.
const noTurnExit = 125

// HeavyLine is the machine's one line for heavy runs, as the sweep sees it
// before it deletes from the package caches that builds read.
type HeavyLine interface {
	// Free is nil while the line has one slot and no run holds it or waits
	// for it, and otherwise says why not, or why it cannot be read.
	Free() error
	// Wrap is command run in a turn of the line, named who, by a short-lived
	// process that waits for the turn and gives it back as it ends, so the
	// line reclaims the turn of one that dies as it would any run's.
	Wrap(who string, command []string) []string
}

// lineFree is the line's Free, or why a sweep given no line deletes no cache.
func (cfg Config) lineFree() error {
	if cfg.Line == nil {
		return errors.New("the sweep was given no heavy line to read")
	}
	return cfg.Line.Free()
}
