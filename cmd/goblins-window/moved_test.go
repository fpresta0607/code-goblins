package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The supervisor moves a window an update left on its old program by ending
// it and opening this one through the desktop, which passes no arguments. A
// window that was in the tray comes back in the tray: the move's record says
// so, and only a record of a move made just now counts. The record is read
// once.
func TestAWindowTheSupervisorMovedStartsInTheTrayWhenTheOldOneWas(t *testing.T) {
	now := time.Date(2026, 10, 9, 4, 0, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		record string
		want   bool
	}{
		"moved from the tray just now": {`{"background":true,"at":"2026-10-09T03:59:50Z"}`, true},
		"moved from the screen":        {`{"background":false,"at":"2026-10-09T03:59:50Z"}`, false},
		"a move of long ago":           {`{"background":true,"at":"2026-10-09T03:30:00Z"}`, false},
		"a record that is not one":     {`tray`, false},
		"no move":                      {"", false},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			record := filepath.Join(stateDir, "window-move.json")
			if tc.record != "" {
				if err := os.WriteFile(record, []byte(tc.record), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			got := movedFromTheTray(stateDir, now)

			// Assert
			if got != tc.want {
				t.Errorf("movedFromTheTray = %v, want %v", got, tc.want)
			}
			if _, err := os.Stat(record); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the move's record is still there (%v), want it read once", err)
			}
		})
	}
}
