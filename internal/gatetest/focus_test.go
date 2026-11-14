package gatetest

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestFocusedRepairIncludesFailuresAndTransitiveRepairChecks(t *testing.T) {
	// Arrange
	found := branch("c/c.go")
	found.packages = append(found.packages, pkg("d", "example.com/repo/b"))
	found.hasPolicy = true
	found.policy = `{"version":1,"slow_packages":["b"],"guards":[{"package":"c","tests":["TestGuard"],"why":"required guard"}]}`

	// Act
	plan, note, err := focused(context.Background(), nil, found, "test example.com/repo/c --- FAIL: TestBroken/subtest (0.01s)\npath a/a.go\n", Fast)

	// Assert
	if err != nil || note != "" {
		t.Fatalf("focused = %v, %s", err, note)
	}
	if !slices.Equal(plan.Tests, []string{"example.com/repo/a", "example.com/repo/b", "example.com/repo/d"}) || !slices.Equal(plan.Vet, plan.Tests) {
		t.Fatalf("repair checks lost dependents or ran the baseline change: %+v", plan)
	}
	if len(plan.Named) != 1 || !slices.Equal(plan.Named[0].Command, append(slices.Clone(StepTestArgs), "-run", "^(TestBroken)$", "example.com/repo/c")) || len(plan.Guards) != 1 {
		t.Fatalf("named failure or required guard missing: %+v", plan)
	}
}

func TestFocusedRepairDoesNotRepeatTestsCoveredByItsRepairPlan(t *testing.T) {
	// Act
	plan, _, err := focused(context.Background(), nil, branch("c/c.go"), "test example.com/repo/b --- FAIL: TestBroken (0.01s)\npath a/a.go\n", Fast)

	// Assert
	if err != nil || len(plan.Named) != 0 || len(plan.Failures) != 1 || !slices.Contains(plan.Tests, "example.com/repo/b") {
		t.Fatalf("named test was omitted or duplicated: %+v, %v", plan, err)
	}
}

// A repair round's check that cannot narrow to the repair's own reach runs
// the configured level over the repaired paths, never the full level, and
// says in one line why and what it leaves to CI (#2176): a full-level
// fallback held the machine's heavy line for two hours and forty minutes.
func TestAFocusedCheckThatCannotNarrowRunsTheConfiguredLevelAndNamesWhatCIIsLeft(t *testing.T) {
	for name, test := range map[string]struct {
		focus      string
		configured Level
		why        string
		over       string
		unmapped   string
	}{
		"a repair with no failed test":       {focus: "path a/a.go\n", configured: Fast, why: "the repair named no failed test", over: "the repaired paths"},
		"no failed test on a hosted CI":      {focus: "path a/a.go\n", configured: Changed, why: "the repair named no failed test", over: "the repaired paths"},
		"an unrecognized failed test":        {focus: "test compiler failed\npath a/a.go\n", configured: Fast, why: "an unrecognized or unmappable failed test", over: "the repaired paths", unmapped: `"compiler failed"`},
		"a failed test of no package":        {focus: "test example.com/repo/gone --- FAIL: TestBroken (0.01s)\npath a/a.go\n", configured: Fast, why: "an unrecognized or unmappable failed test", over: "the repaired paths", unmapped: "example.com/repo/gone TestBroken"},
		"an unclassified repair path":        {focus: "test example.com/repo/a --- FAIL: TestBroken (0.01s)\npath strange.input\n", configured: Fast, why: "a repair path has no mapped check or outside classification", over: "the repaired paths"},
		"a module file repaired":             {focus: "test example.com/repo/a --- FAIL: TestBroken (0.01s)\npath go.mod\n", configured: Fast, why: "go.mod or go.sum changed", over: "the repaired paths"},
		"no repaired path":                   {focus: "test example.com/repo/a --- FAIL: TestBroken (0.01s)\n", configured: Fast, why: "the repair named no repaired path", over: "the branch's change"},
		"nothing named":                      {focus: "", configured: Fast, why: "the repair named no repaired path", over: "the branch's change"},
		"a known and an unknown repair path": {focus: "test example.com/repo/a --- FAIL: TestBroken (0.01s)\npath a/a.go\npath strange.input\n", configured: Fast, why: "a repair path has no mapped check or outside classification", over: "the repaired paths"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			found := branch("a/a.go")
			found.hasPolicy, found.policy = true, `{"version":1,"outside":[]}`

			// Act
			plan, note, err := focused(context.Background(), nil, found, test.focus, test.configured)

			// Assert
			if err != nil || plan.Level != test.configured || slices.Contains(plan.Tests, "./...") {
				t.Fatalf("plan = level %s, tests %v, %v; want the configured level %s and never every package's tests", plan.Level, plan.Tests, err, test.configured)
			}
			for _, says := range []string{"focused check runs the configured level " + string(test.configured) + " over " + test.over + ", not the full level, since " + test.why, "left to CI, which runs every package: the "} {
				if !strings.Contains(note, says) {
					t.Errorf("note = %q, want it to say %q", note, says)
				}
			}
			if test.unmapped != "" && !strings.Contains(note, "the failed tests it could not map: "+test.unmapped) {
				t.Errorf("note = %q, want it to name the failed test it could not map, %s", note, test.unmapped)
			}
		})
	}
}

