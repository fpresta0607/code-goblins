package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// standInVariable names the file a stand-in for goblins writes its arguments
// to. Set, it makes this test binary that stand-in: copied beside a window as
// goblins.exe, it prints what standInPrints holds, says what standInSays holds
// and exits with standInExit.
const (
	standInVariable = "GOBLINS_WINDOW_TEST_STANDIN"
	standInPrints   = "GOBLINS_WINDOW_TEST_STANDIN_PRINTS"
	standInSays     = "GOBLINS_WINDOW_TEST_STANDIN_SAYS"
	standInExit     = "GOBLINS_WINDOW_TEST_STANDIN_EXIT"
)

func TestMain(m *testing.M) {
	if record := os.Getenv(standInVariable); record != "" {
		if err := os.WriteFile(record, []byte(strings.Join(os.Args[1:], " ")), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(90)
		}
		fmt.Fprint(os.Stdout, os.Getenv(standInPrints))
		fmt.Fprint(os.Stderr, os.Getenv(standInSays))
		code, _ := strconv.Atoi(os.Getenv(standInExit))
		os.Exit(code)
	}
	os.Exit(m.Run())
}

// goblinsBeside puts a stand-in for goblins beside a window in a folder of
// the test's own, and returns the window's path and the file the stand-in
// writes its arguments to.
func goblinsBeside(t *testing.T, prints, says string, exit int) (window, record string) {
	t.Helper()
	folder := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	program, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, goblinsName), program, 0o755); err != nil {
		t.Fatal(err)
	}
	record = filepath.Join(folder, "arguments")
	t.Setenv(standInVariable, record)
	t.Setenv(standInPrints, prints)
	t.Setenv(standInSays, says)
	t.Setenv(standInExit, strconv.Itoa(exit))
	return filepath.Join(folder, "goblins-window.exe"), record
}

// The window program started alone, from the Start menu, the desktop or at
// login, asks the goblins beside it where the board is, with --window
// --locate, and shows that board itself. It never has goblins start a window
// of its own: that window would be goblins' child, whose parents stop where
// goblins exited, and the supervisor would take its board for no one's, so
// the Overlord's AFK switch and Update would be refused there.
func TestTheWindowAloneAsksTheGoblinsBesideItWhereTheBoardIs(t *testing.T) {
	// Arrange
	window, record := goblinsBeside(t, "http://127.0.0.1:4310\r\nC:\\Users\\overlord\\AppData\\Local\\CodeGoblins\\state\r\n", "", 0)

	// Act
	board, stateDir, message := locate(window)

	// Assert
	if message != "" {
		t.Errorf("locate tells the user %q, want nothing when goblins named the board", message)
	}
	if board != "http://127.0.0.1:4310" || stateDir != `C:\Users\overlord\AppData\Local\CodeGoblins\state` {
		t.Errorf("locate = %q, %q, want the board and the state folder goblins printed", board, stateDir)
	}
	if ran, err := os.ReadFile(record); err != nil || string(ran) != "--window --locate" {
		t.Errorf("goblins was run with %q (%v), want --window --locate", ran, err)
	}
}

// A goblins that ends well but names no board, as one older than --locate
// might, leaves the user told the board did not open, and no window is shown
// on a board nobody named.
func TestAGoblinsThatNamesNoBoardIsExplained(t *testing.T) {
	for name, prints := range map[string]string{
		"nothing":         "",
		"only an address": "http://127.0.0.1:4310\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			window, _ := goblinsBeside(t, prints, "", 0)

			// Act
			board, stateDir, message := locate(window)

			// Assert
			if board != "" || stateDir != "" {
				t.Errorf("locate = %q, %q, want no board from a goblins that named none", board, stateDir)
			}
			if !strings.Contains(message, "Code Goblins could not open the board.") || !strings.Contains(message, "did not say where the board is") {
				t.Errorf("the message does not say the board did not open and why:\n%s", message)
			}
		})
	}
}

// saidIn returns the lines of goblins' that message shows, between what the
// window says before them and where it says to see more.
func saidIn(t *testing.T, message string) []string {
	t.Helper()
	parts := strings.Split(message, "\n\n")
	if len(parts) != 3 {
		t.Fatalf("the message is not goblins' lines between two of the window's own:\n%s", message)
	}
	return strings.Split(parts[1], "\n")
}

