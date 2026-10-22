package train

import (
	"encoding/json"
	"strings"
	"testing"
)

// The workflows' draft job reads whether a pull request is a draft now and
// tests nothing, so its check is no part of a head's checks wherever gh's
// rollup is read (#963). Counted, a pull request just marked ready, whose
// head showed only its draft run's checks, the draft job's success among
// skipped ones, rode as tested; and a ready head whose draft read failed,
// and whose every job ran and passed, was left out as failed.
func TestTheDraftStateCheckIsNoPartOfAHeadsChecks(t *testing.T) {
	t.Parallel()
	const skipped = `{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SKIPPED"},{"__typename":"CheckRun","name":"plan","status":"COMPLETED","conclusion":"SKIPPED"}`
	const passed = `{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SUCCESS"},{"__typename":"CheckRun","name":"plan","status":"COMPLETED","conclusion":"SUCCESS"}`
	for name, test := range map[string]struct {
		rollup  string
		isRider bool
		why     string
	}{
		"a head with only its draft run's checks":              {`[{"__typename":"CheckRun","name":"draft state","status":"COMPLETED","conclusion":"SUCCESS"},` + skipped + `]`, false, "no check ran on its head"},
		"a head whose draft read failed and whose jobs passed": {`[{"__typename":"CheckRun","name":"draft state","status":"COMPLETED","conclusion":"FAILURE"},` + passed + `]`, true, ""},
		"a head whose draft read runs and whose jobs passed":   {`[{"__typename":"CheckRun","name":"draft state","status":"IN_PROGRESS","conclusion":""},` + passed + `]`, true, ""},
		"a status context named as the draft check counts":     {`[{"__typename":"StatusContext","context":"draft state","state":"FAILURE"},` + passed + `]`, false, "its checks failed"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange: the pull request as gh pr list reports it.
			pr := greenPull(1)
			goblins := goblinsFor(pr)
			if err := json.Unmarshal([]byte(`{"statusCheckRollup":`+test.rollup+`}`), &pr); err != nil {
				t.Fatal(err)
			}

			// Act
			riders, left := Riders([]PullRequest{pr}, "main", fleetAccount, goblins, nil)

			// Assert
			if test.isRider && (len(riders) != 1 || len(left) != 0) {
				t.Fatalf("riders %v, left %q, want it to ride", riders, left)
			}
			if !test.isRider && (len(riders) != 0 || len(left) != 1 || !strings.Contains(left[0], test.why)) {
				t.Fatalf("riders %v, left %q, want it left out because %s", riders, left, test.why)
			}
		})
	}
}
