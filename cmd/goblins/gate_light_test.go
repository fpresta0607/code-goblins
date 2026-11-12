package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/standin"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// holdHeavyTurn takes a heavy turn in the line slots keeps, as a long suite
// would, and gives it back when the test ends.
func holdHeavyTurn(t *testing.T, slots string) {
	t.Helper()
	holder, err := verify.Admission{Dir: slots, Available: func() (uint64, error) { return 16 << 30, nil }, Who: "a long suite", Limit: time.Minute, Poll: 10 * time.Millisecond}.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { holder.Release() })
}

// A light run takes its turn beside a heavy run that holds the line, where an
// ordinary run waits behind it and, given a wait, takes no turn.
func TestGateTurnTakesALightTurnWhileAHeavyRunHoldsTheLine(t *testing.T) {
	for _, row := range []struct {
		name      string
		args      []string
		exit      int
		isStarted bool
	}{
		{"light", []string{"--light", "--memory", "512MiB", "--execution-limit", "1m"}, 0, true},
		{"ordinary", []string{"--wait", "2s"}, exitNoTurn, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			// Arrange
			slots := turnLine(t)
			holdHeavyTurn(t, slots)
			dir := commandFolder(t)
			var stdout, stderr lockedBuffer

			// Act
			exit := gateTurn(turnRuntime(), &stdout, &stderr, append(row.args, turnCommand(dir, "command", 0, false)...)...)

			// Assert
			if exit != row.exit || left(dir, "command", "ended") != row.isStarted {
				t.Fatalf("exit=%d ended=%v stderr=%q; want exit %d and started %v", exit, left(dir, "command", "ended"), stderr.String(), row.exit, row.isStarted)
			}
		})
	}
}

// A light run that outlives its bound is ended with everything it started,
// says so on its last line and exits 124, and leaves no turn behind.
func TestGateTurnEndsALightRunAtItsBoundAndSaysSo(t *testing.T) {
	// Arrange
	slots := turnLine(t)
	holdHeavyTurn(t, slots)
	programs := t.TempDir()
	standin.RemoveAtCleanup(t, programs)
	program := filepath.Join(programs, "stalled.exe")
	standin.Put(t, program)
	for _, variable := range standin.Env("", standin.Rule{Any: true, Hold: true}) {
		name, value, _ := strings.Cut(variable, "=")
		t.Setenv(name, value)
	}
	var stdout, stderr lockedBuffer

	// Act
	exit := gateTurn(turnRuntime(), &stdout, &stderr, "--light", "--memory", "256MiB", "--execution-limit", "2s", "--", program)

	// Assert
	if exit != exitExecutionLimit || !strings.Contains(stderr.String(), "goblins gate turn: the light run outlived its bound of 2s, so it was ended with everything it started") {
		t.Fatalf("exit=%d stderr=%q; want 124 and the bound named", exit, stderr.String())
	}
	records, err := filepath.Glob(filepath.Join(filepath.Dir(slots), "executions", "*.json"))
	if err != nil || len(records) != 1 {
		t.Fatalf("records = %v, %v; want one execution receipt", records, err)
	}
	record, err := verify.LoadExecution(records[0])
	if err != nil || record.Status != "failed" || !strings.Contains(record.Failure, verify.ErrLimit.Error()) || !record.Measurement.CleanupConfirmed || record.Measurement.Usage.ActiveProcesses != 0 {
		t.Fatalf("receipt = %+v, %v; want a run ended at its bound with its cleanup confirmed", record, err)
	}
	holding, _, err := verify.Line(slots)
	if err != nil || slices.ContainsFunc(holding, func(run verify.Standing) bool { return run.IsLight }) {
		t.Fatalf("line = %+v, %v; want the light turn given back", holding, err)
	}
}

