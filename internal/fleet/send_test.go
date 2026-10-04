package fleet

import (
	"strings"
	"testing"
	"time"
)

// A message that opens a harness completion popup gets a longer wait before
// Enter: an Enter that lands while the popup is open selects the highlighted
// completion, so `/exit` would run a different command.
func TestTypeSettleForWaitsLongerForACompletionPopup(t *testing.T) {
	for _, test := range []struct {
		name    string
		message string
		want    time.Duration
	}{
		{name: "slash command", message: "/exit", want: completionSettle},
		{name: "dollar prefix", message: "$env:FOO", want: completionSettle},
		{name: "plain text", message: "status please", want: typeSettle},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := TypeSettleFor(test.message); got != test.want {
				t.Errorf("TypeSettleFor(%q) = %v, want %v", test.message, got, test.want)
			}
		})
	}
}

func TestNormalizeKeyNamesOnlySupportedKeys(t *testing.T) {
	for _, test := range []struct {
		key  string
		want string
	}{
		{key: "enter", want: "Enter"},
		{key: "Esc", want: "Escape"},
		{key: "escape", want: "Escape"},
		{key: "Ctrl-C", want: "Ctrl+C"},
		{key: "c-u", want: "Ctrl+U"},
	} {
		t.Run(test.key, func(t *testing.T) {
			got, err := NormalizeKey(test.key)
			if err != nil || got != test.want {
				t.Errorf("NormalizeKey(%q) = %q, %v; want %q", test.key, got, err, test.want)
			}
		})
	}

	if _, err := NormalizeKey("F1"); err == nil || !strings.Contains(err.Error(), "unsupported key") {
		t.Errorf("NormalizeKey(F1) error = %v, want an unsupported key refusal", err)
	}
}
