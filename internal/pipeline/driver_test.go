package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestReviewDecisionBudgetAndUnresolvedFindings(t *testing.T) {
	selection, err := testPolicy(t).Select("ordinary")
	if err != nil {
		t.Fatal(err)
	}
	gate := Gate{RunID: "run", StepID: "step", Step: "review", Status: "awaiting_approval", Round: 1, Findings: `{"findings":[{"id":"bug","action":"auto-fix"}]}`}
	args, err := ResponseArgs(selection, gate, Response{Action: "fix", Findings: "bug"})
	if err != nil || !strings.Contains(strings.Join(args, " "), "--step review") {
		t.Fatalf("first repair: %v %v", args, err)
	}
	gate.Round = 3
	if _, err := ResponseArgs(selection, gate, Response{Action: "fix", Findings: "bug"}); !errors.Is(err, ErrUnresolved) {
		t.Fatalf("exhausted: %v", err)
	}
	if _, err := ResponseArgs(selection, gate, Response{Action: "approve"}); !errors.Is(err, ErrUnresolved) {
		t.Fatalf("approve bypass: %v", err)
	}
	gate.Findings = `{"findings":[{"id":"note","action":"no-op"}]}`
	if _, err := ResponseArgs(selection, gate, Response{Action: "approve"}); err != nil {
		t.Fatalf("clean final review: %v", err)
	}
	gate.Findings = `{"findings":[{"id":"bug","action":"auto-fix"}]}`
	selection, err = testPolicy(t).Select("high-risk")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResponseArgs(selection, gate, Response{Action: "fix", Findings: "bug"}); err != nil {
		t.Fatalf("third high-risk repair: %v", err)
	}
	gate.Round = 4
	if _, err := ResponseArgs(selection, gate, Response{Action: "fix", Findings: "bug"}); !errors.Is(err, ErrUnresolved) {
		t.Fatalf("high-risk exhausted: %v", err)
	}
}

func TestResponseRefusesUnsafeAndStaleDecisions(t *testing.T) {
	selection, err := testPolicy(t).Select("ordinary")
	if err != nil {
		t.Fatal(err)
	}
	ten := 10
	valid := Gate{RunID: "run", StepID: "step", Step: "review", Status: "awaiting_approval", Round: 1, Findings: `{"findings":[{"id":"bug","action":"auto-fix"}]}`}
	for _, mutate := range []func(*Gate){
		func(g *Gate) { g.AutoFixLimit = &ten },
		func(g *Gate) { g.Status = "fixing" }, func(g *Gate) { g.Selected = "user" },
		func(g *Gate) { g.Round = 0 }, func(g *Gate) { g.Findings = `{}` },
		func(g *Gate) { g.Findings = `{"findings":[{"id":"bug","action":"new-unknown-action"}]}` },
	} {
		gate := valid
		mutate(&gate)
		if _, err := ResponseArgs(selection, gate, Response{Action: "fix", Findings: "bug"}); err == nil {
			t.Fatalf("unsafe gate accepted: %+v", gate)
		}
	}
	for _, response := range []Response{{Action: "skip"}, {Action: "approve"}, {Action: "fix"}, {Action: "fix", Findings: "absent"}} {
		if _, err := ResponseArgs(selection, valid, response); err == nil {
			t.Fatalf("unsafe response: %+v", response)
		}
	}
}

func TestIdleLockIsReleasedOnUnreadableDatabase(t *testing.T) {
	// A missing database is not proof of idleness; no daemon or config is changed.
	r := Reader{Root: t.TempDir()}
	if release, err := r.Idle(context.Background()); err == nil {
		release()
		t.Fatal("missing database accepted as idle")
	}
}