// A failed test the check maps still runs by name beside the configured
// level, even when the round could not narrow for another reason.
func TestAFocusedCheckThatCannotNarrowStillRunsTheFailedTestsItMaps(t *testing.T) {
	// Arrange
	found := branch("a/a.go")
	found.hasPolicy, found.policy = true, `{"version":1,"outside":[]}`

	// Act
	plan, note, err := focused(context.Background(), nil, found, "test example.com/repo/c --- FAIL: TestBroken (0.01s)\npath a/a.go\npath strange.input\n", Fast)

	// Assert
	if err != nil || note == "" || len(plan.Named) != 1 || !slices.Equal(plan.Named[0].Command, append(slices.Clone(StepTestArgs), "-run", "^(TestBroken)$", "example.com/repo/c")) {
		t.Fatalf("plan = %+v, %q, %v; want the mapped failed test run by name", plan.Named, note, err)
	}
}

func TestFocusedRepairBroadensUnmappedPathsWithoutAClassifyingPolicy(t *testing.T) {
	// Act
	plan, note, err := focused(context.Background(), nil, branch("a/a.go"), "test example.com/repo/a --- FAIL: TestBroken (0.01s)\npath a/a.go\npath unknown.input\n", Fast)

	// Assert
	if err != nil || !strings.Contains(note, "not the full level") || plan.Level != Fast {
		t.Fatalf("unmapped legacy-policy path: %+v, %q, %v", plan, note, err)
	}
}

func TestFocusedRepairRetainsUnmappedFailureObligations(t *testing.T) {
	for _, policy := range []string{`{"version":1}`, `{"version":1,"alone_packages":["c"]}`} {
		t.Run(policy, func(t *testing.T) {
			found := branch("a/a.go")
			found.hasPolicy, found.policy = true, policy
			text := "test example.com/repo/gone --- FAIL: TestBroken/subtest (0.01s)\n" +
				"test example.com/repo/gone --- FAIL: TestBroken (0.01s)\npath a/a.go\n"

			plan, note, err := focused(context.Background(), nil, found, text, Fast)

			if err != nil || plan.Level != Fast || !strings.Contains(note, "unmappable") || !slices.Equal(plan.Failures, []FailedTest{{Package: "example.com/repo/gone", Name: "TestBroken"}}) || len(plan.Named) != 0 {
				t.Fatalf("unmapped failure obligation lost: %+v, %q, %v", plan, note, err)
			}
		})
	}
}

func TestFocusedRepairRejectsUnsafePathsAndEncoding(t *testing.T) {
	for _, value := range []string{"..", "../a/a.go", "/a/a.go", `C:\a\a.go`, `a\na.go`, "a/../b.go", "a/a.go\x00", "a/\xff.go"} {
		t.Run(value, func(t *testing.T) {
			// Act
			_, _, err := focused(context.Background(), nil, branch("a/a.go"), "test example.com/repo/a --- FAIL: TestBroken (0.01s)\npath "+value+"\n", Fast)

			// Assert
			if err == nil {
				t.Fatal("unsafe data accepted")
			}
		})
	}
}

func TestFocusedRepairTreatsShellTextAsUnrecognizedData(t *testing.T) {
	// Act
	plan, note, err := focused(context.Background(), nil, branch("a/a.go"), "test $(touch injected)\npath a/a.go\n", Fast)

	// Assert
	if err != nil || !strings.Contains(note, `"$(touch injected)"`) || plan.Level != Fast || len(plan.Named) != 0 {
		t.Fatalf("shell text was not named as data: %+v, %q, %v", plan, note, err)
	}
}

func TestFocusedRepairGroupsDistinctFailuresAndKeepsAlonePackagesSerial(t *testing.T) {
	// Arrange
	found := branch("a/a.go")
	found.hasPolicy, found.policy = true, `{"version":1,"alone_packages":["c"]}`
	text := "test example.com/repo/c --- FAIL: TestSecond/sub (0.01s)\n" +
		"test example.com/repo/c --- FAIL: TestFirst (0.01s)\n" +
		"test example.com/repo/c --- FAIL: TestSecond (0.01s)\npath a/a.go\n"

	// Act
	plan, note, err := focused(context.Background(), nil, found, text, Fast)

	// Assert
	if err != nil || note != "" || len(plan.Named) != 1 || len(plan.Failures) != 2 {
		t.Fatalf("focused = %+v, %q, %v", plan, note, err)
	}
	want := []string{"go", "test", "-json", "-count=1", "-p", "1", "-timeout", "45m", "-run", "^(TestFirst|TestSecond)$", "example.com/repo/c"}
	if !slices.Equal(plan.Named[0].Command, want) {
		t.Fatalf("command=%v, want %v", plan.Named[0].Command, want)
	}
}
