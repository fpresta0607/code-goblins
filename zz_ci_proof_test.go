package codegoblins

import "testing"

// Throwaway, removed before merge: the rest job runs this, and its failure
// must turn the test check red.
func TestCIProofThisJobFails(t *testing.T) {
	t.Fatal("deliberate failure: proof that this job turns the test check red")
}
