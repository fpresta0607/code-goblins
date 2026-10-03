package nativehook

import (
	"strings"
	"testing"
	"time"
)

func TestANativeCFOEventRequiresItsOwnCompleteRecipient(t *testing.T) {
	now := time.Now().UTC()
	recipient := CFORecipient{State: t.TempDir(), HostID: "cfo", HostPID: 10, HostStart: now.Add(-2 * time.Minute), ProgramPID: 20, ProgramStart: now.Add(-time.Minute), Harness: "codex", SessionID: "thread-1", Registration: strings.Repeat("a", 64)}
	tests := []struct {
		name    string
		change  func(*Event)
		isValid bool
	}{
		{"complete recipient", func(*Event) {}, true},
		{"legacy unbound event", func(e *Event) { e.Recipient = CFORecipient{} }, true},
		{"another host", func(e *Event) { e.HostID = "another-cfo" }, false},
		{"another harness", func(e *Event) { e.Harness = "claude" }, false},
		{"another thread", func(e *Event) { e.SessionID = "thread-2" }, false},
		{"another role", func(e *Event) { e.Role = "worker" }, false},
		{"relative state", func(e *Event) { e.Recipient.State = "." }, false},
		{"invalid registration", func(e *Event) { e.Recipient.Registration = "unknown" }, false},
		{"missing host pid", func(e *Event) { e.Recipient.HostPID = 0 }, false},
		{"missing host birth", func(e *Event) { e.Recipient.HostStart = time.Time{} }, false},
		{"missing program pid", func(e *Event) { e.Recipient.ProgramPID = 0 }, false},
		{"missing program birth", func(e *Event) { e.Recipient.ProgramStart = time.Time{} }, false},
		{"missing thread", func(e *Event) { e.Recipient.SessionID = "" }, false},
		{"program born after prompt", func(e *Event) { e.Recipient.ProgramStart = now.Add(time.Second) }, false},
		{"host born after program", func(e *Event) { e.Recipient.HostStart = now }, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			event := Event{Schema: 1, ID: strings.Repeat("b", 64), Harness: "codex", Kind: "active", SessionID: "thread-1", Role: "cfo", CWD: recipient.State, OccurredAt: now, Prompt: true, HostID: "cfo", Recipient: recipient}
			test.change(&event)

			// Act
			err := event.Validate()

			// Assert
			if (err == nil) != test.isValid {
				t.Errorf("event validation=%v, want valid=%t", err, test.isValid)
			}
		})
	}
}
