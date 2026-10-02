package supervisor

import "testing"

// Throwaway, removed before merge: its failure must turn the test check red.
func TestCIProofThisJobFails(t *testing.T) {
	t.Fatal("deliberate failure: proof that this job turns the test check red")
}
