//go:build !windows

package host

import (
	"errors"
	"testing"
)

func TestLaunchAndCloseRefuseAsUnsupportedOffWindows(t *testing.T) {
	tests := []struct {
		name string
		call func() error
	}{
		{name: "Launch", call: func() error { _, err := Launch(t.TempDir(), []string{"goblins"}, nil, Spec{ID: "t1"}); return err }},
		{name: "Close", call: func() error { return Close(t.TempDir(), Record{ID: "t1"}, 0) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act
			err := test.call()

			// Assert
			if !errors.Is(err, errors.ErrUnsupported) {
				t.Fatalf("%s returned %v, want an error wrapping errors.ErrUnsupported", test.name, err)
			}
		})
	}
}

func TestNoHostReadsAsRunningOffWindows(t *testing.T) {
	// Act
	isRunning := Running(Record{ID: "t1", HostPID: 1})

	// Assert
	if isRunning {
		t.Fatal("Running reported a host off Windows, where nothing reads whether one runs yet")
	}
}
