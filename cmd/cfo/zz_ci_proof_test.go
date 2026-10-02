package main

import "testing"

// Throwaway, removed before merge: only the job that runs the update and
// recovery tests runs this, and its failure must turn the test check red.
func TestCIProofUpdateHalfFails(t *testing.T) {
	t.Fatal("deliberate failure: proof that this job turns the test check red")
}

// Throwaway: only the job that skips the update and recovery tests runs this.
func TestCIProofTheOtherHalfFails(t *testing.T) {
	t.Fatal("deliberate failure: proof that this job turns the test check red")
}