// A light run names the memory it is expected to use and its bound, or takes
// no turn, and a detached one stays light.
func TestGateTurnLightNeedsItsMemoryAndBoundAndIsCarriedDetached(t *testing.T) {
	for _, row := range []struct {
		name      string
		args      []string
		isRefused bool
	}{
		{"both", []string{"--light", "--memory", "1GiB", "--execution-limit", "5m"}, false},
		{"no memory", []string{"--light", "--execution-limit", "5m"}, true},
		{"no bound", []string{"--light", "--memory", "1GiB"}, true},
		{"neither", []string{"--light"}, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			// Act
			request, err := parseGateTurn(append(row.args, "--", "program"))

			// Assert
			if row.isRefused {
				if err == nil || !strings.Contains(err.Error(), "--light requires --memory and --execution-limit") {
					t.Fatalf("parseGateTurn = %+v, %v; want a refusal naming both", request, err)
				}
				return
			}
			detached, err := parseGateTurn(detachedArguments(request))
			if err != nil || !request.isLight || !detached.isLight || detached.memory != 1<<30 || detached.executionLimit != 5*time.Minute {
				t.Fatalf("request = %+v, detached = %+v, %v; want a light run carried detached", request, detached, err)
			}
		})
	}
}

// A gate's Test step of a project whose record declares its Test light takes
// its turn beside a heavy run that holds the line, and its command is told
// the project's test evidence. A project without that record waits as before.
func TestGateTestStepOfAProjectWhoseRecordDeclaresALightTestRunsBesideTheLine(t *testing.T) {
	for _, row := range []struct {
		name      string
		hasRecord bool
		record    string
		exit      int
		evidence  string
	}{
		{"hosted CI and light", true, `"light_test":{"memory_mib":512,"limit_minutes":5},"test_evidence":"hosted-ci"`, 0, "hosted-ci"},
		{"record without either", true, "", exitNoTurn, ""},
		{"no record", false, "", exitNoTurn, ""},
	} {
		t.Run(row.name, func(t *testing.T) {
			// Arrange
			project, gateRoot, worktree := gateTaskFixture(t, "cg-example")
			comparison := writeGateSource(t, gateRoot, project, worktree, "feature")
			t.Setenv("NM_HOME", gateRoot)
			if row.hasRecord {
				manifest := filepath.Join(os.Getenv("GOBLINS_HOME"), "data", "projects", filepath.Base(project), "project.json")
				if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
					t.Fatal(err)
				}
				fields := `"project":"` + filepath.Base(project) + `"`
				if row.record != "" {
					fields += "," + row.record
				}
				if err := os.WriteFile(manifest, []byte("{"+fields+"}"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			slots := turnLine(t)
			holdHeavyTurn(t, slots)
			var stdout, stderr lockedBuffer

			// Act
			exit := gateTurn(turnRuntime(), &stdout, &stderr, "--gate-run", "run", "--test-comparison-sha", comparison, "--wait", "3s", "--", "cmd.exe", "/d", "/c", "echo %GOBLINS_TEST_EVIDENCE%")

			// Assert
			if exit != row.exit || row.evidence != "" && strings.TrimSpace(stdout.String()) != row.evidence {
				t.Fatalf("exit=%d stdout=%q stderr=%q; want exit %d and evidence %q", exit, stdout.String(), stderr.String(), row.exit, row.evidence)
			}
		})
	}
}

// goblins gate turns shows a light run apart from the run that holds the slot,
// in its text and in its JSON, whose light list is there even when empty.
func TestGateTurnsShowsALightRunApartFromTheSlots(t *testing.T) {
	// Arrange
	slots := turnLine(t)
	var empty, emptyErrors bytes.Buffer
	if exit := runGateTurnsJSON(&empty, &emptyErrors, plenty); exit != 0 || !strings.Contains(empty.String(), `"light":[]`) {
		t.Fatalf("an empty line's JSON = %d, %q; want an empty light list", exit, empty.String())
	}
	holdHeavyTurn(t, slots)
	light, err := verify.Admission{Dir: slots, Available: func() (uint64, error) { return 16 << 30, nil }, Who: "a light test", IsLight: true, Estimate: 1 << 30, Budget: 5 * time.Minute, Limit: time.Minute, Poll: 10 * time.Millisecond}.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer light.Release()
	var text, textErrors, data, dataErrors bytes.Buffer

	// Act
	textExit := runGateTurns(&text, &textErrors, plenty)
	dataExit := runGateTurnsJSON(&data, &dataErrors, plenty)

	// Assert
	if textExit != 0 || !strings.Contains(text.String(), "turn: a long suite (pid ") || !strings.Contains(text.String(), "light, beside the slots: a light test (pid ") || !strings.Contains(text.String(), "of its 5m0s bound, using 0.0 GB of the 1.0 GB it is expected to use") {
		t.Errorf("goblins gate turns = %d, %q; want the slot's holder and the light run apart", textExit, text.String())
	}
	var report struct {
		Holding []turnsRun `json:"holding"`
		Light   []turnsRun `json:"light"`
	}
	if err := json.Unmarshal(data.Bytes(), &report); dataExit != 0 || err != nil || len(report.Holding) != 1 || report.Holding[0].Who != "a long suite" || len(report.Light) != 1 || report.Light[0].Who != "a light test" || report.Light[0].EstimateBytes != 1<<30 {
		t.Errorf("goblins gate turns --json = %d, %q (%v); want the slot's holder under holding and the light run under light", dataExit, data.String(), err)
	}
}

// With no --level, goblins gate test runs the changed level when its environment
// says the project's hosted CI is its test evidence, naming the tests it runs
// and what it leaves to CI, and the fast level otherwise.
func TestGateTestRunsTheChangedLevelForAProjectWhoseHostedCIIsItsTestEvidence(t *testing.T) {
	for _, row := range []struct {
		name    string
		value   string
		want    []string
		notWant string
	}{
		{"hosted CI", "hosted-ci", []string{
			"goblins gate test: the project's hosted CI is its test evidence",
			"goblins gate test: level changed, the default, since CI runs every package; the change requires affected: the default for a change",
			"- tests of example.com/m/b (added or edited: TestB2)",
			"left to CI:",
			"- the other tests of example.com/m/b (CI runs them before the merge)",
			"would run: go vet example.com/m/b",
			"would run: go test -json -count=1 -p 2 -timeout 45m -run ^(TestB2)$ example.com/m/b",
		}, "would run: go test -json -count=1 -p 2 -timeout 45m example.com/m/b"},
		{"no evidence", "", []string{
			"goblins gate test: level fast, the default, since CI runs every package; the change requires affected: the default for a change",
			"would run: go test -json -count=1 -p 2 -timeout 45m example.com/m/b",
		}, "level changed"},
	} {
		t.Run(row.name, func(t *testing.T) {
			// Arrange
			dir := testStepModule(t, nil, map[string]string{"b/b_test.go": "package b\n\nimport (\n\t\"os\"\n\t\"testing\"\n\n\t\"example.com/m/a\"\n)\n\nfunc TestB(t *testing.T) {\n\tif os.Getenv(\"GOBLINS_HOME\") != \"\" || a.A() == 0 {\n\t\tt.Fatal(\"the fleet's GOBLINS_HOME reached the test\")\n\t}\n}\n\nfunc TestB2(t *testing.T) {}\n"})
			t.Chdir(dir)
			t.Setenv(testEvidenceVariable, row.value)
			var stdout, stderr bytes.Buffer

			// Act
			exit := gateTest(&stdout, &stderr, "--plan")

			// Assert
			if exit != 0 {
				t.Fatalf("exit = %d, want 0; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
			}
			for _, want := range row.want {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("stdout %q lacks %q", stdout.String(), want)
				}
			}
			if strings.Contains(stdout.String(), row.notWant) {
				t.Errorf("stdout %q has %q", stdout.String(), row.notWant)
			}
		})
	}
}

// writeProjectRecord puts fields in the record of the one project the
// gate's fixture keeps, after the project name.
func writeProjectRecord(t *testing.T, fields string) {
	t.Helper()
	records, err := filepath.Glob(filepath.Join(os.Getenv("GOBLINS_HOME"), "data", "projects", "*"))
	if err != nil || len(records) != 1 {
		t.Fatalf("project records = %v, %v; want the fixture's one project", records, err)
	}
	record := `{"project":"` + filepath.Base(records[0]) + `",` + fields + `}`
	if err := os.WriteFile(filepath.Join(records[0], "project.json"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A gate's Test step of a project whose record declares its Test light takes
// a light turn expected to use the record's memory, bounded by the shorter of
// the record's bound and the one it was given, and is ended at that bound.
func TestGateTestStepOfALightProjectRunsWithinTheRecordsMemoryAndTheShorterBound(t *testing.T) {
	// Arrange
	project, gateRoot, worktree := gateTaskFixture(t, "cg-example")
	comparison := writeGateSource(t, gateRoot, project, worktree, "feature")
	t.Setenv("NM_HOME", gateRoot)
	if err := os.MkdirAll(filepath.Join(os.Getenv("GOBLINS_HOME"), "data", "projects", filepath.Base(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	writeProjectRecord(t, `"light_test":{"memory_mib":512,"limit_minutes":5},"test_evidence":"hosted-ci"`)
	slots := turnLine(t)
	holdHeavyTurn(t, slots)
	programs := t.TempDir()
	standin.RemoveAtCleanup(t, programs)
	program := filepath.Join(programs, "stalled.exe")
	standin.Put(t, program)
	for _, variable := range standin.Env("", standin.Rule{Any: true, Hold: true}) {
		name, value, _ := strings.Cut(variable, "=")
		t.Setenv(name, value)
	}
	var stdout, stderr lockedBuffer
	exited := make(chan int, 1)

	// Act
	go func() {
		exited <- gateTurn(turnRuntime(), &stdout, &stderr, "--gate-run", "run", "--test-comparison-sha", comparison, "--execution-limit", "2s", "--", program)
	}()
	var light verify.Standing
	eventually(t, "the Test step's light turn", func() bool {
		holding, _, err := verify.Line(slots)
		index := slices.IndexFunc(holding, func(run verify.Standing) bool { return run.IsLight })
		if err != nil || index < 0 {
			return false
		}
		light = holding[index]
		return true
	})
	exit := <-exited

	// Assert
	if light.Estimate != 512<<20 || light.Budget != 2*time.Second {
		t.Errorf("light turn = %+v; want the record's 512 MiB and the given 2s, shorter than the record's 5m", light)
	}
	if exit != exitExecutionLimit || !strings.Contains(stderr.String(), "goblins gate turn: the light run outlived its bound of 2s, so it was ended with everything it started") {
		t.Fatalf("exit=%d stderr=%q; want 124 and the 2s bound named", exit, stderr.String())
	}
}

// A project whose record also requires a test environment still gives its
// gate's Test step the record's test evidence beside the environment.
func TestGateTestStepOfALightProjectWithATestEnvironmentIsToldItsTestEvidence(t *testing.T) {
	// Arrange
	_, comparison := gateEnvironmentFixture(t)
	writeProjectRecord(t, `"test_environment":{"python_version":"3.12.10"},"light_test":{"memory_mib":512,"limit_minutes":5},"test_evidence":"hosted-ci"`)
	turnLine(t)
	var stdout, stderr lockedBuffer

	// Act
	exit := gateTurn(turnRuntime(), &stdout, &stderr, "--gate-run", "run", "--test-comparison-sha", comparison, "--", "cmd.exe", "/d", "/c", "echo %GOBLINS_TEST_EVIDENCE%^|%UV_NO_SYNC%")

	// Assert
	if exit != 0 || strings.TrimSpace(stdout.String()) != "hosted-ci|1" {
		t.Fatalf("exit=%d stdout=%q stderr=%q; want the record's evidence beside the bound environment", exit, stdout.String(), stderr.String())
	}
}
