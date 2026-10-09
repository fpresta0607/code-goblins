package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fleetconfig"
	"github.com/fpresta0607/code-goblins/internal/harness"
)

// asTheCFO clears what marks this process as a goblin's or a gate agent's, as
// the CFO's terminal has neither.
func asTheCFO(t *testing.T) {
	t.Helper()
	t.Setenv(harness.RoleVariable, "")
	t.Setenv(gateAgentVariable, "")
}

func TestAllowanceFloorSaysEachProvidersFloorAndWhereItComesFrom(t *testing.T) {
	// Arrange
	h := testHome(t)
	path := filepath.Join(h.Root, "config", "fleet.json")
	writeTestFile(t, path, `{"weekly_floor_percent":{"claude":0}}`)
	var stdout, stderr bytes.Buffer

	// Act
	exit := runWithRuntime([]string{"allowance-floor"}, &stdout, &stderr, testCommandRuntimeForHome(h))

	// Assert
	want := "claude: 0 percent, set in " + path + ": goblins run on its week until claude refuses\n" +
		"codex: 5 percent, the default: goblins pause with 5 percent of its week left\n"
	if exit != 0 || stdout.String() != want {
		t.Fatalf("exit %d, stdout %q, stderr %q; want %q", exit, stdout.String(), stderr.String(), want)
	}
}

func TestAllowanceFloorSetsOneProvidersFloorAndKeepsEveryOtherSetting(t *testing.T) {
	// Arrange
	asTheCFO(t)
	h := testHome(t)
	writeTestFile(t, filepath.Join(h.Root, "config", "fleet.json"), `{"disk_floor_gb":20,"weekly_floor_percent":{"codex":3}}`)
	var stdout, stderr bytes.Buffer

	// Act
	exit := runWithRuntime([]string{"allowance-floor", "claude", "0"}, &stdout, &stderr, testCommandRuntimeForHome(h))

	// Assert
	settings, err := fleetconfig.Read(h.Root)
	if exit != 0 || err != nil || settings.WeeklyFloor("claude") != 0 || settings.WeeklyFloor("codex") != 3 || settings.DiskFloorGB != 20 {
		t.Fatalf("exit %d, stderr %q, settings %+v, %v; want claude at 0 and the rest kept", exit, stderr.String(), settings, err)
	}
	if !strings.HasPrefix(stdout.String(), "claude: 0 percent, set in ") || !strings.Contains(stdout.String(), "within a minute") {
		t.Fatalf("stdout %q, want the floor set and when it takes hold", stdout.String())
	}
}

func TestAllowanceFloorRefusesWhatIsNoFloorAndWritesNothing(t *testing.T) {
	for _, testCase := range []struct {
		name string
		args []string
		exit int
	}{
		{name: "a provider with no measured week", args: []string{"pi", "0"}, exit: 2},
		{name: "under 0", args: []string{"claude", "-1"}, exit: 2},
		{name: "the whole week", args: []string{"claude", "100"}, exit: 2},
		{name: "no number", args: []string{"claude", "none"}, exit: 2},
		{name: "not a number", args: []string{"claude", "NaN"}, exit: 2},
		{name: "a percent sign", args: []string{"claude", "5%"}, exit: 2},
		{name: "a provider alone", args: []string{"claude"}, exit: 2},
		{name: "too many", args: []string{"claude", "0", "codex"}, exit: 2},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			asTheCFO(t)
			h := testHome(t)
			path := filepath.Join(h.Root, "config", "fleet.json")
			const before = `{"disk_floor_gb":20}`
			writeTestFile(t, path, before)
			var stdout, stderr bytes.Buffer

			// Act
			exit := runWithRuntime(append([]string{"allowance-floor"}, testCase.args...), &stdout, &stderr, testCommandRuntimeForHome(h))

			// Assert
			data, err := os.ReadFile(path)
			if exit != testCase.exit || err != nil || string(data) != before || stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("exit %d, stdout %q, stderr %q, file %s (%v); want exit %d, a refusal and the file untouched", exit, stdout.String(), stderr.String(), data, err, testCase.exit)
			}
		})
	}
}

// The floor is the Overlord's setting, which the CFO changes on his word, so
// a goblin's and a gate agent's terminal can read it but never change it.
func TestAllowanceFloorChangeIsRefusedInAGoblinsOrAGateAgentsTerminal(t *testing.T) {
	for _, variable := range [][2]string{{harness.RoleVariable, harness.RoleGoblin}, {gateAgentVariable, "1"}} {
		t.Run(variable[0], func(t *testing.T) {
			// Arrange
			asTheCFO(t)
			t.Setenv(variable[0], variable[1])
			h := testHome(t)
			deps := testCommandRuntimeForHome(h)
			var stdout, stderr bytes.Buffer

			// Act
			exit := runWithRuntime([]string{"allowance-floor", "claude", "0"}, &stdout, &stderr, deps)
			readExit := runWithRuntime([]string{"allowance-floor"}, &bytes.Buffer{}, &bytes.Buffer{}, deps)

			// Assert
			if _, err := os.Stat(filepath.Join(h.Root, "config", "fleet.json")); exit != 2 || !strings.Contains(stderr.String(), "never a goblin or a gate agent") || !os.IsNotExist(err) {
				t.Fatalf("exit %d, stderr %q, file %v; want the change refused and no file written", exit, stderr.String(), err)
			}
			if readExit != 0 {
				t.Fatalf("reading the floor exited %d, want it allowed", readExit)
			}
		})
	}
}

func TestAllowanceFloorSaysWhyAFileItCannotReadIsNoFloor(t *testing.T) {
	// Arrange
	h := testHome(t)
	writeTestFile(t, filepath.Join(h.Root, "config", "fleet.json"), `{"weekly_floor_percent":{"claude":100}}`)
	var stdout, stderr bytes.Buffer

	// Act
	exit := runWithRuntime([]string{"allowance-floor"}, &stdout, &stderr, testCommandRuntimeForHome(h))

	// Assert
	if exit != 1 || !strings.Contains(stderr.String(), "weekly_floor_percent") || stdout.Len() != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q; want the file's fault named", exit, stdout.String(), stderr.String())
	}
}
