package supervisor

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
)

func TestNativeCFOComposerProgram(t *testing.T) {
	arguments := flag.Args()
	if len(arguments) != 4 || arguments[0] != "cfo-composer-program" {
		return
	}
	stateDir, events, mode := arguments[1], arguments[2], arguments[3]
	harness := "codex"
	if strings.HasPrefix(mode, "claude-") {
		harness, mode = "claude", strings.TrimPrefix(mode, "claude-")
	}
	record := func(value string) {
		file, err := os.OpenFile(events, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			os.Exit(2)
		}
		fmt.Fprintln(file, value)
		file.Close()
	}
	promptEvent := func(name string) {
		data, err := json.Marshal(map[string]string{"session_id": "session-1", "cwd": stateDir, "hook_event_name": name})
		if err != nil {
			os.Exit(2)
		}
		event, err := nativehook.Normalize(strings.NewReader(string(data)), nativehook.Context{Harness: harness, HostID: os.Getenv(host.IDVariable)})
		if err != nil || nativehook.Spool(stateDir, event) != nil {
			os.Exit(2)
		}
	}
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(10 * time.Millisecond) {
		registered, err := Register(stateDir, "", "session-1")
		if err == nil {
			record("registered " + registered)
			break
		}
		if time.Now().After(deadline) {
			record("registration failed: " + err.Error())
			os.Exit(2)
		}
	}
	if mode == "busy" {
		promptEvent("SessionStart")
	}
	if err := windows.SetConsoleMode(windows.Handle(os.Stdin.Fd()), windows.ENABLE_VIRTUAL_TERMINAL_INPUT); err != nil {
		os.Exit(2)
	}
	line, offer, hasPasteEnded := "", strings.HasPrefix(mode, "daybreak"), false
	// A Claude Code fixture takes its input as Claude Code 2.1.312 did: a
	// bracketed paste is one paste however it is read, while keys typed in a
	// burst longer than claudeRead bytes are taken as a paste of the first
	// claudeRead bytes and keys after them, and a turn's queue then keeps
	// only the keys, as the live CFO's did on 2026-10-29 (#1740).
	paste, readAsPaste, isPasting := "", "", false
	composer := func() string { return paste + readAsPaste + line }
	draw := func(isWorking bool) {
		rows := []string{}
		// A daybreak-stuck offer keeps its rows once Escape dismissed it, as
		// Codex's console keeps an offer an earlier redraw drew, and they own
		// no key (#1556).
		if offer || mode == "daybreak-stuck" {
			rows = append(rows, "Set up security for Daybreak mode", "› 1. Set up security", "Press a number to choose · esc to dismiss · type to continue")
		} else if mode == "late-modal" && composer() != "" {
			fmt.Print("\x1b[2J\x1b[H" + strings.Join([]string{"Do you trust the contents of this directory?", "› 1. Yes, continue", "  2. No, quit", "", "Press enter to continue"}, "\r\n"))
			return
		}
		if isWorking || mode == "busy" {
			if harness == "claude" {
				rows = append(rows, "✻ Working…")
			} else {
				rows = append(rows, "• Working (1s • esc to interrupt)")
			}
		}
		if harness == "claude" {
			rule := strings.Repeat("─", 80)
			shown := line
			if paste != "" || readAsPaste != "" {
				shown = "[Pasted text #1]" + line
			}
			rows = append(rows, rule, "❯ "+shown, rule, "⏵⏵ bypass permissions on (shift+tab to cycle)")
		} else {
			text := line
			if text == "" {
				text = "Ask Codex to do anything"
			}
			rows = append(rows, "› "+text, "100% context left")
		}
		fmt.Print("\x1b[2J\x1b[H" + strings.Join(rows, "\r\n"))
	}
	draw(false)
	reader := bufio.NewReader(os.Stdin)
	for {
		key, _, err := reader.ReadRune()
		if err != nil {
			return
		}
		switch key {
		case '\x1b':
			// A key sent as a control sequence, such as End before Codex's
			// submit, ends a paste and leaves the line as it is.
			if next, err := reader.Peek(min(1, reader.Buffered())); err == nil && len(next) == 1 && next[0] == '[' {
				sequence := ""
				for {
					final, err := reader.ReadByte()
					if err != nil {
						return
					}
					sequence += string(final)
					if final >= 0x40 && final <= 0x7e && final != '[' {
						break
					}
				}
				switch sequence {
				case "[200~":
					isPasting = true
				case "[201~":
					isPasting = false
					draw(false)
				default:
					hasPasteEnded = true
				}
				continue
			}
			record("escape")
			offer = false
			draw(false)
		case '\r', '\n':
			if mode == "ignored-enter" {
				record("ignored-enter")
				continue
			}
			if offer || mode == "late-modal" && composer() != "" {
				record("dialog-enter")
				continue
			}
			if mode == "paste" && !hasPasteEnded {
				hasPasteEnded = true
				record("paste-ended")
				continue
			}
			if mode == "busy" {
				queued := paste + line
				record("queued " + queued)
				go func(text string) {
					for {
						if _, err := os.Stat(events + ".accept"); err == nil {
							promptEvent("UserPromptSubmit")
							record("accepted " + text)
							return
						}
						time.Sleep(10 * time.Millisecond)
					}
				}(queued)
			} else {
				record("accepted " + composer())
			}
			line, paste, readAsPaste = "", "", ""
			draw(true)
		default:
			if offer && composer() == "" {
				record("typed-under-dialog")
			}
			if isPasting {
				paste += string(key)
				continue
			}
			line += string(key)
			if harness == "claude" && readAsPaste == "" && len(line) >= claudeRead {
				readAsPaste, line = line, ""
			}
			// What is typed in one go is drawn once it is all read, as a
			// harness draws a paste: a draw for each character of several
			// answers typed as one message outlasts the wait to see it shown.
			if reader.Buffered() == 0 {
				draw(false)
			}
		}
	}
}

// claudeRead is how much of a burst of keys the live CFO's Claude Code read
// at once on 2026-10-29, and took as a paste (#1740).
const claudeRead = 1024

func nativeCFOComposer(t *testing.T, stateDir, mode string) hostedTerminal {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	shims := t.TempDir()
	events := filepath.Join(t.TempDir(), "events.txt")
	harness := "codex"
	if strings.HasPrefix(mode, "claude-") {
		harness = "claude"
	}
	shim := fmt.Sprintf("@\"%s\" -test.run=^TestNativeCFOComposerProgram$ -- cfo-composer-program \"%s\" \"%s\" %s\r\n", program, stateDir, events, mode)
	if err := os.WriteFile(filepath.Join(shims, harness+".cmd"), []byte(shim), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PANE_ID", "")
	t.Setenv("PATH", shims+string(os.PathListSeparator)+os.Getenv("PATH"))
	terminal := hostProgram(t, stateDir, "cfo", events, os.Getenv("ComSpec"), "/c", harness)
	if lines := terminal.waitForLines(t, 1); len(lines) != 1 || !strings.HasPrefix(lines[0], "registered ") {
		t.Fatalf("fixture registration = %q", lines)
	}
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(10 * time.Millisecond) {
		record, err := host.ReadRecord(stateDir, "cfo")
		if err != nil {
			t.Fatal(err)
		}
		screen, err := host.ReadScreen(record)
		if err == nil && (strings.Contains(strings.Join(screen, " "), "100% context left") || strings.Contains(strings.Join(screen, " "), "bypass permissions on")) {
			return terminal
		}
		if time.Now().After(deadline) {
			t.Fatalf("fixture did not draw its composer: %q, %v", screen, err)
		}
	}
}
