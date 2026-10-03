package supervisor

import (
	"fmt"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// NativeCFORecipient reads the current registered native recipient. A sender
// must authenticate its receiving host; a hook must independently prove its
// caller's terminal custody before attaching this value to a prompt event.
func NativeCFORecipient(stateDir string) (nativehook.CFORecipient, error) {
	primary, identity, err := readPrimary(stateDir)
	if err != nil || primary.Host == "" || !primary.Process.VerifiedAlive() {
		return nativehook.CFORecipient{}, fmt.Errorf("%w: the native CFO recipient cannot be verified", ErrRejected)
	}
	record, err := host.ReadRecord(stateDir, primary.Host)
	if err != nil || record.ChildPID != primary.Process.PID || !record.ChildStart.Equal(primary.Process.Start) {
		return nativehook.CFORecipient{}, fmt.Errorf("%w: the registered CFO's native program changed", ErrRejected)
	}
	programStart, isProgramAlive := proc.StartTime(record.ChildPID)
	hostStart, isHostAlive := proc.StartTime(record.HostPID)
	if !isProgramAlive || !programStart.Equal(record.ChildStart) || !isHostAlive {
		return nativehook.CFORecipient{}, fmt.Errorf("%w: the receiving native host or program cannot be verified", ErrRejected)
	}
	conversation, err := ReadCFOConversation(stateDir)
	if err != nil || conversation.Host != record.ID || conversation.PID != record.ChildPID || conversation.Harness != primary.Agent || conversation.Updated.Before(programStart) {
		return nativehook.CFORecipient{}, fmt.Errorf("%w: the native CFO's current conversation is not registered", ErrRejected)
	}
	canonicalState, err := fsx.Canonical(stateDir)
	if err != nil {
		return nativehook.CFORecipient{}, fmt.Errorf("%w: the native CFO state cannot be verified", ErrRejected)
	}
	recipient := nativehook.CFORecipient{State: canonicalState, HostID: record.ID, HostPID: record.HostPID, HostStart: hostStart, ProgramPID: record.ChildPID, ProgramStart: programStart, Harness: primary.Agent, SessionID: conversation.Session, Registration: identity}
	if !recipient.Valid() {
		return nativehook.CFORecipient{}, fmt.Errorf("%w: the native CFO recipient binding is incomplete", ErrRejected)
	}
	return recipient, nil
}
