package vtscreen

import "testing"

// Width measures text in cells, as a screen's row takes them: a wide
// character two, a combining mark or a variation selector none.
func TestWidthCountsCells(t *testing.T) {
	for _, test := range []struct {
		text string
		want int
	}{
		{"", 0},
		{"plain", 5},
		{"\u6f22\u5b57", 4},
		{"e\u0301", 1},
		{"\u2764\uFE0F", 1},
	} {
		if got := Width(test.text); got != test.want {
			t.Errorf("Width(%q) = %d, want %d", test.text, got, test.want)
		}
	}
}
