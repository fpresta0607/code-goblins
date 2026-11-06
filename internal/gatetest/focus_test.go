package gatetest

import (
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
	plan, note, err := focused(found, "test example.com/repo/c --- FAIL: TestBroken/subtest (0.01s)\npath a/a.go\n")

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
	plan, _, err := focused(branch("c/c.go"), "test example.com/repo/b --- FAIL: TestBroken (0.01s)\npath a/a.go\n")

	// Assert
	if err != nil || len(plan.Named) != 0 || len(plan.Failures) != 1 || !slices.Contains(plan.Tests, "example.com/repo/b") {
		t.Fatalf("named test was omitted or duplicated: %+v, %v", plan, err)
	}
}

func TestFocusedRepairBroadensUnrecognizedData(t *testing.T) {
	for name, focus := range map[string]string{
		"unknown failure":         "test compiler failed\npath a/a.go\n",
		"missing package":         "test example.com/repo/gone --- FAIL: TestBroken (0.01s)\npath a/a.go\n",
		"unknown path":            "test example.com/repo/a --- FAIL: TestBroken (0.01s)\npath strange.input\n",
		"module change":           "test example.com/repo/a --- FAIL: TestBroken (0.01s)\npath go.mod\n",
		"empty":                   "",
		"missing paths":           "test example.com/repo/a --- FAIL: TestBroken (0.01s)\n",
		"mixed known and unknown": "test example.com/repo/a --- FAIL: TestBroken (0.01s)\npath a/a.go\npath strange.input\n",
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			found := branch("a/a.go")
			found.hasPolicy, found.policy = true, `{"version":1,"outside":[]}`

			// Act
			plan, note, err := focused(found, focus)

			// Assert
			if err != nil || note == "" || plan.Level != Full || !slices.Equal(plan.Tests, []string{"./..."}) {
				t.Fatalf("unsafe narrowing: %+v, %q, %v", plan, note, err)
			}
		})
	}
}

func TestFocusedRepairBroadensUnmappedPathsWithoutAClassifyingPolicy(t *testing.T) {
	// Act
	plan, note, err := focused(branch("a/a.go"), "test example.com/repo/a --- FAIL: TestBroken (0.01s)\npath a/a.go\npath unknown.input\n")

	// Assert
	if err != nil || note == "" || plan.Level != Full {
		t.Fatalf("unmapped legacy-policy path narrowed: %+v, %q, %v", plan, note, err)
	}
}

func TestFocusedRepairRetainsUnmappedFailureObligations(t *testing.T) {
	for _, policy := range []string{`{"version":1}`, `{"version":1,"alone_packages":["c"]}`} {
		t.Run(policy, func(t *testing.T) {
			found := branch("a/a.go")
			found.hasPolicy, found.policy = true, policy
			text := "test example.com/repo/gone --- FAIL: TestBroken/subtest (0.01s)\n" +
				"test example.com/repo/gone --- FAIL: TestBroken (0.01s)\npath a/a.go\n"

			plan, note, err := focused(found, text)

			if err != nil || plan.Level != Full || !strings.Contains(note, "unmappable") || !slices.Equal(plan.Failures, []FailedTest{{Package: "example.com/repo/gone", Name: "TestBroken"}}) {
				t.Fatalf("unmapped failure obligation lost: %+v, %q, %v", plan, note, err)
			}
		})
	}
}

func TestFocusedRepairRejectsUnsafePathsAndEncoding(t *testing.T) {
	for _, value := range []string{"..", "../a/a.go", "/a/a.go", `C:\a\a.go`, `a\na.go`, "a/../b.go", "a/a.go\x00", "a/\xff.go"} {
		t.Run(value, func(t *testing.T) {
			// Act
			_, _, err := focused(branch("a/a.go"), "test example.com/repo/a --- FAIL: TestBroken (0.01s)\npath "+value+"\n")

			// Assert
			if err == nil {
				t.Fatal("unsafe data accepted")
			}
		})
	}
}

func TestFocusedRepairTreatsShellTextAsUnrecognizedData(t *testing.T) {
	// Act
	plan, note, err := focused(branch("a/a.go"), "test $(touch injected)\npath a/a.go\n")

	// Assert
	if err != nil || !strings.Contains(note, "full") || !slices.Equal(plan.Tests, []string{"./..."}) {
		t.Fatalf("shell text did not broaden safely: %+v, %q, %v", plan, note, err)
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
	plan, note, err := focused(found, text)

	// Assert
	if err != nil || note != "" || len(plan.Named) != 1 || len(plan.Failures) != 2 {
		t.Fatalf("focused = %+v, %q, %v", plan, note, err)
	}
	want := []string{"go", "test", "-json", "-count=1", "-p", "1", "-timeout", "45m", "-run", "^(TestFirst|TestSecond)$", "example.com/repo/c"}
	if !slices.Equal(plan.Named[0].Command, want) {
		t.Fatalf("command=%v, want %v", plan.Named[0].Command, want)
	}
}
