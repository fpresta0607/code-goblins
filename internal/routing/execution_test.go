package routing

import "testing"

func lanes() map[string]ExecutionLane {
	return map[string]ExecutionLane{"open-scout": {Name: "open-scout", Harness: "pi"}, "open-builder": {Name: "open-builder", Harness: "pi"}, "rescue": {Name: "rescue", Harness: "codex", Effort: "high"}}
}
func TestChooseMechanicalCheap(t *testing.T) {
	x := ChooseExecution(Assessment{Class: Mechanical}, lanes(), "open-builder", "rescue")
	if x.Harness != "pi" {
		t.Fatal(x)
	}
}
func TestChooseHighRiskEscalates(t *testing.T) {
	x := ChooseExecution(Assessment{Class: Security, Risk: "high"}, lanes(), "open-builder", "rescue")
	if x.Harness != "codex" {
		t.Fatal(x)
	}
}
func TestExplicitHarnessWins(t *testing.T) {
	x := ChooseExecution(Assessment{Class: Security, Risk: "high", ExplicitHarness: "claude", ExplicitModel: "x"}, lanes(), "open-builder", "rescue")
	if x.Harness != "claude" || x.Model != "x" {
		t.Fatal(x)
	}
}
func TestRetryEscalates(t *testing.T) {
	x := ChooseExecution(Assessment{Class: Implementation, Attempts: 2}, lanes(), "open-builder", "rescue")
	if x.Name != "rescue" {
		t.Fatal(x)
	}
}
