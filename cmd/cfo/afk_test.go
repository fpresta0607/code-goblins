package main

import (
	"bytes"
	"encoding/json"
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
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

const (
	overlordsShell = "his own terminal (powershell.exe pid 4242)"
	// theCFOAtHisAsk is who a switch the CFO made at the Overlord's ask names.
	theCFOAtHisAsk = "the CFO at his ask (claude pid 7001)"
)

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
		switchAFK: func(h home.Home, on bool, asked string) error {
			var switched afk.State
			var err error
			switch {
			case on && asked == "":
				_, _, err = afk.TurnOn(h.State, overlordsShell, nil, time.Now())
				return err
			case on:
				_, _, err = afk.TurnOnAtHisAsk(h.State, theCFOAtHisAsk, asked, nil, time.Now())
				return err
			case asked == "":
				switched, err = afk.TurnOff(h.State, overlordsShell, time.Now())
			default:
				switched, err = afk.TurnOffAtHisAsk(h.State, theCFOAtHisAsk, asked, time.Now())
			}
			if err != nil {
				return err
			}
			entries, _, err := afk.Entries(h.State, switched.Session)
			if err != nil {
				return err
			}
			return afk.SaveReport(h.State, afk.Report{Session: switched.Session, Since: switched.Since, Ended: switched.Ended, From: switched.From, Asked: switched.Asked, EndedFrom: switched.EndedFrom, EndedAsked: switched.EndedAsked, Decisions: afk.Decisions(entries)})
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
	refusal := "AFK mode is the Supreme Overlord's switch, and this command runs in a goblin's terminal: he turns it on or off from a terminal or a board of his own, and the registered CFO only at his ask, with his words"
	runtime.switchAFK = func(home.Home, bool, string) error { return errors.New(refusal) }

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

// His off over a switch that cannot be read resets it, and the command says
// so rather than print the report of an earlier stretch as if it were this
// one's.
func TestAFKOffOverASwitchThatCannotBeReadSaysItWasResetAndPrintsNoReport(t *testing.T) {
	// Arrange
	runtime, h := afkRuntime(t)
	if exit, _, stderr := afkCommand(runtime, "on"); exit != 0 {
		t.Fatal(stderr)
	}
	if exit, _, stderr := afkCommand(runtime, "off"); exit != 0 {
		t.Fatal(stderr)
	}
	if err := os.WriteFile(filepath.Join(h.State, "afk.json"), []byte(`{"on": tr`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, unread := afk.Read(h.State)
	runtime.switchAFK = func(h home.Home, on bool, _ string) error {
		if on {
			t.Error("the command asked to turn AFK mode on")
		}
		return afk.Reset(h.State, overlordsShell, unread, time.Now())
	}

	// Act
	exit, stdout, stderr := afkCommand(runtime, "off")

	// Assert
	if exit != 0 || stderr != "" {
		t.Fatalf("exit=%d stderr=%q, want the reset reported as done", exit, stderr)
	}
	for _, phrase := range []string{"could not be read", "is reset to off", "no report", "state/afk.audit"} {
		if !strings.Contains(stdout, phrase) {
			t.Errorf("stdout does not say %q:\n%s", phrase, stdout)
		}
	}
	if strings.Contains(stdout, "AFK MODE REPORT") {
		t.Errorf("stdout prints an earlier stretch's report as this one's:\n%s", stdout)
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

	for _, args := range [][]string{{}, {"sideways"}, {"status", "--json"}} {
		// Act
		exit, _, stderr := afkCommand(runtime, args...)

		// Assert
		if exit != 2 || !strings.Contains(stderr, `cfo afk on [--asked "<his words>"] | off [--asked "<his words>"] | status | report | log`) {
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

// The registered CFO makes the switch at the Overlord's ask with --asked,
// which carries his words to the supervisor on one line. The command says who
// switched it, and speaks to the CFO that ran it, not to him.
func TestAFKOnAtHisAskPassesHisWordsAndSaysTheCFOSwitchedIt(t *testing.T) {
	// Arrange
	runtime, h := afkRuntime(t)

	// Act
	exit, stdout, stderr := afkCommand(runtime, "on", "--asked", "  I'm stepping away,\n turn AFK on ")

	// Assert
	switched, err := afk.Read(h.State)
	if exit != 0 || err != nil || !switched.On || switched.From != theCFOAtHisAsk || switched.Asked != "I'm stepping away, turn AFK on" {
		t.Fatalf("exit=%d stderr=%q switch=%+v (%v), want AFK mode on by the CFO with his words", exit, stderr, switched, err)
	}
	for _, phrase := range []string{`turned on by the CFO at his ask (claude pid 7001), in his words "I'm stepping away, turn AFK on"`, "Say in your reply to him that AFK mode is on", "His own switch"} {
		if !strings.Contains(stdout, phrase) {
			t.Errorf("stdout = %q, want it to say %q", stdout, phrase)
		}
	}
	if strings.Contains(stdout, "what stays yours") {
		t.Errorf("stdout speaks to the Overlord, who did not run this: %q", stdout)
	}
}

// Off at his ask ends the stretch and prints its report, which says who made
// each switch and quotes him both times.
func TestAFKOffAtHisAskPrintsTheReportThatQuotesHim(t *testing.T) {
	// Arrange
	runtime, _ := afkRuntime(t)
	if exit, _, stderr := afkCommand(runtime, "on", "--asked", "I'm stepping away, turn AFK on"); exit != 0 {
		t.Fatal(stderr)
	}

	// Act
	exit, stdout, stderr := afkCommand(runtime, "off", "--asked", "I'm back, turn AFK off")

	// Assert
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	want := `turned on by the CFO at his ask (claude pid 7001), in his words "I'm stepping away, turn AFK on", off by the CFO at his ask (claude pid 7001), in his words "I'm back, turn AFK off".`
	if !strings.Contains(stdout, "AFK MODE REPORT") || !strings.Contains(stdout, want) {
		t.Errorf("stdout = %q, want the report with %q", stdout, want)
	}
}

// A switch takes his words or nothing: --asked with none or too many, anything
// else after the switch, and anything at all after status or report are
// refused before the supervisor is asked.
func TestAFKSwitchRefusesWhatItCannotCarryBeforeTheSupervisor(t *testing.T) {
	for name, args := range map[string][]string{
		"on with blank words":           {"on", "--asked", "  "},
		"on with --asked and no words":  {"on", "--asked"},
		"off with empty words":          {"off", "--asked", ""},
		"on with stray words":           {"on", "please"},
		"on with words past the bound":  {"on", "--asked", strings.Repeat("a", 501)},
		"off with words after his ask":  {"off", "--asked", "I'm back", "now"},
		"status with anything after it": {"status", "--asked", "I'm back"},
		"report with anything after it": {"report", "now"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			runtime, h := afkRuntime(t)
			asked := 0
			runtime.switchAFK = func(home.Home, bool, string) error { asked++; return nil }

			// Act
			exit, _, stderr := afkCommand(runtime, args...)

			// Assert
			if exit != 2 || stderr == "" || asked != 0 {
				t.Fatalf("exit=%d stderr=%q asked=%d, want it refused before the supervisor", exit, stderr, asked)
			}
			if switched, _ := afk.Read(h.State); switched.On {
				t.Error("AFK mode reads as on after a refused switch")
			}
		})
	}
}

// cfo afk report prints each held item as it stands in the supervisor's
// records when it is run, and says so: once he answers a held question after
// AFK mode is off, the report no longer says it waits on him. A record it
// cannot read is an error, never the report's older word.
func TestAFKReportPrintsEachHeldItemAsItStandsNow(t *testing.T) {
	// Arrange
	runtime, h := afkRuntime(t)
	ended := time.Now().UTC()
	if err := afk.SaveReport(h.State, afk.Report{Session: "afk-1", Since: ended.Add(-time.Hour), Ended: ended, From: overlordsShell, EndedFrom: overlordsShell, Held: []afk.Held{{Item: "question:drop-legacy", What: "Migration 0042 drops legacy_invoices. Apply it?", At: ended.Add(-time.Minute), Waiting: true, Now: "still waiting on you"}}}); err != nil {
		t.Fatal(err)
	}
	board := func(question supervisor.Question) {
		t.Helper()
		data, err := json.Marshal(supervisor.Database{Schema: 1, Questions: []supervisor.Question{question}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.State, ".supervisor.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	board(supervisor.Question{ID: "drop-legacy", Status: "pending"})

	// Act
	waitingExit, waiting, waitingErr := afkCommand(runtime, "report")
	board(supervisor.Question{ID: "drop-legacy", Status: "succeeded", AnsweredBy: "overlord", Answer: "Keep it held"})
	answeredExit, answered, answeredErr := afkCommand(runtime, "report")
	if err := os.WriteFile(filepath.Join(h.State, ".supervisor.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	unreadExit, unread, unreadErr := afkCommand(runtime, "report")

	// Assert
	if waitingExit != 0 || !strings.Contains(waiting, "Held for you (1), each as it stands now") || !strings.Contains(waiting, "Now: still waiting on you.") {
		t.Errorf("before his answer: exit=%d stderr=%q stdout:\n%s\nwant the question held, still waiting, said to be current", waitingExit, waitingErr, waiting)
	}
	if answeredExit != 0 || !strings.Contains(answered, "Held for you (1), each as it stands now") || !strings.Contains(answered, "Now: you answered it: Keep it held.") || strings.Contains(answered, "still waiting on you") {
		t.Errorf("after his answer: exit=%d stderr=%q stdout:\n%s\nwant the question held, answered by him", answeredExit, answeredErr, answered)
	}
	if unreadExit != 1 || unread != "" || !strings.Contains(unreadErr, "what became of each item held for him cannot be read") {
		t.Errorf("with the records unreadable: exit=%d stdout=%q stderr=%q, want it refused", unreadExit, unread, unreadErr)
	}
}

// His words are bounded in characters, not bytes: 500 of any script are
// taken, normalized to one line, and 501 are refused before the supervisor.
func TestAFKOnAtHisAskBoundsHisWordsInCharacters(t *testing.T) {
	for name, words := range map[string]string{
		"500 plain characters":     strings.Repeat("a", 500),
		"500 multibyte characters": strings.Repeat("界", 500),
		"500 emoji over two lines": strings.Repeat("🙂", 250) + "\n\t " + strings.Repeat("🙂", 249),
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			runtime, h := afkRuntime(t)

			// Act
			exit, _, stderr := afkCommand(runtime, "on", "--asked", words)

			// Assert
			switched, err := afk.Read(h.State)
			want := strings.Join(strings.Fields(words), " ")
			if exit != 0 || err != nil || !switched.On || switched.Asked != want {
				t.Errorf("exit=%d stderr=%q switch=%+v (%v), want it on with his words on one line", exit, stderr, switched, err)
			}
		})
	}
	for name, words := range map[string]string{
		"501 plain characters":     strings.Repeat("a", 501),
		"501 multibyte characters": strings.Repeat("界", 501),
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			runtime, h := afkRuntime(t)
			asked := 0
			runtime.switchAFK = func(home.Home, bool, string) error { asked++; return nil }

			// Act
			exit, _, stderr := afkCommand(runtime, "on", "--asked", words)

			// Assert
			if exit != 2 || asked != 0 || !strings.Contains(stderr, "in at most 500 characters") {
				t.Errorf("exit=%d stderr=%q asked=%d, want it refused before the supervisor", exit, stderr, asked)
			}
			if switched, _ := afk.Read(h.State); switched.On {
				t.Error("AFK mode reads as on after a refused switch")
			}
		})
	}
}
