// Package access keeps the fleet's folders to the accounts their work needs:
// the person the fleet runs as, SYSTEM and Administrators. A folder made
// under a drive's root, as a home that CFO_HOME puts there or a Dev Drive
// folder is, takes Authenticated Users Modify from it, so any second account
// on the machine could read and write it.
package access

import (
	"fmt"
	"strings"
)

// UnfinishedMarker is the file a narrowing that left something with its
// access writes in the folder it narrowed, naming what it left, so the next
// look at the folder reads it as not narrowed and narrows it again. A
// narrowing that changes everything removes it.
const UnfinishedMarker = ".cfo-narrowing-unfinished"

// Unchanged is Narrow's error when it narrowed a folder but some of what the
// folder holds kept the access it had.
type Unchanged struct {
	// Count is how many files and folders kept their access; a folder that
	// kept its access keeps what it holds open with it.
	Count int
	// Examples name the first few, each with why.
	Examples []string
}

func (u *Unchanged) Error() string {
	return fmt.Sprintf("%d of the files and folders in it kept their access, with everything those folders hold, such as %s", u.Count, strings.Join(u.Examples, "; "))
}
