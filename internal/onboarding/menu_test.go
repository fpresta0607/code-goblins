package onboarding

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestMenuAcceptsDefaultNavigatesAndCancels(t *testing.T) {
	for _, test := range []struct {
		name      string
		keys      []Key
		want      int
		cancelled bool
	}{
		{"enter", []Key{KeyEnter}, 1, false},
		{"up", []Key{KeyUp, KeyEnter}, 0, false},
		{"down", []Key{KeyDown, KeyEnter}, 2, false},
		{"wrap", []Key{KeyDown, KeyDown, KeyEnter}, 0, false},
		{"escape", []Key{KeyEscape}, 0, true},
		{"eof", nil, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			index := 0
			menu := Menu{Output: &output, ReadKey: func() (Key, error) {
				if index == len(test.keys) {
					return 0, io.EOF
				}
				key := test.keys[index]
				index++
				return key, nil
			}}
			got, err := menu.Choose("Choose your CFO", []string{"Claude Code", "Codex", "pi"}, 1)
			if test.cancelled {
				if !errors.Is(err, ErrCancelled) {
					t.Fatalf("err=%v", err)
				}
			} else if err != nil || got != test.want {
				t.Fatalf("got=%d err=%v", got, err)
			}
			for _, text := range []string{"Choose your CFO", "Press Enter to continue", "Up/Down", "Esc", "Codex"} {
				if !strings.Contains(output.String(), text) {
					t.Errorf("missing %q", text)
				}
			}
		})
	}
}
