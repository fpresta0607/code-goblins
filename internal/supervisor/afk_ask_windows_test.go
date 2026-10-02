package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// The CFO makes the Overlord's switch when he asks it to in his own words.
// The supervisor proves the process that asks is the registered CFO's, keeps
// his words with the switch, and makes it for nobody else.

const (
	hisAskOn  = "I'm stepping away, turn AFK on"
	hisAskOff = "I'm back, turn AFK off"
)

// asTheCFO is a supervisor whose pipe proves this test process is the
// registered CFO, with nothing of the fleet in its environment: the tests run
// under go test, and often under a goblin's harness.
func asTheCFO(t *testing.T) (*Service, home.Home, string) {
	t.Helper()
	store, h := testStore(t)
	primary, _, _, cfo := primaryFixture(t, store)
	for _, name := range []string{"CFO_ROLE", "NO_MISTAKES_GATE"} {
		t.Setenv(name, "")
	}
	s := &Service{Store: store, Options: Options{CFO: cfo}}
	runPipe(t, s)
	return s, h, fmt.Sprintf("the CFO at his ask (%s pid %d)", primary.Agent, primary.Process.PID)
}

func TestTheRegisteredCFOSwitchesAFKModeAtHisAskAndTheSwitchKeepsHisWords(t *testing.T) {
	// Arrange
	_, h, theCFO := asTheCFO(t)

	// Act: his words arrive as he typed them, on two lines.
	onErr := SwitchAFKAtHisAsk(h, true, "  I'm stepping away,\n turn AFK on ")
	on, onReadErr := afk.Read(h.State)
	offErr := SwitchAFKAtHisAsk(h, false, hisAskOff)

	// Assert
	if onErr != nil || onReadErr != nil || !on.On || on.From != theCFO || on.Asked != hisAskOn {
		t.Fatalf("on at his ask = %v, switch %+v (%v), want it on by %s with his words on one line", onErr, on, onReadErr, theCFO)
	}
	off, err := afk.Read(h.State)
	if offErr != nil || err != nil || off.On || off.Asked != hisAskOn || off.EndedFrom != theCFO || off.EndedAsked != hisAskOff {
		t.Fatalf("off at his ask = %v, switch %+v (%v), want it off by %s with both asks kept", offErr, off, err, theCFO)
	}
	entries := afkEntries(t, h.State)
	if len(entries) != 2 ||
		entries[0].Kind != afk.KindOn || entries[0].What != theCFO || entries[0].Evidence != "his words: "+hisAskOn ||
		entries[1].Kind != afk.KindOff || entries[1].What != theCFO || entries[1].Evidence != "his words: "+hisAskOff {
		t.Errorf("the AFK log = %+v, want the on and the off, each by the CFO with his words", entries)
	}
	report, found, err := afk.ReadReport(h.State)
	if err != nil || !found || report.From != theCFO || report.Asked != hisAskOn || report.EndedFrom != theCFO || report.EndedAsked != hisAskOff {
		t.Errorf("the report = %+v (found %v, %v), want who switched it and his words both times", report, found, err)
	}
	pending, err := wake.Pending(h.State)
	if err != nil || len(pending) != 2 ||
		!strings.Contains(pending[0].Detail, `AFK mode was turned on by `+theCFO+`, in his words "`+hisAskOn+`"`) ||
		!strings.Contains(pending[1].Detail, `AFK mode was turned off by `+theCFO+`, in his words "`+hisAskOff+`"`) {
		t.Errorf("the CFO's queue = %+v, %v, want the on and the off, each by the CFO with his words", pending, err)
	}
}

