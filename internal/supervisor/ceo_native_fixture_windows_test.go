package supervisor

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
)

func TestNativeCEOComposerProgram(t *testing.T) {
	arguments := flag.Args()
	if len(arguments) != 4 || arguments[0] != "ceo-composer-program" {
		return
	}
	stateDir, events, mode := arguments[1], arguments[2], arguments[3]
	hasDisplayModel := mode == "display-model"
	harness := "codex"
	if strings.HasPrefix(mode, "claude-") {
		harness, mode = "claude", strings.TrimPrefix(mode, "claude-")
	}
	// A manual-mode Claude Code shows "? for shortcuts" in its footer, under a
	// conversation that asked a question, takes a lone "?" key at an empty
	// prompt line as its shortcut help rather than words, and does nothing
	// with Enter at an empty one, as the live CEO did on 2026-11-02.
	isManual := strings.HasPrefix(mode, "manual")
	if isManual {
		mode = strings.TrimPrefix(strings.TrimPrefix(mode, "manual"), "-")
	}
	// A harness that hooks what it queues runs its prompt hook as it queues a
	// line typed into its turn, as Claude Code 2.1.292 does, before it takes
	// the line at its next tool call.
	isHookedOnQueue := strings.HasSuffix(mode, "-hooks-queued")
	mode = strings.TrimSuffix(mode, "-hooks-queued")
	// A harness that reads its Enter late takes the first one that ends typed
	// text only once the key after it reaches it, as a Claude Code stalled on
	// a loaded machine takes a submit key among the keys that take its words
	// back (#2541).
	isEnterLate := strings.HasSuffix(mode, "-late-enter")
	mode = strings.TrimSuffix(mode, "-late-enter")
	// A harness that stalls reads nothing typed into it while the file
	// beside its events, named .stall, exists, as the live CEO's Claude Code
	// left typed input unread for seconds on 2026-11-10 (#2306).
	isStalling := strings.HasSuffix(mode, "-stalls")
	mode = strings.TrimSuffix(mode, "-stalls")
	// A harness that ignores its send-now chord leaves what its turn queued
	// where it was, as a Claude Code with chat:sendNow bound to other keys
	// would.
	isSendNowIgnored := strings.HasSuffix(mode, "-ignores-send-now")
	mode = strings.TrimSuffix(mode, "-ignores-send-now")
	// A harness that asks has its turn ask permission for a call once a line
	// waits in its queue, the question drawn under its prompt line in its
	// footer's place, not in the prompt line's place as Claude Code 2.1.302
	// draws one, so the line still reads queued under a screen of its own.
	isAsking := strings.HasSuffix(mode, "-asks")
	mode = strings.TrimSuffix(mode, "-asks")
	// A harness joined at its Enter has words land on its prompt line after
	// all the board typed, just ahead of an Enter it takes as a new line
	// (-lost-enter), as words somebody else typed then would reach a harness
	// slow to read its keys (#2541).
	isJoinedAtEnter := strings.HasSuffix(mode, "-joined-at-enter")
	mode = strings.TrimSuffix(mode, "-joined-at-enter")
	// A joined harness has words land on its prompt line right after the
	// first keys typed into it, as words somebody else typed then would: on
	// 2026-11-13 the CEO's Claude Code took a line of the board's with words
	// after them that the board never typed (#2541).
	isJoined := strings.HasSuffix(mode, "-joined")
	mode = strings.TrimSuffix(mode, "-joined")
	// A harness that loses an Enter takes the first one that ends typed text
	// as a new line, as a loaded Claude Code took a goblin's on 2026-10-20,
	// and one whose Enters are lost takes every one so.
	isEnterLost, areEntersLost := strings.HasSuffix(mode, "-lost-enter"), strings.HasSuffix(mode, "-enters-lost")
	mode = strings.TrimSuffix(strings.TrimSuffix(mode, "-lost-enter"), "-enters-lost")
	// A turn that ends as typed keys reach it, and a stop hook that opens one
	// as they reach an idle harness, change what the screen read before them
	// said, as the CEO's turns ended and its stop hook opened the next for a
	// wake through 2026-11-13 (#2541).
	isEnding, isReopening := strings.HasSuffix(mode, "-ends"), strings.HasSuffix(mode, "-reopens")
	mode = strings.TrimSuffix(strings.TrimSuffix(mode, "-ends"), "-reopens")
	// A Claude Code compacting its conversation draws that as its turn's row
	// and queues what is typed meanwhile, as the CEO's did from 01:03Z to
	// 01:10Z on 2026-11-14 (#2541).
	working := "✻ Working…"
	if strings.HasSuffix(mode, "-compacting") {
		working, mode = "✻ Compacting conversation… (19s)", strings.TrimSuffix(mode, "-compacting")
	}
	hasJoined, lost := false, 0
	isHelp := false
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
	// A goblin's terminal registers no CEO.
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(10 * time.Millisecond) {
		if os.Getenv(host.IDVariable) != "ceo" {
			record("registered as no CEO")
			break
		}
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
	// only the keys, as the live CEO's did on 2026-10-29 (#1740).
	// A bracketed paste goes where the cursor is, after what the prompt line
	// already held, which typedBefore keeps.
	paste, readAsPaste, isPasting, typedBefore := "", "", false, ""
	// A busy fixture is in a turn, which takes what is typed into its queue,
	// until Escape interrupts it and, as Claude Code, Codex and pi do, puts
	// what its queue held back on the prompt line, unsent.
	isBusy, hasQueued, held := mode == "busy", false, ""
	// Claude Code 2.1.322 keeps each line queued, gives them all back to the
	// prompt line, a row each, for Up at an empty one, and sends them with
	// its send-now chord, live on 2026-11-10 (#2332).
	var queue []string
	composer := func() string { return typedBefore + paste + readAsPaste + line }
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
		if isManual {
			rows = append(rows, "● Shall I merge it?", "")
		}
		if isWorking || isBusy {
			if harness == "claude" {
				rows = append(rows, working)
			} else {
				rows = append(rows, "• Working (1s • esc to interrupt)")
			}
		}
		if harness == "claude" {
			rule := strings.Repeat("─", 80)
			shown := typedBefore + line
			if paste != "" || readAsPaste != "" {
				shown = typedBefore + "[Pasted text #1]" + line
			}
			// Claude Code 2.1.314 drew this hint in its empty prompt line while a
			// line waited in its queue, live, on 2026-10-30.
			if shown == "" && hasQueued {
				shown = "Press up to edit queued messages"
			}
			footer := "⏵⏵ bypass permissions on (shift+tab to cycle)"
			switch {
			case isHelp:
				footer = "! for bash mode      / for commands      double tap esc to clear input"
			case isManual:
				footer = "⏸ manual mode on · ? for shortcuts"
			}
			rows = append(rows, rule, "❯ "+strings.ReplaceAll(shown, "\n", "\r\n  "), rule)
			if isAsking && hasQueued {
				rows = append(rows, " Do you want to create nine.txt?", " ❯ 1. Yes", "   2. No")
			} else {
				rows = append(rows, footer)
			}
		} else {
			text := typedBefore + line
			if text == "" {
				text = "Ask Codex to do anything"
			}
			rows = append(rows, "› "+text)
			if hasDisplayModel {
				rows = append(rows, "", "  GPT-6.1 Terra xhigh · C:\\work · Register CEO and handle …", "  ? for shortcuts                            ⚠ 8 warnings · f2 to view")
			} else {
				rows = append(rows, "100% context left")
			}
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
		for isStalling {
			if _, err := os.Stat(events + ".stall"); err != nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		switch key {
		case '\x15':
			// Ctrl+U takes typed words back off the prompt line: Codex 0.160
			// the whole line, and Claude Code 2.1.322 the row its cursor is
			// in, claudeRow characters here, or a paste's placeholder whole,
			// live on 2026-11-10 (#2306).
			had := composer()
			switch {
			case harness != "claude":
				line, paste, readAsPaste, typedBefore = "", "", "", ""
			case strings.HasSuffix(line, "\n"):
				// The row above the cursor's emptied one joins it.
				line = strings.TrimSuffix(line, "\n")
			case line != "":
				row := line[strings.LastIndex(line, "\n")+1:]
				line = strings.TrimSuffix(line, row) + string([]rune(row)[:max(0, len([]rune(row))-claudeRow)])
			case paste != "" || readAsPaste != "":
				paste, readAsPaste = "", ""
			default:
				typedBefore = string([]rune(typedBefore)[:max(0, len([]rune(typedBefore))-claudeRow)])
			}
			if had != "" && composer() == "" {
				record("cleared")
			}
			draw(false)
		case '\x18':
			// Claude Code's chat:sendNow chord, Ctrl+X Ctrl+S, sends what its
			// turn has queued at once.
			if next, _, err := reader.ReadRune(); err == nil && next == '\x13' {
				if isSendNowIgnored {
					record("send now ignored")
					continue
				}
				record("sent now")
				hasQueued, queue = false, nil
				draw(false)
			}
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
					if paste == "" && readAsPaste == "" {
						typedBefore, line = line, ""
					}
				case "[201~":
					isPasting = false
					draw(false)
				case "[A":
					// Up at an empty prompt line takes the queue back onto it.
					if harness == "claude" && composer() == "" && len(queue) > 0 {
						record("took back " + strings.Join(queue, " | "))
						line, hasQueued, queue, held = strings.Join(queue, "\n"), false, nil, ""
						draw(false)
					}
				default:
					hasPasteEnded = true
				}
				continue
			}
			if isBusy {
				record("interrupted")
				isBusy, hasQueued, line, held, queue = false, false, held, "", nil
				draw(false)
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
			if isEnterLate && composer() != "" {
				isEnterLate = false
				if _, _, err := reader.ReadRune(); err != nil || reader.UnreadRune() != nil {
					return
				}
			}
			if composer() != "" && (areEntersLost || isEnterLost && lost == 0) {
				lost++
				record("enter taken as a new line")
				if isJoinedAtEnter {
					line += " and the line beside it"
				}
				line += "\n"
				draw(false)
				continue
			}
			// An Enter that submits drops the new lines lost Enters left after
			// the words, as Claude Code trims what it submits.
			line = strings.TrimRight(line, "\n")
			if offer || mode == "late-modal" && composer() != "" {
				record("dialog-enter")
				continue
			}
			if mode == "paste" && !hasPasteEnded {
				hasPasteEnded = true
				record("paste-ended")
				continue
			}
			if isManual && composer() == "" {
				record("empty-enter")
				continue
			}
			// Codex 0.160 lists its skills while the line ends in a $ word and
			// takes an Enter then as a pick from that list, the line unsent.
			if harness == "codex" && codexMention.MatchString(composer()) {
				record("mention-picked")
				continue
			}
			// A busy fixture runs one command of its harness's own at once
			// beside its turn, which goes on, as Claude Code 2.1.322 runs
			// /remote-control and Codex 0.160 runs /status, and queues every
			// other line (#2372).
			if isBusy && composer() == map[string]string{"claude": "/remote-control", "codex": "/status"}[harness] {
				record("ran " + composer())
			} else if isBusy {
				queued := typedBefore + paste + line
				record("queued " + queued)
				hasQueued, held, queue = true, queued, append(queue, queued)
				if isHookedOnQueue {
					promptEvent("UserPromptSubmit")
				}
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
			// A slash command the harness runs itself starts no turn, and an
			// unmoved harness clears its input line for any line it takes
			// before its working row draws.
			isCommand := strings.HasPrefix(composer(), "/")
			line, paste, readAsPaste, typedBefore = "", "", "", ""
			draw(!isCommand && mode != "unmoved")
		default:
			if offer && composer() == "" {
				record("typed-under-dialog")
			}
			if isEnding && isBusy {
				record("turn ended")
				isBusy, isEnding = false, false
			}
			if isReopening && !isBusy {
				record("turn opened by its stop hook")
				isBusy, isReopening = true, false
			}
			if isPasting {
				paste += string(key)
				continue
			}
			if isManual && key == '?' && composer() == "" && reader.Buffered() == 0 {
				isHelp = !isHelp
				record("shortcuts")
				draw(false)
				continue
			}
			isHelp = false
			line += string(key)
			if harness == "claude" && readAsPaste == "" && len(line) >= claudeRead {
				readAsPaste, line = line, ""
			}
			// What is typed in one go is drawn once it is all read, as a
			// harness draws a paste: a draw for each character of several
			// answers typed as one message outlasts the wait to see it shown.
			if reader.Buffered() == 0 {
				if isJoined && !hasJoined {
					line, hasJoined = line+" and the line beside it", true
				}
				draw(false)
			}
		}
	}
}

// claudeRead is how much of a burst of keys the live CEO's Claude Code read
// at once on 2026-10-29, and took as a paste (#1740).
const claudeRead = 1024

// claudeRow is how many characters of its prompt line one Ctrl+U takes off a
// Claude Code fixture, as Claude Code takes the one row its cursor is in.
const claudeRow = 100

// codexMention matches a line that ends in a $ word, over which Codex 0.160
// lists its skills.
var codexMention = regexp.MustCompile(`(^|\s)\$\S*$`)

func nativeCEOComposer(t *testing.T, stateDir, mode string) hostedTerminal {
	t.Helper()
	return nativeComposer(t, stateDir, "ceo", mode)
}

// nativeComposer hosts the fixture harness of mode as terminal id: the CEO's,
// or a goblin's, which registers no CEO.
func nativeComposer(t *testing.T, stateDir, id, mode string) hostedTerminal {
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
	shim := fmt.Sprintf("@\"%s\" -test.run=^TestNativeCEOComposerProgram$ -- ceo-composer-program \"%s\" \"%s\" %s\r\n", program, stateDir, events, mode)
	if err := os.WriteFile(filepath.Join(shims, harness+".cmd"), []byte(shim), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PANE_ID", "")
	t.Setenv("PATH", shims+string(os.PathListSeparator)+os.Getenv("PATH"))
	terminal := hostProgram(t, stateDir, id, events, os.Getenv("ComSpec"), "/c", harness)
	if lines := terminal.waitForLines(t, 1); len(lines) != 1 || !strings.HasPrefix(lines[0], "registered ") {
		t.Fatalf("fixture registration = %q", lines)
	}
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(10 * time.Millisecond) {
		record, err := host.ReadRecord(stateDir, id)
		if err != nil {
			t.Fatal(err)
		}
		screen, err := host.ReadScreen(record)
		if err == nil && (strings.Contains(strings.Join(screen, " "), "100% context left") || strings.Contains(strings.Join(screen, " "), "bypass permissions on") || strings.Contains(strings.Join(screen, " "), "manual mode on") || mode == "display-model" && strings.Contains(strings.Join(screen, " "), "? for shortcuts")) {
			return terminal
		}
		if time.Now().After(deadline) {
			t.Fatalf("fixture did not draw its composer: %q, %v", screen, err)
		}
	}
}
