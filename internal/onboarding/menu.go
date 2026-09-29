package onboarding

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

type Key int

const (
	KeyEnter Key = iota + 1
	KeyUp
	KeyDown
	KeyEscape
	KeyBoard
)

type Menu struct {
	Output  io.Writer
	ReadKey func() (Key, error)
}

func (m Menu) Choose(title string, choices []string, selected int) (int, error) {
	if len(choices) == 0 || selected < 0 || selected >= len(choices) {
		return 0, errors.New("invalid menu default")
	}
	fmt.Fprintf(m.Output, "\n\x1b[38;2;110;231;183mCODE GOBLINS\x1b[0m\n\n%s\n\n", title)
	for {
		for index, choice := range choices {
			if index == selected {
				fmt.Fprintf(m.Output, "\x1b[2K\x1b[48;2;15;24;32m\x1b[38;2;110;231;183m  > %s\x1b[0m\n", choice)
			} else {
				fmt.Fprintf(m.Output, "\x1b[2K    %s\n", choice)
			}
		}
		fmt.Fprint(m.Output, "\x1b[2K\n\x1b[2KPress Enter to continue   Up/Down to choose   Esc to cancel\n")
		key, err := m.ReadKey()
		if err != nil {
			return 0, ErrCancelled
		}
		switch key {
		case KeyEnter:
			return selected, nil
		case KeyEscape:
			return 0, ErrBack
		case KeyUp:
			selected = (selected + len(choices) - 1) % len(choices)
		case KeyDown:
			selected = (selected + 1) % len(choices)
		case KeyBoard:
			for index, choice := range choices {
				if strings.HasPrefix(choice, "[B]") {
					return index, nil
				}
			}
		}
		fmt.Fprintf(m.Output, "\x1b[%dA", len(choices)+2)
	}
}