// Who asks is what the supervisor proves, so his words open the switch to
// nobody but the registered CFO: a goblin and a gate agent are refused though
// they pass words and though they run under the CFO, and so is a process
// outside it.
func TestASwitchAtHisAskIsMadeForNobodyButTheRegisteredCFO(t *testing.T) {
	for _, c := range []struct {
		name    string
		arrange func(t *testing.T, s *Service, h home.Home)
		refusal string
	}{
		{name: "a goblin", refusal: "this command runs in a goblin's terminal", arrange: func(t *testing.T, _ *Service, _ home.Home) {
			t.Setenv("CFO_ROLE", "goblin")
		}},
		{name: "a gate agent", refusal: "this command runs as a gate agent", arrange: func(t *testing.T, _ *Service, _ home.Home) {
			t.Setenv("NO_MISTAKES_GATE", "1")
		}},
		{name: "a process outside the registered CFO", refusal: "does not run under the registered CFO", arrange: func(t *testing.T, _ *Service, h home.Home) {
			registerElsewhere(t, h.State)
		}},
		{name: "no CFO registered", refusal: "could not prove the process that asked for it with his words is the registered CFO's", arrange: func(t *testing.T, _ *Service, h home.Home) {
			if err := os.Remove(filepath.Join(h.State, "primary.json")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a supervisor that cannot verify the CFO", refusal: "this supervisor cannot verify the CFO", arrange: func(_ *testing.T, s *Service, _ home.Home) {
			s.Options.CFO = nil
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			s, h, _ := asTheCFO(t)
			c.arrange(t, s, h)

			// Act
			err := SwitchAFKAtHisAsk(h, true, hisAskOn)

			// Assert
			if err == nil || !strings.Contains(err.Error(), c.refusal) || !strings.Contains(err.Error(), "the registered CFO only at his ask, with his words") {
				t.Fatalf("SwitchAFKAtHisAsk = %v, want it refused as %q, with who makes the switch", err, c.refusal)
			}
			if switched, err := afk.Read(h.State); err != nil || switched.On {
				t.Errorf("the switch = %+v, %v, want it still off", switched, err)
			}
			if entries := afkEntries(t, h.State); len(entries) != 0 {
				t.Errorf("the AFK log = %+v, want nothing", entries)
			}
			if pending, _ := wake.Pending(h.State); len(pending) != 0 {
				t.Errorf("the CFO's queue = %+v, want nothing", pending)
			}
		})
	}
}

// A switch at his ask carries his words. Without them the CFO's request is the
// plain one, which is his alone, and words that say nothing, or run past their
// bound, are refused before and behind the pipe.
func TestTheCFOsSwitchWithoutHisWordsIsRefused(t *testing.T) {
	for _, c := range []struct {
		name    string
		ask     func(h home.Home) error
		refusal string
	}{
		{name: "the plain switch", refusal: "under the registered CFO", ask: func(h home.Home) error { return SwitchAFK(h, true) }},
		{name: "blank words", refusal: "carries his words", ask: func(h home.Home) error { return SwitchAFKAtHisAsk(h, true, " \t\n ") }},
		{name: "blank words sent past the command", refusal: "carries his words", ask: func(h home.Home) error {
			return sendPipeRequest(h.State, runPipeRequest{Kind: "afk-on", Asked: " \t "})
		}},
		{name: "words past their bound sent past the command", refusal: "in at most 500 characters", ask: func(h home.Home) error {
			return sendPipeRequest(h.State, runPipeRequest{Kind: "afk-on", Asked: strings.Repeat("a", 501)})
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			_, h, _ := asTheCFO(t)

			// Act
			err := c.ask(h)

			// Assert
			if err == nil || !strings.Contains(err.Error(), c.refusal) {
				t.Fatalf("the switch = %v, want it refused as %q", err, c.refusal)
			}
			if switched, err := afk.Read(h.State); err != nil || switched.On {
				t.Errorf("the switch = %+v, %v, want it still off", switched, err)
			}
			if entries := afkEntries(t, h.State); len(entries) != 0 {
				t.Errorf("the AFK log = %+v, want nothing", entries)
			}
		})
	}
}

// His own switch works whoever made the last one: what the CFO turned on at
// his ask he turns off from his terminal or his board, and what he turned on
// the CFO turns off when he asks it to.
func TestHisOwnSwitchTurnsOffWhatTheCFOTurnedOnAndTheCFOTurnsOffHisAtHisAsk(t *testing.T) {
	t.Run("his terminal turns off what the CFO turned on", func(t *testing.T) {
		// Arrange
		s, h, theCFO := asTheCFO(t)
		if err := SwitchAFKAtHisAsk(h, true, hisAskOn); err != nil {
			t.Fatal(err)
		}
		asOverlordsTerminal(s)

		// Act
		err := SwitchAFK(h, false)

		// Assert
		switched, readErr := afk.Read(h.State)
		if err != nil || readErr != nil || switched.On || switched.From != theCFO || switched.Asked != hisAskOn || switched.EndedFrom != "his own terminal (powershell.exe pid 4242)" || switched.EndedAsked != "" {
			t.Fatalf("his off = %v, switch %+v (%v), want it off from his own terminal, with no words of his for the off", err, switched, readErr)
		}
		report, found, err := afk.ReadReport(h.State)
		if err != nil || !found || report.Asked != hisAskOn || report.EndedFrom != switched.EndedFrom || report.EndedAsked != "" {
			t.Errorf("the report = %+v (found %v, %v), want the CFO's on with his words and his own off", report, found, err)
		}
	})

	t.Run("his board turns off what the CFO turned on", func(t *testing.T) {
		// Arrange
		s, h, _ := asTheCFO(t)
		if err := SwitchAFKAtHisAsk(h, true, hisAskOn); err != nil {
			t.Fatal(err)
		}

		// Act: the board's switch, once its program is proven his.
		s.runRequests.Lock()
		err := s.switchAFKAs(context.Background(), "his own board (goblins-window.exe pid 4242)", "", false)
		s.runRequests.Unlock()

		// Assert
		if switched, readErr := afk.Read(h.State); err != nil || readErr != nil || switched.On || switched.EndedFrom != "his own board (goblins-window.exe pid 4242)" || switched.EndedAsked != "" {
			t.Fatalf("his board's off = %v, switch %+v (%v), want it off from his own board", err, switched, readErr)
		}
	})

	t.Run("the CFO turns off at his ask what he turned on himself", func(t *testing.T) {
		// Arrange
		s, h, theCFO := asTheCFO(t)
		asOverlordsTerminal(s)
		if err := SwitchAFK(h, true); err != nil {
			t.Fatal(err)
		}

		// Act
		err := SwitchAFKAtHisAsk(h, false, hisAskOff)

		// Assert
		if switched, readErr := afk.Read(h.State); err != nil || readErr != nil || switched.On || switched.From != "his own terminal (powershell.exe pid 4242)" || switched.Asked != "" || switched.EndedFrom != theCFO || switched.EndedAsked != hisAskOff {
			t.Fatalf("the CFO's off = %v, switch %+v (%v), want his own on kept and the off by the CFO with his words", err, switched, readErr)
		}
	})
}

// A switch that cannot be read is the Overlord's to reset. The CFO's off at
// his ask leaves it as it is and says whose the reset is.
func TestTheCFOCannotResetASwitchThatCannotBeRead(t *testing.T) {
	// Arrange
	_, h, _ := asTheCFO(t)
	broken := []byte("{not json")
	path := filepath.Join(h.State, "afk.json")
	if err := os.WriteFile(path, broken, 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	err := SwitchAFKAtHisAsk(h, false, hisAskOff)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "only the Overlord resets it") {
		t.Fatalf("the CFO's off of a switch that cannot be read = %v, want it refused as the Overlord's to reset", err)
	}
	if kept, readErr := os.ReadFile(path); readErr != nil || string(kept) != string(broken) {
		t.Errorf("the switch file = %q (%v), want it left as it was", kept, readErr)
	}
	if entries := afkEntries(t, h.State); len(entries) != 0 {
		t.Errorf("the AFK log = %+v, want nothing", entries)
	}
}

// The board says who turned AFK mode on, so a stretch the CFO turned on at his
// ask reaches the board with his words, and the report page with both asks.
func TestTheBoardIsToldTheCFOSwitchedAFKModeAtHisAskWithHisWords(t *testing.T) {
	// Arrange
	s, h, theCFO := asTheCFO(t)
	if err := SwitchAFKAtHisAsk(h, true, hisAskOn); err != nil {
		t.Fatal(err)
	}

	// Act
	on, onErr := s.afkView(s.Store.Snapshot())
	offErr := SwitchAFKAtHisAsk(h, false, hisAskOff)
	reportCode, reportBody := askTheBoard(t, s, "GET", "/api/afk/report", "", nil)

	// Assert
	if onErr != nil || on.State != "on" || on.From != theCFO || on.Asked != hisAskOn {
		t.Errorf("the board's view while on = %+v (%v), want on by %s with his words", on, onErr, theCFO)
	}
	data, err := json.Marshal(on)
	if err != nil || !strings.Contains(string(data), `"asked":"I'm stepping away, turn AFK on"`) {
		t.Errorf("the view's JSON = %s (%v), want the asked field the board reads", data, err)
	}
	var page struct {
		Found      bool   `json:"found"`
		From       string `json:"from"`
		Asked      string `json:"asked"`
		EndedFrom  string `json:"ended_from"`
		EndedAsked string `json:"ended_asked"`
	}
	if err := json.Unmarshal([]byte(reportBody), &page); offErr != nil || reportCode != 200 || err != nil {
		t.Fatalf("off = %v, GET /api/afk/report = %d %s (%v)", offErr, reportCode, reportBody, err)
	}
	if !page.Found || page.From != theCFO || page.Asked != hisAskOn || page.EndedFrom != theCFO || page.EndedAsked != hisAskOff {
		t.Errorf("the report page = %+v, want who switched it and his words both times", page)
	}
}