// When goblins cannot open the board, the user is told why in goblins' own
// words, which say what to do, and where to see more; only the end of a long
// answer is shown.
func TestABoardThatDoesNotOpenIsExplainedInGoblinsWords(t *testing.T) {
	// Arrange
	var said []string
	for line := 1; line <= 20; line++ {
		said = append(said, fmt.Sprintf("line %d", line))
	}
	said = append(said, "goblins: the board's address 127.0.0.1:4310 is in use by another program, so no supervisor was started")
	window, _ := goblinsBeside(t, "", strings.Join(said, "\r\n")+"\r\n", 1)

	// Act
	_, _, message := locate(window)

	// Assert
	for _, want := range []string{"Code Goblins could not open the board.", "run: goblins"} {
		if !strings.Contains(message, want) {
			t.Errorf("the message lacks %q:\n%s", want, message)
		}
	}
	if shown := saidIn(t, message); len(shown) != 16 || !slices.Equal(shown, said[len(said)-16:]) {
		t.Errorf("the message shows %d lines, want the last 16 goblins said, from line 6 on, in order:\n%s", len(shown), message)
	}
}

// All that goblins says when a supervisor does not start is shown, in order:
// what to do about it, where serve.log is, and the end of that log after them.
func TestASupervisorThatDoesNotStartIsExplainedInFull(t *testing.T) {
	const header = `goblins: the supervisor did not start; the end of C:\home\state\serve.log says:`
	var tail []string
	for line := 1; line <= 12; line++ {
		tail = append(tail, fmt.Sprintf("serve log line %d", line))
	}
	for name, said := range map[string][]string{
		"the board's address is in use": append([]string{
			`goblins: the board's address 127.0.0.1:4310 is in use by another program, so no supervisor was started (listen tcp 127.0.0.1:4310: bind: Only one usage of each socket address (protocol/network address/port) is normally permitted.). Close that program, or give this home's board an address of its own by setting CFO_BOARD_ADDRESS, for example to 127.0.0.1:4311`,
			header,
		}, tail...),
		"another process held serve.log": append([]string{
			`goblins: the supervisor was not started, because another process held C:\home\state\serve.log (start cfo serve`,
			`open C:\home\state\serve.log: The process cannot access the file because it is being used by another process.)`,
			header,
		}, tail...),
		"serve.log holds nothing": {header, "(nothing)"},
		"this home's supervisor recorded no board": {
			"goblins: this home's supervisor (pid 4242) holds the board's address 127.0.0.1:4310 but recorded no board. End that process in Windows PowerShell, then run goblins again:",
			"  Stop-Process -Id 4242",
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			window, _ := goblinsBeside(t, "", strings.Join(said, "\n")+"\n", 1)

			// Act
			_, _, message := locate(window)

			// Assert
			if shown := saidIn(t, message); len(shown) > 16 || !slices.Equal(shown, said) {
				t.Errorf("the message shows %d of the %d lines goblins said, want them all, in order, in at most 16:\n%s", len(shown), len(said), message)
			}
		})
	}
}

// A goblins that fails without a word still leaves the user told that the
// board did not open, with how the program ended.
func TestAGoblinsThatSaysNothingIsStillExplained(t *testing.T) {
	window, _ := goblinsBeside(t, "", "", 3)

	_, _, message := locate(window)

	if !strings.Contains(message, "Code Goblins could not open the board.") || !strings.Contains(message, "exit status 3") {
		t.Errorf("the message does not say the board did not open and how goblins ended:\n%s", message)
	}
}

// A window with no goblins beside it, as one copied somewhere on its own,
// says that Code Goblins is not installed there and what to do, and runs
// nothing.
func TestAWindowWithNoGoblinsBesideItSaysSo(t *testing.T) {
	window := filepath.Join(t.TempDir(), "goblins-window.exe")

	_, _, message := locate(window)

	if !strings.Contains(message, filepath.Join(filepath.Dir(window), "goblins.exe")+" is missing") || !strings.Contains(message, "Run the Code Goblins install again") {
		t.Errorf("the message does not name the missing goblins and the install:\n%s", message)
	}
}
