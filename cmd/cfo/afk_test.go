package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/quota"
)

const overlordsShell = "his own terminal (powershell.exe pid 4242)"

// afkRuntime is a home whose supervisor accepts every switch as the
// Overlord's and every decision as the CFO's: who may do either is the
// supervisor's own proof, tested there.
func afkRuntime(t *testing.T) (commandRuntime, home.Home) {
	t.Helper()
	dir := t.TempDir()
	h := home.Home{Root: dir, State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data")}
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	return commandRuntime{
		resolveHome: func() (home.Home, error) { return h, nil },
		switchAFK: func(h home.Home, on bool) error {
			if on {
				_, _, err := afk.TurnOn(h.State, overlordsShell, nil, time.Now())
				return err
			}
			switched, err := afk.TurnOff(h.State, overlordsShell, time.Now())
			if err != nil {
				return err
			}
			entries, _, err := afk.Entries(h.State, switched.Session)
			if err != nil {
				return err
			}
			return afk.SaveReport(h.State, afk.Report{Session: switched.Session, Since: switched.Since, Ended: switched.Ended, From: switched.From, EndedFrom: switched.EndedFrom, Decisions: afk.Decisions(entries)})
		},
		logAFK: func(h home.Home, entry afk.Entry) error {
			_, err := afk.Log(h.State, entry, time.Now())
			return err
		},
	}, h
}

func afkCommand(runtime commandRuntime, args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime(append([]string{"afk"}, args...), &stdout, &stderr, runtime)
	return exit, stdout.String(), stderr.String()
}

func TestAFKStatusSaysItIsOffWhereItWasNeverTurnedOn(t *testing.T) {
	// Arrange
	runtime, _ := afkRuntime(t)

	// Act
	exit, stdout, stderr := afkCommand(runtime, "status")

	// Assert
	if exit != 0 || !strings.Contains(stdout, "AFK mode is off") || stderr != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q, want it reported off", exit, stdout, stderr)
	}
}

func TestAFKOnSaysItIsOnSinceWhenAndFromWhere(t *testing.T) {
	// Arrange
	runtime, h := afkRuntime(t)

	// Act
	exit, stdout, stderr := afkCommand(runtime, "on")

	// Assert
	switched, err := afk.Read(h.State)
	if exit != 0 || err != nil || !switched.On {
		t.Fatalf("exit=%d stderr=%q switch=%+v (%v), want AFK mode on", exit, stderr, switched, err)
	}
	for _, phrase := range []string{"AFK mode is on since " + switched.Since.Format("2006-01-02 15:04 UTC"), overlordsShell, "cfo afk off"} {
		if !strings.Contains(stdout, phrase) {
			t.Errorf("stdout = %q, want it to say %q", stdout, phrase)
		}
	}
}

// The supervisor's refusal of a goblin or the CFO is what the command says,
// and nothing reads as turned on.
func TestAFKOnReportsTheSupervisorsRefusalAndTurnsNothingOn(t *testing.T) {
	// Arrange
	runtime, h := afkRuntime(t)
	refusal := "AFK mode is the Supreme Overlord's switch, and this command runs in a goblin's terminal: only he turns it on or off, from a terminal of his own"
	runtime.switchAFK = func(home.Home, bool) error { return errors.New(refusal) }

	// Act
	exit, stdout, stderr := afkCommand(runtime, "on")

	// Assert
	if exit != 1 || !strings.Contains(stderr, refusal) || stdout != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q, want the refusal and nothing else", exit, stdout, stderr)
	}
	if switched, _ := afk.Read(h.State); switched.On {
		t.Error("AFK mode reads as on after a refused switch")
	}
}

// Turning it off prints the report of the stretch where the Overlord turned
// it off.
func TestAFKOffPrintsTheReportOfTheStretch(t *testing.T) {
	// Arrange
	runtime, _ := afkRuntime(t)
	if exit, _, stderr := afkCommand(runtime, "on"); exit != 0 {
		t.Fatal(stderr)
	}
	if exit, _, stderr := afkCommand(runtime, "log", "--kind", "deploy", "--what", "acme production", "--evidence", "/health reads 200 with commit abc1234", "--link", "https://acme.example/health"); exit != 0 {
		t.Fatal(stderr)
	}

	// Act
	exit, stdout, stderr := afkCommand(runtime, "off")

	// Assert
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	for _, phrase := range []string{"AFK MODE REPORT", "Deployed (1)", "acme production (https://acme.example/health)", "/health reads 200 with commit abc1234", "Held for you (0)"} {
		if !strings.Contains(stdout, phrase) {
			t.Errorf("stdout does not say %q:\n%s", phrase, stdout)
		}
	}
	if exit, again, _ := afkCommand(runtime, "report"); exit != 0 || again != stdout {
		t.Errorf("cfo afk report = %d %q, want the same report again", exit, again)
	}
}

