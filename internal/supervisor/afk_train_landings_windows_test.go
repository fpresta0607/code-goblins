package supervisor

import (
	"context"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/train"
)

// A train's every landing writes its record while AFK mode is on, whichever
// run tested it, and a pull request is merged once in a stretch.
func TestATrainThatFoundARiderAlreadyOnItsBaseLogsItAsMergedOnce(t *testing.T) {
	// Arrange
	s, h, _ := asTheCFO(t)
	if err := SwitchAFKAtHisAsk(h, true, hisAskOn); err != nil {
		t.Fatal(err)
	}
	engine := s.trainEngine(nil, "o/r")
	pull := "https://github.com/o/r/pull/821"
	earlier := train.Train{ID: "r-134810", Base: "main"}
	found := train.Train{ID: "r-142332", Base: "main"}
	car := train.Car{URL: pull, State: train.CarLanded, Note: "already on main"}

	// Act
	engine.Landed(context.Background(), earlier, train.Car{URL: pull, State: train.CarLanded})
	engine.Landed(context.Background(), found, car)
	switched, err := afk.Read(h.State)
	if err != nil {
		t.Fatal(err)
	}
	entries, _, err := afk.Entries(h.State, switched.Session)

	// Assert
	var merges []afk.Entry
	for _, entry := range entries {
		if entry.Kind == afk.KindMerge {
			merges = append(merges, entry)
		}
	}
	if err != nil || len(merges) != 1 || merges[0].What != pull || merges[0].Outcome != afk.OutcomeMerged {
		t.Fatalf("merge lines = %+v (%v), want the pull request logged as merged once", merges, err)
	}
}

func TestATrainThatFoundARiderAlreadyOnItsBaseLogsItWithoutARun(t *testing.T) {
	// Arrange
	s, h, _ := asTheCFO(t)
	if err := SwitchAFKAtHisAsk(h, true, hisAskOn); err != nil {
		t.Fatal(err)
	}
	engine := s.trainEngine(nil, "o/r")
	pull := "https://github.com/o/r/pull/821"

	// Act
	engine.Landed(context.Background(), train.Train{ID: "r-142332", Base: "main"}, train.Car{URL: pull, State: train.CarLanded, Note: "already on main"})
	switched, _ := afk.Read(h.State)
	entries, _, err := afk.Entries(h.State, switched.Session)

	// Assert
	last := entries[len(entries)-1]
	if err != nil || last.Kind != afk.KindMerge || !strings.Contains(last.Evidence, "found "+pull+" already on main") || strings.Contains(last.Evidence, "CI passed") {
		t.Errorf("last line = %+v, want the merge with the train finding it already on main and no run it did not have", last)
	}
}
