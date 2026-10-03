package nativehook

import (
	"encoding/hex"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// CFORecipient identifies the native program and conversation that received
// input, independently of a later program reusing its terminal or thread.
type CFORecipient struct {
	State        string    `json:"state"`
	HostID       string    `json:"host_id"`
	HostPID      int       `json:"host_pid"`
	HostStart    time.Time `json:"host_start"`
	ProgramPID   int       `json:"program_pid"`
	ProgramStart time.Time `json:"program_start"`
	Harness      string    `json:"harness"`
	SessionID    string    `json:"session_id"`
	Registration string    `json:"registration"`
}

func (r CFORecipient) Valid() bool {
	registration, err := hex.DecodeString(r.Registration)
	return err == nil && len(registration) == 32 &&
		filepath.IsAbs(r.State) && len(r.State) <= 4096 && !strings.ContainsAny(r.State, "\x00\r\n") &&
		state.ValidTaskID(r.HostID) == nil && r.HostPID > 0 && !r.HostStart.IsZero() &&
		r.ProgramPID > 0 && !r.ProgramStart.IsZero() && !r.ProgramStart.Before(r.HostStart) &&
		(r.Harness == "claude" || r.Harness == "codex" || r.Harness == "pi") &&
		r.SessionID != "" && len(r.SessionID) <= 4096 && !strings.ContainsAny(r.SessionID, "\x00\r\n")
}

func (r CFORecipient) Matches(other CFORecipient) bool {
	return r.Valid() && other.Valid() && r.State == other.State &&
		r.HostID == other.HostID && r.HostPID == other.HostPID && r.HostStart.Equal(other.HostStart) &&
		r.ProgramPID == other.ProgramPID && r.ProgramStart.Equal(other.ProgramStart) &&
		r.Harness == other.Harness && r.SessionID == other.SessionID && r.Registration == other.Registration
}