func TestAFKOffWhileItIsOffSaysSo(t *testing.T) {
	// Arrange
	runtime, _ := afkRuntime(t)

	// Act
	exit, stdout, stderr := afkCommand(runtime, "off")

	// Assert
	if exit != 1 || !strings.Contains(stderr, "AFK mode is not on") || stdout != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q, want it refused as not on", exit, stdout, stderr)
	}
}

func TestAFKLogSendsTheDecisionWithItsEvidence(t *testing.T) {
	// Arrange
	runtime, h := afkRuntime(t)
	if exit, _, stderr := afkCommand(runtime, "on"); exit != 0 {
		t.Fatal(stderr)
	}

	// Act
	exit, stdout, stderr := afkCommand(runtime, "log", "--kind", "migration", "--what", "acme 0042_add_invoice_index", "--evidence", "applied to production and dev; both applied lists read back with 0042 last")

	// Assert
	entries, _, err := afk.Entries(h.State, "")
	if exit != 0 || err != nil || len(entries) != 2 {
		t.Fatalf("exit=%d stderr=%q log=%+v (%v), want the migration logged after the switch", exit, stderr, entries, err)
	}
	if logged := entries[1]; logged.Kind != afk.KindMigration || logged.What != "acme 0042_add_invoice_index" || logged.Evidence != "applied to production and dev; both applied lists read back with 0042 last" {
		t.Errorf("logged = %+v", logged)
	}
	if !strings.Contains(stdout, "logged migration: acme 0042_add_invoice_index") {
		t.Errorf("stdout = %q, want it to say what was logged", stdout)
	}
}

// A decision that names no kind, subject or evidence never reaches the
// supervisor.
func TestAFKLogRefusesADecisionThatSaysTooLittle(t *testing.T) {
	for name, args := range map[string][]string{
		"no kind":         {"--what", "acme production", "--evidence", "/health reads 200"},
		"an unknown kind": {"--kind", "drop", "--what", "legacy_invoices", "--evidence", "it was in the way"},
		"no subject":      {"--kind", "deploy", "--evidence", "/health reads 200"},
		"no evidence":     {"--kind", "deploy", "--what", "acme production"},
		"stray words":     {"--kind", "deploy", "--what", "acme production", "--evidence", "/health reads 200", "and more"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			runtime, _ := afkRuntime(t)
			sent := 0
			runtime.logAFK = func(home.Home, afk.Entry) error { sent++; return nil }

			// Act
			exit, _, stderr := afkCommand(runtime, append([]string{"log"}, args...)...)

			// Assert
			if exit != 2 || stderr == "" || sent != 0 {
				t.Fatalf("exit=%d stderr=%q sent=%d, want it refused before the supervisor", exit, stderr, sent)
			}
		})
	}
}

