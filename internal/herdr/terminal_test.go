package herdr

import (
	"reflect"
	"testing"
)

// The board's view of a pane attaches as an observer at the pane's size, or
// as a controller that sizes the pane and takes it over from any other
// client, since the most recent client to take control wins.
func TestTerminalSessionArgsTakeOverOnlyForControl(t *testing.T) {
	cases := []struct {
		name    string
		control bool
		want    []string
	}{
		{"an observer changes nothing", false, []string{"--session", "cfo", "terminal", "session", "observe", "term_1", "--cols", "120", "--rows", "40"}},
		{"a controller sizes the pane and takes it over", true, []string{"--session", "cfo", "terminal", "session", "control", "term_1", "--cols", "120", "--rows", "40", "--takeover"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := terminalSessionArgs("cfo", "term_1", c.control, 120, 40); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("args = %q, want %q", got, c.want)
			}
		})
	}
}
