package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthStorePreservesStdinExceptLeadingBOMAndTrailingLineBreaks(t *testing.T) {
	for _, test := range []struct {
		name            string
		input           string
		want            string
		removedBOMCount int
	}{
		{"one unicode BOM", "\ufefffake-token\r\n", "fake-token", 1},
		{"two unicode BOMs", "\ufeff\ufefffake-token\r\n", "fake-token", 2},
		{"three unicode BOMs", "\ufeff\ufeff\ufefffake-token\r\n", "fake-token", 3},
		{"one mojibake BOM", "\u00ef\u00bb\u00bffake-token\r\n", "fake-token", 1},
		{"two mojibake BOMs", "\u00ef\u00bb\u00bf\u00ef\u00bb\u00bffake-token\n", "fake-token", 2},
		{"three mojibake BOMs", "\u00ef\u00bb\u00bf\u00ef\u00bb\u00bf\u00ef\u00bb\u00bffake-token\n", "fake-token", 3},
		{"unicode then mojibake", "\ufeff\u00ef\u00bb\u00bffake-token\n", "fake-token", 2},
		{"mojibake then unicode", "\u00ef\u00bb\u00bf\ufefffake-token\n", "fake-token", 2},
		{"alternating mixed BOMs", "\ufeff\u00ef\u00bb\u00bf\ufeff\u00ef\u00bb\u00bffake-token\n", "fake-token", 4},
		{"repeated mixed BOMs", "\u00ef\u00bb\u00bf\u00ef\u00bb\u00bf\ufeff\ufefffake-token\n", "fake-token", 4},
		{"no BOM", "fake-token\r\n", "fake-token", 0},
		{"no line break", "fake-token", "fake-token", 0},
		{"LF line break", "fake-token\n", "fake-token", 0},
		{"spaces and tabs", " \tfake-token\t \r\n", " \tfake-token\t ", 0},
		{"unicode lower neighbour", "\ufefefake-token\n", "\ufefefake-token", 0},
		{"unicode upper neighbour", "\uff00fake-token\n", "\uff00fake-token", 0},
		{"mojibake lower neighbour", "\u00ef\u00bb\u00befake-token\n", "\u00ef\u00bb\u00befake-token", 0},
		{"mojibake upper neighbour", "\u00ef\u00bb\u00c0fake-token\n", "\u00ef\u00bb\u00c0fake-token", 0},
		{"BOM then unicode neighbour", "\ufeff\ufefefake-token\n", "\ufefefake-token", 1},
		{"BOM then mojibake neighbour", "\u00ef\u00bb\u00bf\u00ef\u00bb\u00befake-token\n", "\u00ef\u00bb\u00befake-token", 1},
		{"partial mojibake", "\u00ef\u00bbfake-token\n", "\u00ef\u00bbfake-token", 0},
		{"embedded BOMs", "fake-\ufeff\u00ef\u00bb\u00bftoken\n", "fake-\ufeff\u00ef\u00bb\u00bftoken", 0},
		{"BOM after space", " \ufefffake-token\n", " \ufefffake-token", 0},
		{"BOM with whitespace", "\ufeff \tfake-token\t \n", " \tfake-token\t ", 1},
		{"embedded line break", "fake\r\ntoken\r\n", "fake\r\ntoken", 0},
		{"empty input", "", "", 0},
		{"only unicode BOM", "\ufeff\r\n", "", 1},
		{"only mojibake BOM", "\u00ef\u00bb\u00bf\r\n", "", 1},
		{"only repeated unicode BOMs", "\ufeff\ufeff\ufeff\r\n", "", 3},
		{"only repeated mojibake BOMs", "\u00ef\u00bb\u00bf\u00ef\u00bb\u00bf\u00ef\u00bb\u00bf\r\n", "", 3},
		{"only mixed BOMs", "\u00ef\u00bb\u00bf\ufeff\u00ef\u00bb\u00bf\r\n", "", 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("CFO_HOME", "")
			t.Setenv("CFO_STATE_OVERRIDE", "")
			storeDir := useFileStore(t)
			input, err := os.CreateTemp(t.TempDir(), "stdin")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { input.Close() })
			if _, err := input.WriteString(test.input); err != nil {
				t.Fatal(err)
			}
			if _, err := input.Seek(0, 0); err != nil {
				t.Fatal(err)
			}
			originalStdin := os.Stdin
			os.Stdin = input
			t.Cleanup(func() { os.Stdin = originalStdin })

			code, stdout, stderr := runCLI(t, "store", "FAKE_TOKEN")

			if test.removedBOMCount > 0 {
				notice := fmt.Sprintf("cfo auth store: leading byte-order marks removed from stdin: %d\n", test.removedBOMCount)
				if !strings.HasPrefix(stdout, notice) || strings.Count(stdout, "byte-order") != 1 {
					t.Error("BOM removal count does not match the input")
				}
			} else if strings.Contains(stdout, "byte-order") {
				t.Error("auth store reported BOM removal for unmarked input")
			}
			if test.want == "" {
				if code != 2 || strings.Contains(stdout, "stored") {
					t.Error("auth store did not refuse empty content")
				}
				if _, err := os.Stat(filepath.Join(storeDir, "FAKE_TOKEN")); !os.IsNotExist(err) {
					t.Error("auth store wrote empty content")
				}
				if stderr != "cfo auth store: refusing to store an empty value\n" {
					t.Error("empty content diagnostics do not match the input")
				}
				return
			}
			if code != 0 {
				t.Fatalf("auth store failed with code %d", code)
			}
			stored, err := os.ReadFile(filepath.Join(storeDir, "FAKE_TOKEN"))
			if err != nil {
				t.Fatal(err)
			}
			if string(stored) != test.want {
				t.Error("stored value differs from the intended content")
			}
			if stderr != "" {
				t.Error("auth store emitted an error for valid input")
			}
			if strings.Contains(stdout+stderr, test.want) {
				t.Error("auth store disclosed the value")
			}
		})
	}
}

func TestAuthStorePreservesBOMInArgument(t *testing.T) {
	for _, prefix := range []string{"\ufeff", "\u00ef\u00bb\u00bf"} {
		t.Run(prefix, func(t *testing.T) {
			storeDir := useFileStore(t)
			value := prefix + "fake-token"

			code, _, stderr := runCLI(t, "store", "FAKE_TOKEN", value)

			if code != 0 || stderr != "" {
				t.Fatalf("auth store argument failed with code %d or unexpected diagnostics", code)
			}
			stored, err := os.ReadFile(filepath.Join(storeDir, "FAKE_TOKEN"))
			if err != nil {
				t.Fatal(err)
			}
			if string(stored) != value {
				t.Error("auth store changed the argument value")
			}
		})
	}
}