// Status, while AFK mode is on, says who turned it on and its terms, what the
// CFO decided so far, and what is held for the Overlord: a question the CFO
// answered is a decision, not something held.
func TestAFKStatusListsWhatWasDecidedAndWhatIsHeld(t *testing.T) {
	// Arrange
	runtime, h := afkRuntime(t)
	if exit, _, stderr := afkCommand(runtime, "on"); exit != 0 {
		t.Fatal(stderr)
	}
	pr := "https://github.com/acme/api/pull/12"
	for _, entry := range []afk.Entry{
		{Kind: afk.KindMerge, What: pr, Evidence: "gate run 41 passed"},
		{Kind: afk.KindMerge, What: pr, Evidence: "gh pr merge", Outcome: afk.OutcomeMerged},
		{Kind: afk.KindAnswer, What: "notify-pd-billing-12", Task: "pd-billing", Evidence: "asked: Which store? answered: SQLite"},
	} {
		if _, err := afk.Log(h.State, entry, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	for _, held := range []afk.Entry{
		{Item: "question:drop-legacy-invoices", What: "Migration 0042 drops legacy_invoices. Apply it?"},
		{Item: "question:notify-pd-billing-12", Task: "pd-billing", What: "Which store?"},
		{Item: "review:waiting-pd-auth-7", Task: "pd-auth", What: "Waiting on you: sign in to Vercel"},
	} {
		if err := afk.Hold(h.State, held, time.Now()); err != nil {
			t.Fatal(err)
		}
	}

	// Act
	exit, stdout, stderr := afkCommand(runtime, "status")

	// Assert
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	for _, phrase := range []string{
		"AFK MODE IS ON", overlordsShell, "never decided for him",
		"Decided so far (2)", "merge: " + pr + " (merged)", "answer: pd-billing: notify-pd-billing-12",
		"Held for you so far (2)", "question:drop-legacy-invoices, the CFO's: Migration 0042 drops legacy_invoices. Apply it?", "review:waiting-pd-auth-7, pd-auth's: Waiting on you: sign in to Vercel",
	} {
		if !strings.Contains(stdout, phrase) {
			t.Errorf("status does not say %q:\n%s", phrase, stdout)
		}
	}
	if strings.Contains(stdout, "question:notify-pd-billing-12") {
		t.Errorf("status lists a question the CFO answered as held:\n%s", stdout)
	}
}

func TestAFKReportSaysWhenNoStretchHasEnded(t *testing.T) {
	// Arrange
	runtime, _ := afkRuntime(t)

	// Act
	exit, stdout, stderr := afkCommand(runtime, "report")

	// Assert
	if exit != 0 || !strings.Contains(stdout, "No stretch of AFK mode has ended") || stderr != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q, want it to say there is no report yet", exit, stdout, stderr)
	}
}

func TestAFKNamesWhatItTakes(t *testing.T) {
	// Arrange
	runtime, _ := afkRuntime(t)

	for _, args := range [][]string{{}, {"sideways"}, {"on", "now"}, {"status", "--json"}} {
		// Act
		exit, _, stderr := afkCommand(runtime, args...)

		// Assert
		if exit != 2 || !strings.Contains(stderr, "cfo afk on | off | status | report | log") {
			t.Errorf("cfo afk %q: exit=%d stderr=%q, want its usage", args, exit, stderr)
		}
	}
}

// cfo pr merge reads the switch from the home: off or with no home it merges
// on the Overlord's word as before, on it is the CFO's own merge word, and a
// switch that cannot be read refuses the merge rather than guess.
func TestThePRMergeCommandReadsAFKModeFromTheHome(t *testing.T) {
	t.Run("no home", func(t *testing.T) {
		away, err := afkAuthorityOf(commandRuntime{resolveHome: func() (home.Home, error) { return home.Home{}, errors.New("no home here") }})
		if away != nil || err != nil {
			t.Fatalf("afkAuthorityOf = %v, %v, want AFK mode off", away, err)
		}
	})
	t.Run("off", func(t *testing.T) {
		runtime, _ := afkRuntime(t)
		away, err := afkAuthorityOf(runtime)
		if away != nil || err != nil {
			t.Fatalf("afkAuthorityOf = %v, %v, want AFK mode off", away, err)
		}
	})
	t.Run("on", func(t *testing.T) {
		runtime, h := afkRuntime(t)
		if exit, _, stderr := afkCommand(runtime, "on"); exit != 0 {
			t.Fatal(stderr)
		}
		away, err := afkAuthorityOf(runtime)
		if away == nil || err != nil {
			t.Fatalf("afkAuthorityOf = %v, %v, want AFK mode on", away, err)
		}
		if err := away.log(afk.Entry{Kind: afk.KindMerge, What: "https://github.com/acme/api/pull/12", Evidence: "gate run 41 passed"}); err != nil {
			t.Fatal(err)
		}
		if entries, _, _ := afk.Entries(h.State, ""); len(entries) != 2 || entries[1].Kind != afk.KindMerge {
			t.Errorf("log = %+v, want the merge word sent to this home's log", entries)
		}
	})
	t.Run("unreadable", func(t *testing.T) {
		runtime, h := afkRuntime(t)
		if err := os.WriteFile(filepath.Join(h.State, "afk.json"), []byte(`{"on": tr`), 0o600); err != nil {
			t.Fatal(err)
		}
		runner := &prMergeRunner{}
		var stdout, stderr bytes.Buffer

		exit := runPR("merge", []string{"https://github.com/o/r/pull/13"}, &stdout, &stderr, runner, runtime)

		if exit != 1 || !strings.Contains(stderr.String(), "AFK mode's switch") || len(runner.requests) != 0 {
			t.Fatalf("exit=%d stderr=%q requests=%d, want the merge refused before GitHub is asked anything", exit, stderr.String(), len(runner.requests))
		}
	})
}

// The report says what was spent from quota-axi's reading of every provider
// it measured: each window's use and any credit balance. A provider whose
// numbers quota-axi itself calls stale is no reading.
func TestAFKAllowanceKeepsEachMeasuredWindowAndCreditBalance(t *testing.T) {
	// Arrange
	reset := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	report := quota.Report{Providers: map[string]quota.Provider{
		"claude": {Name: "claude", Windows: []quota.Window{{ID: "five_hour", Label: "session", PercentUsed: 88, ResetsAt: reset}, {ID: "seven_day", Label: "week", PercentUsed: 1}}},
		"codex":  {Name: "codex", Windows: []quota.Window{{ID: "weekly", PercentUsed: 40}}, Credits: &quota.Credits{Remaining: 12, Unit: "credits"}},
		"kimi":   {Name: "kimi", Stale: true, Windows: []quota.Window{{ID: "weekly", Label: "week", PercentUsed: 5}}},
	}}

	// Act
	got := afkAllowance(report)

	// Assert
	want := []afk.Allowance{
		{Provider: "claude", Window: "session", PercentUsed: 88, ResetsAt: reset},
		{Provider: "claude", Window: "week", PercentUsed: 1},
		{Provider: "codex", Window: "weekly", PercentUsed: 40},
		{Provider: "codex", Window: "credits", Credits: true, Remaining: 12, Unit: "credits"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("afkAllowance = %+v, want %+v", got, want)
	}
}
