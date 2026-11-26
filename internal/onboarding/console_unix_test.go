//go:build linux || darwin

package onboarding

import (
	"bufio"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/pty"
)

// askRole has this test binary, started again in a terminal, ask one step in
// it with AskConsole, say what it chose, then read one line from the
// terminal.
const askRole = "ONBOARDING_TEST_ASK"

func TestMain(m *testing.M) {
	if os.Getenv(askRole) != "" {
		chosen, err := AskConsole(os.Stdout, Step{Title: "Pick one", Choices: Labels("one", "two", "three")})
		fmt.Printf("chose %d, %v\n", chosen, err)
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		fmt.Printf("read line %q, %v\n", strings.TrimSpace(line), err)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestParseKeysReadsTheKeysATerminalSends(t *testing.T) {
	for _, test := range []struct {
		name  string
		chunk string
		want  []Key
	}{
		{"the arrows", "\x1b[A\x1b[B\x1b[C\x1b[D", []Key{KeyUp, KeyDown, KeyRight, KeyLeft}},
		{"the arrows in application cursor mode", "\x1bOA\x1bOB", []Key{KeyUp, KeyDown}},
		{"an arrow held with a modifier", "\x1b[1;5B", []Key{KeyDown}},
		{"Escape alone", "\x1b", []Key{KeyEscape}},
		{"Escape pressed twice", "\x1b\x1b", []Key{KeyEscape, KeyEscape}},
		{"Ctrl-C, read as Escape", "\x03", []Key{KeyEscape}},
		{"Enter, as a carriage return or a newline", "\r\n", []Key{KeyEnter, KeyEnter}},
		{"letters, in lower case", "yN", []Key{'y', 'n'}},
		{"a letter beyond ASCII", "É", []Key{'é'}},
		{"keys a menu does not act on, skipped", "1 \x1b[3~\x1b[H\t", nil},
		{"a sequence cut off at the end of the read", "\x1b[1;", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			got := parseKeys([]byte(test.chunk))

			// Assert
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parseKeys(%q) = %v, want %v", test.chunk, got, test.want)
			}
		})
	}
}

// shown is what a terminal's program wrote, read as it comes.
type shown struct {
	mu   sync.Mutex
	text strings.Builder
}

func (s *shown) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.text.String()
}

func (s *shown) waitFor(t *testing.T, text string) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); !strings.Contains(s.String(), text); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the terminal never showed %q; it shows %q", text, s.String())
		}
	}
}

// The quick start's menu reads keys from a terminal as they are typed, with
// nothing echoed, and leaves the terminal reading lines again once answered.
func TestAskConsoleReadsKeysInATerminalAndPutsItBack(t *testing.T) {
	for _, test := range []struct {
		name  string
		typed string
		want  string
	}{
		{"Down twice and Enter", "\x1b[B\x1b[B\r", "chose 2, <nil>"},
		{"Down and Up and Enter", "\x1b[B\x1b[A\r", "chose 0, <nil>"},
		{"Escape", "\x1b", "chose 0, " + ErrBack.Error()},
		{"Ctrl-C", "\x03", "chose 0, " + ErrBack.Error()},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			terminal, err := pty.Start(pty.Spec{Args: []string{os.Args[0], "-test.run=^$"}, Env: append(os.Environ(), askRole+"=1"), Cols: 80, Rows: 24})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = terminal.Close() })
			screen := &shown{}
			go func() {
				chunk := make([]byte, 4096)
				for {
					n, err := terminal.Read(chunk)
					screen.mu.Lock()
					screen.text.Write(chunk[:n])
					screen.mu.Unlock()
					if err != nil {
						return
					}
				}
			}()
			screen.waitFor(t, "three")

			// Act
			for _, key := range strings.SplitAfter(test.typed, "\r") {
				if _, err := terminal.Write([]byte(key)); err != nil {
					t.Fatal(err)
				}
			}

			// Assert
			screen.waitFor(t, test.want)
			if _, err := terminal.Write([]byte("hello\r")); err != nil {
				t.Fatal(err)
			}
			screen.waitFor(t, `read line "hello", <nil>`)
			if before := screen.String()[:strings.Index(screen.String(), test.want)]; strings.Contains(before, "[B") || strings.Contains(before, "^[") {
				t.Errorf("the keys typed at the menu were echoed: %q", before)
			}
		})
	}
}
