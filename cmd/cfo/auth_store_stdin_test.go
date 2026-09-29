package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthStorePreservesStdinExceptLeadingBOMAndTrailingLineBreaks(t *testing.T) {
	for _, test := range []struct {
		name         string
		input        string
		want         string
		isBOMRemoved bool
	}{
		{"unicode BOM", "\ufefffake-token\r\n", "fake-token", true},
		{"mojibake BOM", "\u00ef\u00bb\u00bffake-token\r\n", "fake-token", true},
		{"no BOM", "fake-token\r\n", "fake-token", false},
		{"no line break", "fake-token", "fake-token", false},
		{"LF line break", "fake-token\n", "fake-token", false},
		{"spaces and tabs", " \tfake-token\t \r\n", " \tfake-token\t ", false},
		{"unicode neighbour", "\ufefefake-token\n", "\ufefefake-token", false},
		{"mojibake neighbour", "\u00ef\u00bb\u00befake-token\n", "\u00ef\u00bb\u00befake-token", false},
		{"partial mojibake", "\u00ef\u00bbfake-token\n", "\u00ef\u00bbfake-token", false},
		{"embedded BOMs", "fake-\ufeff\u00ef\u00bb\u00bftoken\n", "fake-\ufeff\u00ef\u00bb\u00bftoken", false},
		{"BOM after space", " \ufefffake-token\n", " \ufefffake-token", false},
		{"repeated BOMs", "\ufeff\ufefffake-token\r\n", "fake-token", true},
		{"repeated mojibake BOMs", "\u00ef\u00bb\u00bf\u00ef\u00bb\u00bffake-token\n", "fake-token", true},
		{"mixed BOMs", "\ufeff\u00ef\u00bb\u00bffake-token\n", "fake-token", true},
		{"BOM with whitespace", "\ufeff \tfake-token\t \n", " \tfake-token\t ", true},
		{"embedded line break", "fake\r\ntoken\r\n", "fake\r\ntoken", false},
		{"empty input", "", "", false},
		{"only unicode BOM", "\ufeff\r\n", "", true},
		{"only mojibake BOM", "\u00ef\u00bb\u00bf\r\n", "", true},
		{"only repeated BOMs", "\ufeff\ufeff\r\n", "", true},
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

			notice := "cfo auth store: removed a leading byte-order mark from stdin\n"
			if strings.Contains(stdout, notice) != test.isBOMRemoved {
				t.Error("BOM removal notice does not match the input")
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
