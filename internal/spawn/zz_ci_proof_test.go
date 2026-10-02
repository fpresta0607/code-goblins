package spawn

import "testing"

// Throwaway, removed before merge: only the job that skips the tests named
// for spawning runs this, and its failure must turn the test check red.
func TestCIProofTheOtherHalfFails(t *testing.T) {
	t.Fatal("deliberate failure: proof that this job turns the test check red")
}
