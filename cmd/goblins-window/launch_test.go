package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// standInVariable names the file a stand-in for goblins writes its arguments
// to. Set, it makes this test binary that stand-in: copied beside a window as
// goblins.exe, it says what standInSays holds and exits with standInExit.
const (
	standInVariable = "GOBLINS_WINDOW_TEST_STANDIN"
	standInSays     = "GOBLINS_WINDOW_TEST_STANDIN_SAYS"
	standInExit     = "GOBLINS_WINDOW_TEST_STANDIN_EXIT"
)

func TestMain(m *testing.M) {
	if record := os.Getenv(standInVariable); record != "" {
		if err := os.WriteFile(record, []byte(strings.Join(os.Args[1:], " ")), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(90)
		}
		fmt.Fprint(os.Stderr, os.Getenv(standInSays))
		code, _ := strconv.Atoi(os.Getenv(standInExit))
		os.Exit(code)
	}
	os.Exit(m.Run())
}

// goblinsBeside puts a stand-in for goblins beside a window in a folder of
// the test's own, and returns the window's path and the file the stand-in
// writes its arguments to.
func goblinsBeside(t *testing.T, says string, exit int) (window, record string) {
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
	t.Setenv(standInSays, says)
	t.Setenv(standInExit, strconv.Itoa(exit))
	return filepath.Join(folder, "goblins-window.exe"), record
}

// The window program started alone opens the app through the goblins beside
// it, which is the one that finds or starts the supervisor: with --window,
// and with --background too when it was started for the tray.
func TestTheWindowAloneOpensTheAppThroughTheGoblinsBesideIt(t *testing.T) {
	for name, test := range map[string]struct {
		background bool
		want       string
	}{
		"opened from the Start menu": {false, "--window"},
		"started at login":           {true, "--window --background"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			window, record := goblinsBeside(t, "", 0)

			// Act
			message := launch(window, test.background)

			// Assert
			if message != "" {
				t.Errorf("launch tells the user %q, want nothing when goblins opened the board", message)
			}
			if ran, err := os.ReadFile(record); err != nil || string(ran) != test.want {
				t.Errorf("goblins was run with %q (%v), want %s", ran, err, test.want)
			}
		})
	}
}

// When goblins cannot open the board, the user is told why in goblins' own
// words, which say what to do, and where to see more; only the end of a long
// answer is shown.
func TestABoardThatDoesNotOpenIsExplainedInGoblinsWords(t *testing.T) {
	// Arrange
	var said strings.Builder
	for line := 1; line <= 20; line++ {
		fmt.Fprintf(&said, "line %d\r\n", line)
	}
	said.WriteString("goblins: the board's address 127.0.0.1:4310 is in use by another program, so no supervisor was started\r\n")
	window, _ := goblinsBeside(t, said.String(), 1)

	// Act
	message := launch(window, false)

	// Assert
	for _, want := range []string{"Code Goblins could not open the board.", "goblins: the board's address 127.0.0.1:4310 is in use by another program", "line 10\n", "run: goblins"} {
		if !strings.Contains(message, want) {
			t.Errorf("the message lacks %q:\n%s", want, message)
		}
	}
	if strings.Contains(message, "line 9\n") {
		t.Errorf("the message shows more than the last %d lines goblins said:\n%s", saidLines, message)
	}
}

// A goblins that fails without a word still leaves the user told that the
// board did not open, with how the program ended.
func TestAGoblinsThatSaysNothingIsStillExplained(t *testing.T) {
	window, _ := goblinsBeside(t, "", 3)

	message := launch(window, false)

	if !strings.Contains(message, "Code Goblins could not open the board.") || !strings.Contains(message, "exit status 3") {
		t.Errorf("the message does not say the board did not open and how goblins ended:\n%s", message)
	}
}

// A window with no goblins beside it, as one copied somewhere on its own,
// says that Code Goblins is not installed there and what to do, and runs
// nothing.
func TestAWindowWithNoGoblinsBesideItSaysSo(t *testing.T) {
	window := filepath.Join(t.TempDir(), "goblins-window.exe")

	message := launch(window, false)

	if !strings.Contains(message, filepath.Join(filepath.Dir(window), "goblins.exe")+" is missing") || !strings.Contains(message, "Run the Code Goblins install again") {
		t.Errorf("the message does not name the missing goblins and the install:\n%s", message)
	}
}
