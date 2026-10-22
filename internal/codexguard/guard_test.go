package codexguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// An armed guard holds a codex for cmd and one for Git Bash, each running the
// home's cfo.exe as cfo codex-guard with the home, the task and when Codex
// renews, and passing codex's own arguments after --. A path cmd would
// expand or a shell would split is quoted for each.
func TestArmWritesACodexForCmdAndOneForGitBash(t *testing.T) {
	// Arrange
	stateDir := filepath.Join(t.TempDir(), "100% state")
	program := `C:\Users\o'neil\CodeGoblins\bin\cfo.exe`
	until := time.Date(2026, 10, 22, 12, 0, 13, 0, time.UTC)

	// Act
	err := Arm(stateDir, program, "g1", until)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	escapedState := strings.ReplaceAll(stateDir, "%", "%%")
	for name, want := range map[string]string{
		"codex.cmd": `@"C:\Users\o'neil\CodeGoblins\bin\cfo.exe" "codex-guard" "--state" "` + escapedState + `" "--task" "g1" "--until" "2026-10-22T12:00:13Z" -- %*` + "\r\n",
		"codex":     "#!/bin/sh\nexec 'C:/Users/o'\\''neil/CodeGoblins/bin/cfo.exe' 'codex-guard' '--state' '" + stateDir + "' '--task' 'g1' '--until' '2026-10-22T12:00:13Z' -- \"$@\"\n",
	} {
		got, err := os.ReadFile(filepath.Join(Dir(stateDir, "g1"), name))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", name, got, err, want)
		}
	}
}

// A guard armed again for the same renewal is left as it is, since the
// supervisor arms every guard at every fleet reading, and one armed for a
// later renewal says the later one.
func TestArmLeavesAGuardArmedSoAsItIs(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	until := time.Date(2026, 10, 22, 12, 0, 13, 0, time.UTC)
	if err := Arm(stateDir, `C:\home\bin\cfo.exe`, "g1", until); err != nil {
		t.Fatal(err)
	}
	staged := fsx.Stages()

	// Act
	again := Arm(stateDir, `C:\home\bin\cfo.exe`, "g1", until)
	stagedAgain := fsx.Stages() - staged
	later := Arm(stateDir, `C:\home\bin\cfo.exe`, "g1", until.Add(time.Hour))

	// Assert
	if again != nil || later != nil {
		t.Fatalf("Arm again = %v, later = %v", again, later)
	}
	if stagedAgain != 0 {
		t.Errorf("arming an armed guard again wrote %d files, want none", stagedAgain)
	}
	if got, _ := os.ReadFile(filepath.Join(Dir(stateDir, "g1"), "codex.cmd")); !strings.Contains(string(got), "2026-10-22T13:00:13Z") {
		t.Errorf("the guard armed for a later renewal runs %q", got)
	}
}

// A task id that is no task's arms and disarms nothing, so no folder outside
// the home's guard folders is written or removed.
func TestArmAndDisarmRefuseAnIDThatIsNoTasks(t *testing.T) {
	stateDir := t.TempDir()
	for _, id := range []string{"", "..", `..\state`, "a/b"} {
		if err := Arm(stateDir, `C:\home\bin\cfo.exe`, id, time.Now()); err == nil {
			t.Errorf("Arm(%q) armed a guard", id)
		}
		if err := Disarm(stateDir, id); err == nil {
			t.Errorf("Disarm(%q) disarmed a guard", id)
		}
	}
}

// Disarming empties a task's guard, a guard that is not armed included, and
// pruning takes away the guard of every task no longer live.
func TestDisarmAndPruneTakeGuardsAway(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	until := time.Now().Add(time.Hour)
	for _, id := range []string{"g1", "g2", "gone-1"} {
		if err := Arm(stateDir, `C:\home\bin\cfo.exe`, id, until); err != nil {
			t.Fatal(err)
		}
	}

	// Act
	disarmed := Disarm(stateDir, "g1")
	disarmedTwice := Disarm(stateDir, "g1")
	pruned := Prune(stateDir, []string{"g1", "G2"})

	// Assert
	if disarmed != nil || disarmedTwice != nil || pruned != nil {
		t.Fatalf("Disarm = %v, again %v, Prune = %v", disarmed, disarmedTwice, pruned)
	}
	for id, isArmed := range map[string]bool{"g1": false, "g2": true, "gone-1": false} {
		if _, err := os.Stat(filepath.Join(Dir(stateDir, id), "codex.cmd")); (err == nil) != isArmed {
			t.Errorf("%s's guard armed = %v, want %v", id, err == nil, isArmed)
		}
	}
	if err := Prune(t.TempDir(), nil); err != nil {
		t.Errorf("Prune of a home with no guards = %v", err)
	}
}

// A refused codex says why in one line and wakes the CFO the first time the
// task meets its guard before Codex renews, naming the goblin and the
// command; the second refusal wakes nobody. A guard met after Codex renewed,
// which the supervisor has not yet disarmed, says so and wakes nobody.
func TestRefuseWakesTheCFOOncePerTaskUntilCodexRenews(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	now := time.Date(2026, 10, 17, 3, 0, 0, 0, time.UTC)
	until := time.Date(2026, 10, 22, 12, 0, 13, 0, time.UTC)

	// Act
	first, firstErr := Refuse(stateDir, "g1", until, []string{"exec", "-c", "model_provider=local", "probe the harness"}, now)
	second, secondErr := Refuse(stateDir, "g1", until, []string{"--version"}, now.Add(time.Minute))
	renewed, renewedErr := Refuse(stateDir, "g1", until, []string{"exec"}, until)

	// Assert
	if firstErr != nil || secondErr != nil || renewedErr != nil {
		t.Fatalf("Refuse = %v, %v, %v", firstErr, secondErr, renewedErr)
	}
	for _, line := range []string{first, second} {
		if !strings.HasPrefix(line, "codex: refused") || !strings.Contains(line, "used up until 2026-10-22T12:00:13Z") || !strings.Contains(line, "task g1") || strings.Contains(line, "\n") {
			t.Errorf("refusal = %q, want one line naming the renewal and the task", line)
		}
	}
	if !strings.Contains(renewed, "renewed at 2026-10-22T12:00:13Z") {
		t.Errorf("refusal after the renewal = %q, want it to say Codex renewed", renewed)
	}
	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Kind != "allowance" || records[0].Key != "g1" || !strings.Contains(records[0].Detail, "goblin g1 started `codex exec -c model_provider=local \"probe the harness\"`") || !strings.Contains(records[0].Detail, "cfo auth grant g1 --codex-credits") {
		t.Errorf("wakes = %+v, want one naming goblin g1, its command and the grant", records)
	}
}

// A wake names a long command cut short, and an empty argument quoted.
func TestShownCommandCutsALongCommand(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "no arguments", want: "codex"},
		{name: "an empty argument", args: []string{"exec", ""}, want: `codex exec ""`},
		{name: "a quote", args: []string{`say "hi"`}, want: `codex "say \"hi\""`},
		{name: "a long prompt", args: []string{"exec", strings.Repeat("a", 400)}, want: "codex exec " + strings.Repeat("a", commandShown-len("codex exec ")) + "..."},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := shownCommand(test.args); got != test.want {
				t.Errorf("shownCommand(%q) = %q, want %q", test.args, got, test.want)
			}
		})
	}
}
