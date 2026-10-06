package afk

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var night = time.Date(2026, 10, 2, 2, 10, 0, 0, time.UTC)

func turnedOn(t *testing.T) (string, State) {
	t.Helper()
	dir := t.TempDir()
	state, changed, err := TurnOn(dir, "his terminal: powershell.exe pid 4242", []Allowance{{Provider: "claude", Window: "week", PercentUsed: 40}}, night)
	if err != nil || !changed {
		t.Fatalf("TurnOn = %+v, %v, %v, want it turned on", state, changed, err)
	}
	return dir, state
}

func TestAFKModeIsOffInAHomeWhereItWasNeverTurnedOn(t *testing.T) {
	state, err := Read(t.TempDir())

	if err != nil || state.On || state.Session != "" {
		t.Fatalf("Read = %+v, %v, want off with no stretch", state, err)
	}
}

func TestTurningOnRecordsWhereAndWhenAndLogsIt(t *testing.T) {
	dir, state := turnedOn(t)

	read, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !read.On || !read.Since.Equal(night) || read.From != "his terminal: powershell.exe pid 4242" || read.Session == "" || read.Session != state.Session {
		t.Errorf("state = %+v, want on since %s from his terminal, in one named stretch", read, night)
	}
	if len(read.Allowance) != 1 || read.Allowance[0].PercentUsed != 40 {
		t.Errorf("allowance = %+v, want the reading taken when it turned on", read.Allowance)
	}
	entries, unreadable, err := Entries(dir, state.Session)
	if err != nil || unreadable != 0 || len(entries) != 1 || entries[0].Kind != KindOn || entries[0].What != state.From || !entries[0].At.Equal(night) {
		t.Errorf("log = %+v (%d unreadable, %v), want the one line that turned it on", entries, unreadable, err)
	}
}

func TestTurningOnWhatIsAlreadyOnChangesNothing(t *testing.T) {
	dir, first := turnedOn(t)

	second, changed, err := TurnOn(dir, "the board", nil, night.Add(time.Hour))

	if err != nil || changed || second.Session != first.Session || !second.Since.Equal(night) || second.From != first.From {
		t.Errorf("second TurnOn = %+v, %v, %v, want the first stretch unchanged", second, changed, err)
	}
	if entries, _, _ := Entries(dir, ""); len(entries) != 1 {
		t.Errorf("log = %+v, want only the line that turned it on", entries)
	}
}

func TestTurningOffKeepsTheStretchItEnded(t *testing.T) {
	dir, on := turnedOn(t)
	morning := night.Add(10 * time.Hour)

	off, err := TurnOff(dir, "the board", morning)

	if err != nil || off.On || off.Session != on.Session || !off.Since.Equal(night) || !off.Ended.Equal(morning) || off.EndedFrom != "the board" {
		t.Fatalf("TurnOff = %+v, %v, want the stretch kept with when and where it ended", off, err)
	}
	if read, err := Read(dir); err != nil || read.On || read.Session != on.Session {
		t.Errorf("Read after TurnOff = %+v, %v, want off with the stretch kept", read, err)
	}
	entries, _, _ := Entries(dir, on.Session)
	if len(entries) != 2 || entries[1].Kind != KindOff || entries[1].What != "the board" {
		t.Errorf("log = %+v, want the line that turned it off after the one that turned it on", entries)
	}
	if _, err := TurnOff(dir, "the board", morning); !errors.Is(err, ErrNotOn) {
		t.Errorf("a second TurnOff = %v, want %v", err, ErrNotOn)
	}
}

