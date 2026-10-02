package supervisor

import "testing"

// Throwaway, removed before merge: only the job that runs the tests named
// from A to M runs this, and its failure must turn the test check red.
func TestCIProofFirstHalfFails(t *testing.T) {
	t.Fatal("deliberate failure: proof that this job turns the test check red")
}

// Throwaway: only the job that skips the tests named from A to M runs this.
func TestZCIProofSecondHalfFails(t *testing.T) {
	t.Fatal("deliberate failure: proof that this job turns the test check red")
}
