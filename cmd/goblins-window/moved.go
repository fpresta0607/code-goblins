package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// moveRecord is where the supervisor records that it moved the window onto
// the program an update installed, as internal/supervisor/window_move.go
// writes it: whether the window it ended was in the tray, and when.
const moveRecord = "window-move.json"

// moveFresh is how old a move's record may be and still be this window's: the
// supervisor opens it a moment after it writes the record.
const moveFresh = 2 * time.Minute

// movedFromTheTray reads the move's record in stateDir once, and reports
// whether this window is the one the supervisor just opened in place of a
// window that was in the tray, which starts in the tray too.
func movedFromTheTray(stateDir string, now time.Time) bool {
	path := filepath.Join(stateDir, moveRecord)
	data, err := fsx.ReadFile(path)
	if err != nil {
		return false
	}
	_ = os.Remove(path)
	var move struct {
		Background bool      `json:"background"`
		At         time.Time `json:"at"`
	}
	if json.Unmarshal(data, &move) != nil {
		return false
	}
	return move.Background && now.Sub(move.At) < moveFresh
}
