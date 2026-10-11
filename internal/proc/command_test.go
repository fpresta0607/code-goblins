package proc

import (
	"strings"
	"testing"
)

// A command line can hold a credential, and what a sweep or a watch names
// is kept in its record and shown in a wake.
func TestAProcessIsNamedWithoutWhatItsCommandLineHides(t *testing.T) {
	for _, test := range []struct {
		arguments []string
		want      string
	}{
		{[]string{"node", "server.js", "--port", "4455"}, "node server.js --port 4455"},
		{[]string{"curl", "--token=abc123", "https://example.test/hook?signature=def456"}, "curl --token=<hidden> https://example.test/hook"},
		{[]string{"tool", "--api-key", "abc123", "run"}, "tool --api-key <hidden> run"},
		{[]string{"tool", "PASSWORD=hunter2"}, "tool PASSWORD=<hidden>"},
		{[]string{"node", strings.Repeat("x", 200)}, "node " + strings.Repeat("x", 152) + "..."},
	} {
		if got := ShownCommand(test.arguments); got != test.want {
			t.Errorf("ShownCommand(%q) = %q, want %q", test.arguments, got, test.want)
		}
	}
}
