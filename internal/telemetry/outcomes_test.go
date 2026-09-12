package telemetry

import (
	"path/filepath"
	"testing"
)

func TestOutcomeRoundTripAndScore(t *testing.T) {
	p := filepath.Join(t.TempDir(), "o.jsonl")
	x := Outcome{TaskClass: "implementation", Harness: "pi", Accepted: true, FirstPassTests: true, DurationSeconds: 10}
	if e := AppendOutcome(p, x); e != nil {
		t.Fatal(e)
	}
	xs, e := ReadOutcomes(p)
	if e != nil || len(xs) != 1 {
		t.Fatal(xs, e)
	}
	if Score(xs) <= 0 {
		t.Fatal(Score(xs))
	}
}
func TestBestByClass(t *testing.T) {
	xs := []Outcome{{TaskClass: "x", Harness: "pi", Accepted: true}, {TaskClass: "x", Harness: "codex", Accepted: false, RepairRounds: 2}}
	b, e := BestByClass(xs, "x")
	if e != nil || b != "pi|" {
		t.Fatal(b, e)
	}
}
