package supervisor

import (
	"fmt"
	"strings"
)

// failedOnceClause is what a ci_finished wake adds about the tests that
// failed once and passed on their second try, as train.FailedOnce read them:
// how many and which, or that they could not be read, which is said rather
// than passed over as none. With none it adds nothing.
func failedOnceClause(names []string, err error) string {
	switch {
	case err != nil:
		return fmt.Sprintf(", and the tests that failed once could not be read (%s)", bounded(err.Error(), 200))
	case len(names) == 0:
		return ""
	}
	return fmt.Sprintf(", and %d failed once and passed on the second try (%s)", len(names), strings.Join(names, ", "))
}
