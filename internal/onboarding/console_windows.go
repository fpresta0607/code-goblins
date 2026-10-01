package onboarding

import (
	"io"
	"os"
	"unicode"
	"unsafe"

	"golang.org/x/sys/windows"
)

var readConsoleInput = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReadConsoleInputW")

// keyEvent is Windows' INPUT_RECORD holding a KEY_EVENT_RECORD.
type keyEvent struct {
	Type       uint16
	_          uint16
	Down       int32
	Repeat     uint16
	VirtualKey uint16
	Scan       uint16
	Character  uint16
	Control    uint32
}

// ChooseConsole shows a menu in this process's console and reads its keys as
// Windows key events, so Escape and the arrows are told apart with no timing.
// The console's modes are put back when it returns. Without a console it
// returns ErrNoConsole and accepts nothing.
func ChooseConsole(output io.Writer, title string, choices []Choice, selected int) (int, error) {
	input, screen := windows.Handle(os.Stdin.Fd()), windows.Handle(os.Stdout.Fd())
	var inputMode, screenMode uint32
	if windows.GetConsoleMode(input, &inputMode) != nil || windows.GetConsoleMode(screen, &screenMode) != nil {
		return 0, ErrNoConsole
	}
	if err := windows.SetConsoleMode(input, inputMode&^(windows.ENABLE_LINE_INPUT|windows.ENABLE_ECHO_INPUT|windows.ENABLE_PROCESSED_INPUT)); err != nil {
		return 0, err
	}
	defer windows.SetConsoleMode(input, inputMode)
	if err := windows.SetConsoleMode(screen, screenMode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
		return 0, err
	}
	defer windows.SetConsoleMode(screen, screenMode)
	width := func() int {
		var info windows.ConsoleScreenBufferInfo
		if windows.GetConsoleScreenBufferInfo(screen, &info) != nil {
			return 0
		}
		return int(info.Window.Right-info.Window.Left) + 1
	}
	menu := Menu{Output: output, Width: width, ReadKey: func() (Key, error) {
		for {
			var event keyEvent
			var count uint32
			if result, _, err := readConsoleInput.Call(uintptr(input), uintptr(unsafe.Pointer(&event)), 1, uintptr(unsafe.Pointer(&count))); result == 0 {
				return 0, err
			}
			const keyEventType = 1
			if count == 0 || event.Type != keyEventType || event.Down == 0 {
				continue
			}
			switch event.VirtualKey {
			case windows.VK_RETURN:
				return KeyEnter, nil
			case windows.VK_UP:
				return KeyUp, nil
			case windows.VK_DOWN:
				return KeyDown, nil
			case windows.VK_ESCAPE:
				return KeyEscape, nil
			}
			// Ctrl-C arrives as a character while processed input is off.
			if event.Character == 3 {
				return KeyEscape, nil
			}
			if letter := unicode.ToLower(rune(event.Character)); unicode.IsLetter(letter) {
				return Key(letter), nil
			}
		}
	}}
	return menu.Choose(title, choices, selected)
}
