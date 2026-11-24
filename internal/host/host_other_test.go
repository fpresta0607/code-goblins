//go:build !windows

package host

import (
	"errors"
	"strings"
	"testing"
)

func TestEntryPointsRefuseAsUnsupportedOffWindows(t *testing.T) {
	tests := []struct {
		name       string
		call       func(record Record) error
		wantPrefix string
	}{
		{name: "Dial", call: func(record Record) error { _, err := Dial(record); return err }},
		{name: "View", call: func(record Record) error { _, err := View(record); return err }},
		{name: "Watch", call: func(record Record) error { _, err := Watch(record); return err }},
		{name: "DialDelivery", call: func(record Record) error { _, err := DialDelivery(record); return err }},
		{name: "Answer", call: Answer},
		{name: "ReadScreen", call: func(record Record) error { _, err := ReadScreen(record); return err }, wantPrefix: "host: read the screen of terminal t1: "},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			record := Record{ID: "t1"}

			// Act
			err := test.call(record)

			// Assert
			if !errors.Is(err, errors.ErrUnsupported) {
				t.Fatalf("%s returned %v, want an error wrapping errors.ErrUnsupported", test.name, err)
			}
			if !strings.HasPrefix(err.Error(), test.wantPrefix) {
				t.Errorf("%s returned %q, want it to start %q", test.name, err, test.wantPrefix)
			}
		})
	}
}
