package guard

import "testing"

func TestClassifyNativePrompt(t *testing.T) {
	for _, tc := range []struct {
		tool string
		want bool
	}{
		{"AskUserQuestion", true},
		{"ask_user_question", true},
		{"mcp__board__AskUserQuestion", false},
		{"Bash", false},
		{"Agent", false},
	} {
		if got := ClassifyNativePrompt(tc.tool); got != tc.want {
			t.Errorf("ClassifyNativePrompt(%q) = %v, want %v", tc.tool, got, tc.want)
		}
	}
}
