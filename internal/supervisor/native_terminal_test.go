package supervisor

import (
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/harness"
)

// The relay presses a typed message's Enter over Claude Code's screen only
// while its prompt line holds the message's words alone, read to their end,
// under no question (#2848's review): pressed over words beside them it sends
// those too, and pressed under a question it answers it. A screen whose
// prompt line cannot be read shows no words beside them, so it takes the
// Enter once the words show, as the board's own typed delivery submits over
// one (sendNative).
func TestATypedMessagesEnterGoesOnlyOverItsWordsAloneUnderNoQuestion(t *testing.T) {
	rule := strings.Repeat("─", 80)
	mode := "  ⏵⏵ bypass permissions on (shift+tab to cycle)"
	for _, test := range []struct {
		name, typed string
		screen      []string
		isPressed   bool
	}{
		{name: "its words alone", typed: "please run /ship now", screen: []string{"> rebase onto main", "", rule, "❯ please run /ship now", rule, mode}, isPressed: true},
		{name: "its lines alone", typed: "first line\nthen run /ship", screen: []string{rule, "❯ first line", "  then run /ship", rule, mode}, isPressed: true},
		{name: "its words over the skills they end in", typed: "please run /ship", screen: []string{rule, "❯ please run /ship", rule, "  /ship          Ship the branch", "  /ship-docs     Ship the docs"}, isPressed: true},
		{name: "its words where no prompt line can be read", typed: "please run /ship now", screen: []string{"> rebase onto main", "", rule, "❯ please run /ship now"}, isPressed: true},
		{name: "words typed behind its own", typed: "please run /ship now", screen: []string{rule, "❯ please run /ship nowand /review it", rule, mode}},
		{name: "words typed ahead of its own", typed: "please run /ship now", screen: []string{rule, "❯ and /review itplease run /ship now", rule, mode}},
		{name: "its words not yet read to their end", typed: "please run /ship now", screen: []string{rule, "❯ please run /sh", rule, mode}},
		{name: "an empty prompt line under its words sent before", typed: "please run /ship now", screen: []string{"> please run /ship now", "", rule, "❯ ", rule, mode}},
		{name: "a permission prompt in the prompt line's place", typed: "please run /ship now", screen: []string{"> please run /ship now", "● Write(nine.txt)", " Do you want to create nine.txt?", " ❯ 1. Yes", "   2. No"}},
		{name: "a question under its words", typed: "please run /ship now", screen: []string{rule, "❯ please run /ship now", rule, " Do you want to proceed?", " ❯ 1. Yes", "   2. No"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			screens, _ := harness.NativeScreens(harness.Claude)

			// Act
			isPressed := typedAlone(screens, test.screen, test.typed)

			// Assert
			if isPressed != test.isPressed {
				t.Fatalf("typedAlone = %t over %q, want %t", isPressed, test.screen, test.isPressed)
			}
		})
	}
}
