package onboarding

import (
	"errors"
	"io"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var readConsoleInput = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReadConsoleInputW")

func ChooseConsole(output io.Writer, title string, choices []string, selected int) (int, error) {
	input, screen := windows.Handle(os.Stdin.Fd()), windows.Handle(os.Stdout.Fd())
	var inputMode, screenMode uint32
	if windows.GetConsoleMode(input, &inputMode) != nil || windows.GetConsoleMode(screen, &screenMode) != nil {
		return 0, errors.New("quick start needs an interactive terminal; run goblins in Windows Terminal")
	}
	if err := windows.SetConsoleMode(input, inputMode&^(windows.ENABLE_LINE_INPUT|windows.ENABLE_ECHO_INPUT|windows.ENABLE_PROCESSED_INPUT)); err != nil {
		return 0, err
	}
	defer windows.SetConsoleMode(input, inputMode)
	if err := windows.SetConsoleMode(screen, screenMode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
		return 0, err
	}
	defer windows.SetConsoleMode(screen, screenMode)
	menu := Menu{Output: output, ReadKey: func() (Key, error) {
		for {
			var event struct {
				Type       uint16
				Padding    uint16
				IsDown     int32
				Repeat     uint16
				VirtualKey uint16
				Scan       uint16
				Character  uint16
				Control    uint32
			}
			var count uint32
			result, _, err := readConsoleInput.Call(uintptr(input), uintptr(unsafe.Pointer(&event)), 1, uintptr(unsafe.Pointer(&count)))
			if result == 0 {
				return 0, err
			}
			if count == 0 || event.Type != 1 || event.IsDown == 0 {
				continue
			}
			switch event.VirtualKey {
			case 13:
				return KeyEnter, nil
			case 38:
				return KeyUp, nil
			case 40:
				return KeyDown, nil
			case 27:
				return KeyEscape, nil
			case 66:
				return KeyBoard, nil
			}
			if event.Character == 3 {
				return KeyEscape, nil
			}
		}
	}}
	return menu.Choose(title, choices, selected)
}
