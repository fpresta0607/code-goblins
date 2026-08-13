package claudehook

import (
	"os"
	"strconv"
	"time"
)

// Seconds reads the env var name as an integer count of seconds and returns
// it as a time.Duration. It ports upstream's env-driven timing knobs (e.g.
// FM_GUARD_GRACE) to the CFO_ prefix (e.g. CFO_GUARD_GRACE): an unset or
// non-integer value falls back to def seconds, matching upstream's
// fail-to-default behavior for malformed overrides.
func Seconds(name string, def int) time.Duration {
	n, err := strconv.Atoi(os.Getenv(name))
	if err != nil {
		return time.Duration(def) * time.Second
	}
	return time.Duration(n) * time.Second
}

// Int reads the env var name as an integer, clamped to [min, max]. It ports
// upstream's env-driven bounded knobs (e.g. FM_CLAUDE_AUTOARM_ATTEMPTS) to
// the CFO_ prefix (e.g. CFO_CLAUDE_AUTOARM_ATTEMPTS): an unset or
// non-integer value falls back to def unclamped, matching upstream's
// fail-to-default behavior for malformed overrides; a valid value outside
// [min, max] is clamped to the nearer bound.
func Int(name string, def, min, max int) int {
	n, err := strconv.Atoi(os.Getenv(name))
	if err != nil {
		return def
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}
