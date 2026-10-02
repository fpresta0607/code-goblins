package spawn

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
)

// nativeTerminalRuns reports whether native terminal id's host may still run,
// and so the harness it holds: the host ends with its harness.
func nativeTerminalRuns(stateDir, id string) bool {
	record, err := host.ReadRecord(stateDir, id)
	return err == nil && host.Running(record)
}

// stopNative asks a native task's harness to exit on its own terms, and
// proves it did: its terminal ends with it. A dialog its exit command opens is answered with the harness's own
// keys. A harness that still runs after that has its terminal closed, which
// ends it and everything it started, since a second harness must never start
// beside it.
func (s Service) stopNative(ctx context.Context, id string, control harness.Control) error {
	record, err := host.ReadRecord(s.StateDir, id)
	if errors.Is(err, fs.ErrNotExist) {
		// Nothing is running: an ended harness is a common reason to switch.
		return nil
	}
	if err != nil {
		return fmt.Errorf("switch: read native terminal %s: %w", id, err)
	}
	if !host.Running(record) {
		return nil
	}
	ended := func(tries int) (bool, error) {
		for i := 0; i < tries; i++ {
			if !nativeTerminalRuns(s.StateDir, id) {
				return true, nil
			}
			if err := s.sleep(ctx, stopPoll); err != nil {
				return false, err
			}
		}
		return !nativeTerminalRuns(s.StateDir, id), nil
	}
	client, err := host.Dial(record)
	if err == nil {
		defer client.Close()
		keys := func(names []string) error {
			for _, name := range names {
				key, err := fleet.NormalizeKey(name)
				if err != nil {
					return fmt.Errorf("switch: %w", err)
				}
				if err := client.Input([]byte(nativeKeys[key])); err != nil {
					return fmt.Errorf("switch: send %s to native terminal %s: %w", key, id, err)
				}
				if err := s.sleep(ctx, launchSettle); err != nil {
					return err
				}
			}
			return nil
		}
		if err := keys(control.StopKeys); err != nil {
			return err
		}
		if control.StopCommand != "" {
			if err := client.Input([]byte(control.StopCommand)); err != nil {
				return fmt.Errorf("switch: type the harness stop command into native terminal %s: %w", id, err)
			}
			if err := s.sleep(ctx, launchSettle); err != nil {
				return err
			}
			if err := client.Input([]byte("\r")); err != nil {
				return fmt.Errorf("switch: submit the harness stop command in native terminal %s: %w", id, err)
			}
		}
		if stopped, err := ended(stopTries / 2); err != nil || stopped {
			return err
		}
		if len(control.ExitMarkers) > 0 {
			if screen, err := s.readNativeScreen(ctx, record); err == nil {
				shown := strings.Join(screen, "\n")
				if slices.ContainsFunc(control.ExitMarkers, func(marker string) bool { return strings.Contains(shown, marker) }) {
					if err := keys(control.ExitKeys); err != nil {
						return err
					}
					if stopped, err := ended(stopTries); err != nil || stopped {
						return err
					}
				}
			}
		}
	}
	if err := host.Close(s.StateDir, record, nativeCloseWait); err != nil {
		return fmt.Errorf("switch: the harness in native terminal %s did not exit and its terminal could not be closed, so no second harness was started beside it: %w", id, err)
	}
	return nil
}
