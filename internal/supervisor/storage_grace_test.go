package supervisor

import (
	"errors"
	"fmt"
	"testing"
)

// The board showed the Overlord "supervisor persistence failed: rename
// ...\.cfo-tmp-2713629804 ...\.supervisor.json: Access is denied" on
// 2026-10-01 for a lock that was gone within the minute. A failed save
// loses nothing: what it would have saved stays where it came from and the
// next cycle, at most two seconds away, saves it. So one failure is not the
// Overlord's business; only a store that keeps failing is.
func TestATransientPersistFailureStaysOffTheBoard(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "a storage failure alone", err: fmt.Errorf("%w: rename .cfo-tmp-1 .supervisor.json: Access is denied", ErrStorage), want: ""},
		{name: "a storage failure beside another error", err: errors.Join(fmt.Errorf("%w: Access is denied", ErrStorage), errors.New("questions inbox unreadable")), want: "questions inbox unreadable"},
		{name: "another error alone", err: errors.New("questions inbox unreadable"), want: "questions inbox unreadable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			s := &Service{}

			// Act
			s.publish(test.err)

			// Assert
			if got := s.lastError; got != test.want {
				t.Errorf("the board shows %q, want %q", got, test.want)
			}
		})
	}
}
