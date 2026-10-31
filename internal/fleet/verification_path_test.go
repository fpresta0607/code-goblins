package fleet

import (
	"strings"
	"testing"
)

func TestStrictQueuedBriefAssignsChecksToOnePipelineOwner(t *testing.T) {
	// Arrange
	queued := QueuedTask{Row: BacklogRow{ID: "task", Repo: "demo", Title: "Change an approved report", Mode: "no-mistakes"}}

	// Act: the emitted brief is the worker's owned instruction interface.
	brief := QueuedBriefText(queued)
	_, verification, _ := strings.Cut(brief, "## Verification\n\n")
	verification, _, _ = strings.Cut(verification, "\n\n## Authentication")

	// Assert
	if !strings.Contains(verification, "pipeline Test step") || !strings.Contains(verification, "once") || strings.Contains(verification, "Before each push run") {
		t.Fatalf("strict brief duplicates or omits its check owner: %s", verification)
	}
}
