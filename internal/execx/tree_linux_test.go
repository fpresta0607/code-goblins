package execx

import (
	"testing"

	"golang.org/x/sys/unix"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

// A process in a session of its own whose environment cannot be read, as
// one that made itself undumpable cannot be by its own user, may carry the
// tree's mark, so the walk does not stop at it: it stays a member and ends
// with the tree.
func TestAChildInASessionOfItsOwnWhoseMarkCannotBeReadStaysInTheTree(t *testing.T) {
	playSessionRole(func() error { return unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0) })
	// Arrange
	tree, launcher, child := launchUnderTree(t, "TestAChildInASessionOfItsOwnWhoseMarkCannotBeReadStaysInTheTree", true, false)
	if _, err := proc.Environment(child); err == nil {
		t.Skip("this process may read an undumpable process's environment, as root may")
	}

	assertEndedWithTree(t, tree, launcher, child, true)
}