func TestEachStretchHasItsOwnNameAndItsOwnLines(t *testing.T) {
	dir, first := turnedOn(t)
	if _, err := TurnOff(dir, "the board", night.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	second, changed, err := TurnOn(dir, "the board", nil, night.Add(2*time.Hour))

	if err != nil || !changed || second.Session == first.Session || !second.Ended.IsZero() || second.EndedFrom != "" {
		t.Fatalf("a second stretch = %+v, %v, %v, want a new one that has not ended", second, changed, err)
	}
	if entries, _, _ := Entries(dir, second.Session); len(entries) != 1 || entries[0].Kind != KindOn {
		t.Errorf("the second stretch's lines = %+v, want only its own", entries)
	}
}

func TestADecisionIsLoggedInTheStretchItWasMadeIn(t *testing.T) {
	dir, on := turnedOn(t)

	logged, err := Log(dir, Entry{Kind: KindDeploy, What: "northwind production", Link: "https://northwind.example/health", Evidence: "deploy 41 is live; /health reads 200 with commit abc1234"}, night.Add(time.Hour))

	if err != nil || logged.Session != on.Session || !logged.At.Equal(night.Add(time.Hour)) {
		t.Fatalf("Log = %+v, %v, want the line stamped with its stretch and time", logged, err)
	}
	entries, _, _ := Entries(dir, on.Session)
	if len(entries) != 2 || entries[1].Kind != KindDeploy || entries[1].What != "northwind production" || entries[1].Link != "https://northwind.example/health" || entries[1].Evidence != logged.Evidence || !entries[1].At.Equal(logged.At) {
		t.Errorf("log = %+v, want the decision after the switch", entries)
	}
}

func TestNothingIsLoggedAsDecidedWhileAFKModeIsOff(t *testing.T) {
	dir := t.TempDir()

	_, err := Log(dir, Entry{Kind: KindDeploy, What: "northwind production", Evidence: "deploy 41 is live"}, night)

	if !errors.Is(err, ErrNotOn) {
		t.Fatalf("Log while off = %v, want %v", err, ErrNotOn)
	}
	if entries, _, _ := Entries(dir, ""); len(entries) != 0 {
		t.Errorf("log = %+v, want nothing", entries)
	}
}

func TestADecisionWithoutEvidenceOrOfAnUnknownKindIsRefused(t *testing.T) {
	for name, entry := range map[string]Entry{
		"no evidence":      {Kind: KindDeploy, What: "northwind production"},
		"blank evidence":   {Kind: KindDeploy, What: "northwind production", Evidence: "  "},
		"no subject":       {Kind: KindDeploy, Evidence: "deploy 41 is live"},
		"unknown kind":     {Kind: "drop-table", What: "legacy_invoices", Evidence: "it was in the way"},
		"the switch":       {Kind: KindOn, What: "the CFO", Evidence: "it seemed useful"},
		"a held item":      {Kind: KindHeld, What: "a question", Evidence: "held"},
		"turning it off":   {Kind: KindOff, What: "the CFO", Evidence: "done for the night"},
		"evidence too big": {Kind: KindDeploy, What: "northwind production", Evidence: strings.Repeat("x", 8001)},
	} {
		t.Run(name, func(t *testing.T) {
			dir, _ := turnedOn(t)

			_, err := Log(dir, entry, night.Add(time.Minute))

			if err == nil {
				t.Fatalf("Log(%+v) was accepted", entry)
			}
			if entries, _, _ := Entries(dir, ""); len(entries) != 1 {
				t.Errorf("log = %+v, want only the switch", entries)
			}
		})
	}
}

func TestAnItemIsHeldOnlyWhileAFKModeIsOn(t *testing.T) {
	dir, on := turnedOn(t)
	held := Entry{Item: "question:drop-legacy-invoices", What: "Migration 0042 drops legacy_invoices. Apply it?", Recommendation: "Keep it held"}

	if err := Hold(dir, held, night.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	entries, _, _ := Entries(dir, on.Session)
	if len(entries) != 2 || entries[1].Kind != KindHeld || entries[1].Item != held.Item || entries[1].What != held.What || entries[1].Session != on.Session {
		t.Errorf("log = %+v, want the held item in the stretch", entries)
	}
	if len(entries) == 2 && entries[1].Recommendation != held.Recommendation {
		t.Errorf("held line = %+v, want it with what was recommended for it", entries[1])
	}
	if err := Hold(dir, Entry{What: "no item"}, night.Add(time.Minute)); err == nil {
		t.Error("a held line that names no item was accepted")
	}
	if _, err := TurnOff(dir, "the board", night.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := Hold(dir, held, night.Add(2*time.Hour)); !errors.Is(err, ErrNotOn) {
		t.Errorf("Hold while off = %v, want %v", err, ErrNotOn)
	}
}

// A line cut short, as a crash mid-write leaves one, is counted, never
// dropped unseen: a log that says less than it holds must say so.
func TestALogLineThatCannotBeReadIsCounted(t *testing.T) {
	dir, on := turnedOn(t)
	f, err := os.OpenFile(filepath.Join(dir, "afk.audit"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{\"at\":\"2026-10-02T03:00:00Z\",\"session\":\"" + on.Session + "\",\"kind\":\"mer\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	entries, unreadable, err := Entries(dir, on.Session)

	if err != nil || len(entries) != 1 || unreadable != 1 {
		t.Fatalf("Entries = %+v, %d unreadable, %v, want the switch and one line counted unreadable", entries, unreadable, err)
	}
}

// A switch file that cannot be read is never taken for off: off is what lets
// the board prompt and what stops the CFO deciding, so a guess either way is
// a wrong answer given with confidence.
func TestASwitchThatCannotBeReadIsAnErrorNotOff(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "afk.json"), []byte("{\"on\": tr"), 0o600); err != nil {
		t.Fatal(err)
	}

	if state, err := Read(dir); err == nil {
		t.Fatalf("Read = %+v with no error, want the unreadable switch reported", state)
	}
	if _, _, err := TurnOn(dir, "his terminal", nil, night); err == nil {
		t.Error("TurnOn over an unreadable switch was accepted")
	}
}

// The Overlord's off always works: a switch that cannot be read is put back
// to off from where he did it, and the log says it was reset and why.
func TestResettingASwitchThatCannotBeReadPutsItBackToOff(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "afk.json"), []byte("{\"on\": tr"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, unread := Read(dir)

	// Act
	err := Reset(dir, "his own terminal (powershell.exe pid 4242)", unread, night)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if state, err := Read(dir); err != nil || state.On || state.Session != "" {
		t.Errorf("Read after Reset = %+v, %v, want off with no stretch", state, err)
	}
	entries, unreadable, err := Entries(dir, "")
	if err != nil || unreadable != 0 || len(entries) != 1 {
		t.Fatalf("log = %+v (%d unreadable, %v), want the one line of the reset", entries, unreadable, err)
	}
	if reset := entries[0]; reset.Kind != KindOff || reset.What != "his own terminal (powershell.exe pid 4242)" || !reset.At.Equal(night) || !strings.Contains(reset.Evidence, "could not be read") || !strings.Contains(reset.Evidence, unread.Error()) {
		t.Errorf("logged = %+v, want the switch turned off from his terminal, saying it could not be read and why", reset)
	}
}
