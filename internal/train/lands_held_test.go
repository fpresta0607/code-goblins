package train

import (
	"encoding/json"
	"strings"
	"testing"
)

// GitHub's label history is what says a pull request once carried the hold
// label after its owner took it off, which is the word to land it.
func TestAPullRequestHasCarriedHoldByItsLabelsOrItsLabelHistory(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		graph          string
		hasCarriedHold bool
	}{
		"one that carries it":         {graph: `{"labels":{"nodes":[{"name":"Hold"}]}}`, hasCarriedHold: true},
		"one it was taken off":        {graph: `{"labels":{"nodes":[]},"timelineItems":{"nodes":[{"label":{"name":"hold"}}]}}`, hasCarriedHold: true},
		"one with another label":      {graph: `{"labels":{"nodes":[{"name":"docs"}]},"timelineItems":{"nodes":[{"label":{"name":"docs"}}]}}`},
		"one that never had a label":  {graph: `{"labels":{"nodes":[]},"timelineItems":{"nodes":[]}}`},
		"one whose history is absent": {graph: `{}`},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			var graph GraphPullRequest
			if err := json.Unmarshal([]byte(test.graph), &graph); err != nil {
				t.Fatal(err)
			}

			// Act
			pr := graph.PullRequest()

			// Assert
			if pr.HasCarriedHold != test.hasCarriedHold {
				t.Fatalf("HasCarriedHold = %v, want %v", pr.HasCarriedHold, test.hasCarriedHold)
			}
		})
	}
}

// Issue 2724: in a project whose pull requests land held, a green pull
// request a goblin reported done stays off every train until it carried the
// hold label, and the reason names the command that holds it.
func TestRidersInAProjectWhosePullRequestsLandHeld(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		landsHeld      bool
		hasCarriedHold bool
		isRiding       bool
	}{
		"one that never carried hold":        {landsHeld: true},
		"one its owner took hold off":        {landsHeld: true, hasCarriedHold: true, isRiding: true},
		"one in a project that does not ask": {isRiding: true},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			pr := greenPull(1)
			pr.LandsHeld, pr.HasCarriedHold = test.landsHeld, test.hasCarriedHold

			// Act
			riders, left := Riders([]PullRequest{pr}, "main", fleetAccount, goblinsFor(pr), nil)

			// Assert
			if test.isRiding {
				if len(riders) != 1 || len(left) != 0 {
					t.Fatalf("riders %+v, left %q, want it riding", riders, left)
				}
				return
			}
			want := "gh pr edit " + pr.URL + " --add-label " + HoldLabel
			if len(riders) != 0 || len(left) != 1 || !strings.Contains(left[0], "never carried the "+HoldLabel+" label") || !strings.Contains(left[0], want) {
				t.Fatalf("riders %+v, left %q, want it left out naming %q", riders, left, want)
			}
		})
	}
}
