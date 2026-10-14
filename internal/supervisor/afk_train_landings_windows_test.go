package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

// A train the CFO starts detached does not run under the registered CFO and
// cannot write the decision log, so it asks the supervisor to log a landing,
// and the supervisor logs what the train's record and GitHub say.

const landedPull = "https://github.com/o/r/pull/821"

// writeTrain keeps a train's record in the home, as the train keeps it.
func writeTrain(t *testing.T, stateDir string, record train.Train) {
	t.Helper()
	record.Schema = train.Schema
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(train.Dir(stateDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(train.Dir(stateDir), record.ID+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func landedTrain() train.Train {
	return train.Train{
		ID: "r-142332", Repository: "o/r", Base: "main", PR: "https://github.com/o/r/pull/900", Head: "abc1234def", BaseSHA: "1234567890", State: train.StateLanded,
		History: []train.Run{{Number: 1, Riders: []int{821}, Base: "1111111aaa", Head: "def5678abc", PR: "https://github.com/o/r/pull/890", Result: train.RunLanded}},
		Cars:    []train.Car{{Number: 821, URL: landedPull, State: train.CarLanded}, {Number: 822, URL: "https://github.com/o/r/pull/822", State: train.CarReturned}},
	}
}

func mergeLines(t *testing.T, stateDir string) []afk.Entry {
	t.Helper()
	switched, err := afk.Read(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	entries, _, err := afk.Entries(stateDir, switched.Session)
	if err != nil {
		t.Fatal(err)
	}
	var merges []afk.Entry
	for _, entry := range entries {
		if entry.Kind == afk.KindMerge {
			merges = append(merges, entry)
		}
	}
	return merges
}

func TestTheSupervisorLogsALandingOfATrainItDidNotStartOnceFromItsRecord(t *testing.T) {
	// Arrange
	s, h, _ := asTheCFO(t)
	if err := SwitchAFKAtHisAsk(h, true, hisAskOn); err != nil {
		t.Fatal(err)
	}
	writeTrain(t, h.State, landedTrain())
	github := &gitHubSays{state: map[string]string{landedPull: "MERGED"}}
	s.Options.PullRequestState = github.read

	// Act
	first := LogTrainLanding(h, "r-142332", landedPull)
	second := LogTrainLanding(h, "r-142332", landedPull)

	// Assert
	merges := mergeLines(t, h.State)
	if first != nil || second != nil || len(merges) != 1 {
		t.Fatalf("asked twice = %v and %v, merge lines = %+v, want the landing logged once", first, second, merges)
	}
	if merges[0].What != landedPull || merges[0].Outcome != afk.OutcomeMerged || merges[0].Evidence != "merge train r-142332: CI passed on https://github.com/o/r/pull/890 at def5678, built on main at 1111111" {
		t.Errorf("merge line = %+v, want the pull request merged, with the run that landed it", merges[0])
	}
}

func TestTheSupervisorLogsNoLandingThatIsNotOnTheTrainsRecordOrNotMergedOnGitHub(t *testing.T) {
	for name, tc := range map[string]struct {
		train, url, state string
		said              string
	}{
		"a car the train did not land": {"r-142332", "https://github.com/o/r/pull/822", "MERGED", "does not hold"},
		"a pull request not on it":     {"r-142332", "https://github.com/o/r/pull/999", "MERGED", "does not hold"},
		"a train that is not kept":     {"r-none", landedPull, "MERGED", "merge train r-none"},
		"a path in place of an id":     {"../r-142332", landedPull, "MERGED", "by its id"},
		"a pull request still open":    {"r-142332", landedPull, "OPEN", "not merged"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			s, h, _ := asTheCFO(t)
			if err := SwitchAFKAtHisAsk(h, true, hisAskOn); err != nil {
				t.Fatal(err)
			}
			writeTrain(t, h.State, landedTrain())
			github := &gitHubSays{state: map[string]string{tc.url: tc.state}}
			s.Options.PullRequestState = github.read

			// Act
			err := LogTrainLanding(h, tc.train, tc.url)

			// Assert
			if err == nil || !strings.Contains(err.Error(), tc.said) || len(mergeLines(t, h.State)) != 0 {
				t.Errorf("LogTrainLanding = %v, merge lines = %+v, want it refused with %q and nothing logged", err, mergeLines(t, h.State), tc.said)
			}
		})
	}
}

func TestTheSupervisorLogsNoTrainLandingWhileAFKModeIsOff(t *testing.T) {
	// Arrange
	_, h, _ := asTheCFO(t)
	writeTrain(t, h.State, landedTrain())

	// Act
	err := LogTrainLanding(h, "r-142332", landedPull)
	entries, _, readErr := afk.Entries(h.State, "")

	// Assert
	if err != nil || readErr != nil || len(entries) != 0 {
		t.Errorf("LogTrainLanding = %v, log = %+v (%v), want nothing logged and nothing refused", err, entries, readErr)
	}
}
