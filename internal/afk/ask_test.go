package afk

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// A switch the CFO makes at the Overlord's ask carries his words: the switch,
// the log, the notice and the report keep them, and say the CFO made it.

const (
	theCFO   = "the CFO at his ask (claude pid 4242)"
	askedOn  = "I'm stepping away, turn AFK on"
	askedOff = "I'm back, turn AFK off"
)

func TestASwitchAtHisAskKeepsHisWordsInTheSwitchAndTheLog(t *testing.T) {
	// Arrange
	dir := t.TempDir()

	// Act: his words as he typed them, over two lines.
	on, changed, onErr := TurnOnAtHisAsk(dir, theCFO, "  I'm stepping away,\n\tturn AFK on ", nil, night)
	off, offErr := TurnOffAtHisAsk(dir, theCFO, askedOff, night.Add(time.Hour))

	// Assert
	if onErr != nil || !changed || !on.On || on.From != theCFO || on.Asked != askedOn {
		t.Fatalf("TurnOnAtHisAsk = %+v, %v, %v, want it on by the CFO with his words on one line", on, changed, onErr)
	}
	if offErr != nil || off.On || off.From != theCFO || off.Asked != askedOn || off.EndedFrom != theCFO || off.EndedAsked != askedOff {
		t.Fatalf("TurnOffAtHisAsk = %+v, %v, want it off by the CFO with both asks kept", off, offErr)
	}
	read, err := Read(dir)
	if err != nil || read.Asked != askedOn || read.EndedAsked != askedOff {
		t.Errorf("Read = %+v, %v, want the switch file to keep both asks", read, err)
	}
	entries, unreadable, err := Entries(dir, on.Session)
	if err != nil || unreadable != 0 || len(entries) != 2 ||
		entries[0].Kind != KindOn || entries[0].What != theCFO || entries[0].Evidence != "his words: "+askedOn ||
		entries[1].Kind != KindOff || entries[1].What != theCFO || entries[1].Evidence != "his words: "+askedOff {
		t.Errorf("log = %+v (%d unreadable, %v), want the on and the off, each by the CFO with his words", entries, unreadable, err)
	}
}

func TestASwitchAtHisAskWithoutHisWordsIsNotMade(t *testing.T) {
	for name, asked := range map[string]string{"none": "", "blank": " \t\n", "past their bound": strings.Repeat("a", 501), "past their bound in multibyte characters": strings.Repeat("界", 501)} {
		t.Run("on with "+name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()

			// Act
			_, changed, err := TurnOnAtHisAsk(dir, theCFO, asked, nil, night)

			// Assert
			if err == nil || changed || !strings.Contains(err.Error(), "carries his words, in at most 500 characters") {
				t.Fatalf("TurnOnAtHisAsk = %v, %v, want it refused for want of his words", changed, err)
			}
			if state, _ := Read(dir); state.On {
				t.Errorf("the switch = %+v, want it still off", state)
			}
			if entries, _, _ := Entries(dir, ""); len(entries) != 0 {
				t.Errorf("log = %+v, want nothing", entries)
			}
		})
		t.Run("off with "+name, func(t *testing.T) {
			// Arrange
			dir, _ := turnedOn(t)

			// Act
			_, err := TurnOffAtHisAsk(dir, theCFO, asked, night.Add(time.Hour))

			// Assert
			if err == nil || !strings.Contains(err.Error(), "carries his words, in at most 500 characters") {
				t.Fatalf("TurnOffAtHisAsk = %v, want it refused for want of his words", err)
			}
			if state, _ := Read(dir); !state.On {
				t.Errorf("the switch = %+v, want it still on", state)
			}
			if entries, _, _ := Entries(dir, ""); len(entries) != 1 {
				t.Errorf("log = %+v, want only the line that turned it on", entries)
			}
		})
	}
}

// His words are bounded in characters, not bytes: 500 characters of any
// script are his words, whatever bytes they take.
func TestASwitchAtHisAskTakesFiveHundredCharactersOfAnyScript(t *testing.T) {
	for name, asked := range map[string]string{"plain": strings.Repeat("a", 500), "multibyte": strings.Repeat("界", 500), "emoji": strings.Repeat("🙂", 500)} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()

			// Act
			on, changed, err := TurnOnAtHisAsk(dir, theCFO, asked, nil, night)

			// Assert
			if err != nil || !changed || !on.On || on.Asked != asked {
				t.Errorf("TurnOnAtHisAsk = %v, %v, %v, want it on with all 500 characters of his words", on.On, changed, err)
			}
		})
	}
}

