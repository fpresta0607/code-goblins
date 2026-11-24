package runtime

import (
	"strings"
	"testing"
)

// On Windows a path written with forward slashes, a trailing separator or a
// different case is the same directory. Docker labels, task metadata and the
// process parameter block each spell them differently on this machine, so an
// attribution that matched literally would report a live goblin as a leftover.
func TestAttributionOfMatchesPathsWrittenAnyWay(t *testing.T) {
	attribution := NewAttribution(machineFixture())
	for _, spelling := range []string{
		peakLiveTree,
		strings.ToLower(peakLiveTree),
		strings.ReplaceAll(peakLiveTree, `\`, "/"),
		peakLiveTree + `\`,
	} {
		if owner := attribution.Of(spelling, ""); owner.Kind != OwnerGoblin {
			t.Errorf("Of(%q) = %q, want goblin", spelling, owner.Kind)
		}
	}
}
