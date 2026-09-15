package pipeline

import (
	"path/filepath"
	"testing"
)

func TestTaskRepairBudgetCannotResetWithRunOrRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budget.json")
	for i, run := range []string{"first", "first", "successor"} {
		b, err := LoadBudget(path, "policy", 3)
		if err != nil {
			t.Fatal(err)
		}
		if err = b.Reserve(path, Gate{RunID: run, StepID: "review", Round: i + 1}); err != nil {
			t.Fatal(err)
		}
	}
	b, err := LoadBudget(path, "policy", 3)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Reserve(path, Gate{RunID: "new-run", StepID: "review", Round: 1}); err == nil {
		t.Fatal("new run reset exhausted budget")
	}
	if _, err = LoadBudget(path, "different-policy", 3); err == nil {
		t.Fatal("policy rebound accepted")
	}
	if err = b.Reserve(path, Gate{RunID: "first", StepID: "review", Round: 1}); err != nil {
		t.Fatal("same response was charged twice", err)
	}
}

func TestLaunchBindingRejectsReboundRunIdentity(t *testing.T) {
	project := t.TempDir()
	binding := Launch{Project: project, Branch: "feat", Head: "head", Nonce: "nonce", Generation: "generation", IntentDigest: "digest", RunID: "run"}
	run := NativeRun{RunID: "run", Project: project, Branch: "feat", Head: "head", Nonce: "nonce", Generation: "generation", IntentDigest: "digest"}
	if err := binding.Verify(run); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*NativeRun){func(r *NativeRun) { r.RunID = "other" }, func(r *NativeRun) { r.Project = "C:/other" }, func(r *NativeRun) { r.Head = "stale" }, func(r *NativeRun) { r.Nonce = "stale" }} {
		other := run
		change(&other)
		if binding.Verify(other) == nil {
			t.Fatal("rebound identity accepted", other)
		}
	}
}
