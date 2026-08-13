package claudehook

import (
	"testing"
	"time"
)

func TestSecondsGuardGrace(t *testing.T) {
	tests := []struct {
		name string
		set  bool
		env  string
		want time.Duration
	}{
		{name: "unset", set: false, want: 300 * time.Second},
		{name: "valid", set: true, env: "7", want: 7 * time.Second},
		{name: "invalid", set: true, env: "bogus", want: 300 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv("CFO_GUARD_GRACE", tt.env)
			}
			got := Seconds("CFO_GUARD_GRACE", 300)
			if got != tt.want {
				t.Errorf("Seconds(CFO_GUARD_GRACE, 300) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIntAutoarmAttempts(t *testing.T) {
	tests := []struct {
		name string
		set  bool
		env  string
		want int
	}{
		{name: "clamps above max", set: true, env: "9", want: 3},
		{name: "clamps below min", set: true, env: "0", want: 1},
		{name: "unset returns default", set: false, want: 2},
		{name: "garbage returns default", set: true, env: "bogus", want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv("CFO_CLAUDE_AUTOARM_ATTEMPTS", tt.env)
			}
			got := Int("CFO_CLAUDE_AUTOARM_ATTEMPTS", 2, 1, 3)
			if got != tt.want {
				t.Errorf("Int(CFO_CLAUDE_AUTOARM_ATTEMPTS, 2, 1, 3) = %d, want %d", got, tt.want)
			}
		})
	}
}
