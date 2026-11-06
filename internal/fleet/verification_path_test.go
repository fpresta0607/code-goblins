package fleet

import (
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/routing"
)

// The Verification section is boilerplate every generated brief carries: it
// says how the work is checked, never what the brief asks for. Its words on
// which findings a goblin may accept, correctness, security or regression
// defects among them, leave an ordinary task's class and risk as they are
// (#2049).
func TestGeneratedVerificationLeavesAnOrdinaryBriefOrdinary(t *testing.T) {
	// Arrange
	queued := QueuedTask{Row: BacklogRow{ID: "task", Repo: "demo", Title: "Add a sort control to the settings list", Mode: "no-mistakes"}}
	brief := QueuedBriefText(queued, nil)
	_, verification, _ := strings.Cut(brief, "## Verification\n\n")
	verification, _, _ = strings.Cut(verification, "\n\n## Authentication")
	if !strings.Contains(verification, "security") || !strings.Contains(verification, "regression") {
		t.Fatalf("premise: the generated Verification section no longer names the defects a goblin never accepts: %s", verification)
	}

	// Act
	got := routing.Classify(brief)

	// Assert
	if got.Class != routing.Implementation || got.Risk != "normal" {
		t.Fatalf("Classify = %+v, want an ordinary implementation task: the Verification boilerplate is not the ask", got)
	}
}

func TestStrictQueuedBriefAssignsChecksToOnePipelineOwner(t *testing.T) {
	// Arrange
	queued := QueuedTask{Row: BacklogRow{ID: "task", Repo: "demo", Title: "Change an approved report", Mode: "no-mistakes"}}

	// Act: the emitted brief is the worker's owned instruction interface.
	brief := QueuedBriefText(queued, nil)
	_, verification, _ := strings.Cut(brief, "## Verification\n\n")
	verification, _, _ = strings.Cut(verification, "\n\n## Authentication")

	// Assert
	if !strings.Contains(verification, "pipeline Test step") || !strings.Contains(verification, "once") || strings.Contains(verification, "Before each push run") {
		t.Fatalf("strict brief duplicates or omits its check owner: %s", verification)
	}
}

func TestPipelineVerificationReusesTheProjectEnvironmentAndReportsMissingFixtures(t *testing.T) {
	// Act
	verification := BriefVerification("task", "no-mistakes")

	// Assert
	for _, want := range []string{"repository environment", "existing fixtures", "untested", "disposable full application"} {
		if !strings.Contains(verification, want) {
			t.Errorf("verification lacks %q", want)
		}
	}
}
