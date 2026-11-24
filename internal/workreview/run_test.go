package workreview

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
)

// fixtureStates is what GitHub says of the numbers the fixture home names.
var fixtureStates = map[string]map[int]string{
	"acme/widgets": {11: "closed", 30: "open", 40: "open", 41: "open", 50: "open", 60: "open"},
	"acme/gadgets": {8: "open"},
}

func TestRunFindsEachKindWithOneReadForEachRepository(t *testing.T) {
	// Arrange
	h, checkout := fixtureHome(t)
	reads := map[string]int{}
	read := func(_ context.Context, repository string, numbers []int) (map[int]string, error) {
		reads[repository]++
		states := map[int]string{}
		for _, number := range numbers {
			states[number] = fixtureStates[repository][number]
		}
		return states, nil
	}

	// Act
	report, err := Run(context.Background(), h, fixtureResolver(checkout), read, time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC), 7*24*time.Hour)

	// Assert
	if err != nil || len(report.Problems) != 0 {
		t.Fatalf("Run: problems %q, err %v", report.Problems, err)
	}
	var found []string
	for _, finding := range report.Findings {
		found = append(found, finding.Key())
	}
	want := []string{
		"done already:queued-done",
		"overtaken:his-step",
		"fold:fold-a,fold-b",
		"stale:paused-task",
		"stale:held-elsewhere",
		"stale:parked-old",
	}
	if !slices.Equal(found, want) {
		t.Errorf("findings = %q, want %q", found, want)
	}
	if wantReads := map[string]int{"acme/widgets": 1, "acme/gadgets": 1}; !maps.Equal(reads, wantReads) {
		t.Errorf("reads = %v, want %v", reads, wantReads)
	}
}

func TestRunRecordsAFailedReadAsAProblemAndSettlesNothingByIt(t *testing.T) {
	// Arrange
	h, checkout := fixtureHome(t)
	read := func(context.Context, string, []int) (map[int]string, error) {
		return nil, errors.New("GitHub is unreachable")
	}

	// Act
	report, err := Run(context.Background(), h, fixtureResolver(checkout), read, time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC), 7*24*time.Hour)

	// Assert
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Problems) != 2 || !strings.Contains(report.Problems[0], "acme/gadgets") || !strings.Contains(report.Problems[1], "acme/widgets") {
		t.Errorf("problems = %q, want one for each repository, in order", report.Problems)
	}
	for _, finding := range report.Findings {
		if finding.Kind == KindDone {
			t.Errorf("finding %s settled an issue that was never read", finding.Key())
		}
	}
}