// His words belong to the switch they asked for: his own off after the CFO's
// on carries none, and the next stretch starts with none.
func TestHisOwnSwitchAfterTheCFOsCarriesNoWordsOfHis(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	if _, _, err := TurnOnAtHisAsk(dir, theCFO, askedOn, nil, night); err != nil {
		t.Fatal(err)
	}

	// Act
	off, offErr := TurnOff(dir, "his own board (goblins-window.exe pid 4242)", night.Add(time.Hour))
	again, _, againErr := TurnOn(dir, "his own board (goblins-window.exe pid 4242)", nil, night.Add(2*time.Hour))

	// Assert
	if offErr != nil || off.Asked != askedOn || off.EndedFrom != "his own board (goblins-window.exe pid 4242)" || off.EndedAsked != "" {
		t.Errorf("his own off = %+v, %v, want the CFO's on kept with his words and no words for his own off", off, offErr)
	}
	if againErr != nil || !again.On || again.Asked != "" || again.EndedAsked != "" || again.EndedFrom != "" {
		t.Errorf("the next stretch = %+v, %v, want it to start with nothing of the last one", again, againErr)
	}
	entries, _, err := Entries(dir, off.Session)
	if err != nil || len(entries) != 2 || entries[1].Kind != KindOff || entries[1].Evidence != "" {
		t.Errorf("log = %+v (%v), want his own off logged with no words", entries, err)
	}
}

func TestWhoMadeASwitchIsSaidWithHisWordsWhenTheCFOMadeIt(t *testing.T) {
	for name, c := range map[string]struct{ from, asked, want string }{
		"his own terminal":        {"his own terminal (powershell.exe pid 4242)", "", "from his own terminal (powershell.exe pid 4242)"},
		"the CFO at his ask":      {theCFO, askedOn, `by the CFO at his ask (claude pid 4242), in his words "I'm stepping away, turn AFK on"`},
		"words that hold a quote": {theCFO, `say "afk on"`, `by the CFO at his ask (claude pid 4242), in his words "say \"afk on\""`},
		"words with an accent":    {theCFO, "pon AFK, adi\u00f3s", "by the CFO at his ask (claude pid 4242), in his words \"pon AFK, adi\u00f3s\""},
	} {
		if got := SwitchedBy(c.from, c.asked); got != c.want {
			t.Errorf("%s: SwitchedBy = %q, want %q", name, got, c.want)
		}
	}
}

func TestTheNoticeSaysTheCFOTurnedItOnAtHisAskWithHisWords(t *testing.T) {
	// Act
	asked := Notice(State{On: true, Since: night, From: theCFO, Asked: askedOn})
	own := Notice(State{On: true, Since: night, From: "his own terminal (powershell.exe pid 4242)"})

	// Assert
	if len(asked) == 0 || len(own) == 0 {
		t.Fatalf("the notices = %q and %q, want both said", asked, own)
	}
	if want := `AFK MODE IS ON: it was turned on 2026-10-02 02:10 UTC by the CFO at his ask (claude pid 4242), in his words "I'm stepping away, turn AFK on", and the Supreme Overlord is away until it is turned off.`; asked[0] != want {
		t.Errorf("the notice of a switch at his ask opens %q, want %q", asked[0], want)
	}
	if want := "AFK MODE IS ON: the Supreme Overlord turned it on 2026-10-02 02:10 UTC from his own terminal (powershell.exe pid 4242), and he is away until he turns it off."; own[0] != want {
		t.Errorf("the notice of his own switch opens %q, want %q", own[0], want)
	}
	if strings.Join(asked[1:], "\n") != strings.Join(own[1:], "\n") {
		t.Error("the terms under the notice differ with who made the switch, want the same terms")
	}
}

func TestTheReportSaysTheCFOMadeASwitchAtHisAskAndQuotesHim(t *testing.T) {
	// Arrange
	report := nightReport()
	report.From, report.Asked = theCFO, askedOn

	// Act
	var out bytes.Buffer
	if err := Render(&out, report); err != nil {
		t.Fatal(err)
	}

	// Assert
	if want := `turned on by the CFO at his ask (claude pid 4242), in his words "I'm stepping away, turn AFK on", off from his own terminal (powershell.exe pid 5151).`; !strings.Contains(out.String(), want) {
		t.Errorf("the report does not say %q:\n%s", want, out.String())
	}
}

// Coming back, the Overlord reads what is held for him first, then what the
// CFO decided.
func TestTheReportListsWhatIsHeldBeforeWhatWasDecided(t *testing.T) {
	// Act
	var out bytes.Buffer
	if err := Render(&out, nightReport()); err != nil {
		t.Fatal(err)
	}

	// Assert
	report := out.String()
	var at []int
	for _, phrase := range []string{"AFK mode was on from", "Held for you (2)", "Migration 0042 drops legacy_invoices. Apply it?", "Merged (1)", "Answered for goblins (1)", "Goblins finished (1)", "Spent"} {
		index := strings.Index(report, phrase)
		if index < 0 {
			t.Fatalf("the report does not say %q:\n%s", phrase, report)
		}
		at = append(at, index)
	}
	for i := 1; i < len(at); i++ {
		if at[i] <= at[i-1] {
			t.Fatalf("the report's order is %v, want what is held right after its first line and before every decision:\n%s", at, report)
		}
	}
}
