package fsx

import (
	"testing"
	"time"
)

// WaitWithinForTest makes state file operations, until the test ends, wait
// out a held file within budget and sleep each wait through sleepFor.
func WaitWithinForTest(t testing.TB, budget time.Duration, sleepFor func(time.Duration)) {
	originalBudget, originalSleep := transientBudget, sleep
	t.Cleanup(func() { transientBudget, sleep = originalBudget, originalSleep })
	transientBudget, sleep = budget, sleepFor
}
