package verify

import (
	"os"
	"path/filepath"
	"testing"
)

// The store keeps how long each of a project's tests took, and a later run
// adds what it timed to what is there: a run of one package forgets nothing
// of another, and a test timed again has its latest time.
func TestKeepTimesAddsARunsTimesToTheProjectsRecord(t *testing.T) {
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	first := map[string]map[string]float64{"example.com/m/a": {"TestOne": 1.5, "TestTwo": 30}}
	second := map[string]map[string]float64{"example.com/m/a": {"TestTwo": 4}, "example.com/m/b": {"TestThree": 0.2}}

	// Act
	before, beforeErr := Times("m")
	firstErr := KeepTimes("m", first)
	secondErr := KeepTimes("m", second)
	nothingErr := KeepTimes("m", nil)
	times, err := Times("m")
	other, otherErr := Times("another")

	// Assert
	if beforeErr != nil || firstErr != nil || secondErr != nil || nothingErr != nil || err != nil || otherErr != nil {
		t.Fatalf("errors: %v, %v, %v, %v, %v, %v; want none", beforeErr, firstErr, secondErr, nothingErr, err, otherErr)
	}
	if len(before) != 0 || len(other) != 0 {
		t.Errorf("a project with no record has %v and another project %v, want nothing of either", before, other)
	}
	if len(times) != 2 || len(times["example.com/m/a"]) != 2 || times["example.com/m/a"]["TestOne"] != 1.5 || times["example.com/m/a"]["TestTwo"] != 4 || times["example.com/m/b"]["TestThree"] != 0.2 {
		t.Errorf("Times = %v, want TestOne 1.5 and TestTwo 4 in a and TestThree 0.2 in b", times)
	}
}

// A record that does not parse is said to be unreadable when it is read, and
// the next run's times replace it: one bad write never ends the record.
func TestKeepTimesReplacesARecordThatCannotBeRead(t *testing.T) {
	// Arrange
	store := t.TempDir()
	t.Setenv("CFO_VERIFY_DIR", store)
	if err := os.MkdirAll(filepath.Join(store, "times"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "times", "m.json"), []byte("{half"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Act
	_, brokenErr := Times("m")
	keepErr := KeepTimes("m", map[string]map[string]float64{"example.com/m/a": {"TestOne": 1}})
	times, err := Times("m")

	// Assert
	if brokenErr == nil {
		t.Error("Times read a record that does not parse with no error")
	}
	if keepErr != nil || err != nil || times["example.com/m/a"]["TestOne"] != 1 {
		t.Errorf("after KeepTimes (%v) Times = %v, %v; want TestOne 1", keepErr, times, err)
	}
}
