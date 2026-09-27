package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// The board's view of a pane attaches as an observer at the pane's size, or
// as a controller that sizes the pane and takes it over from any other
// client, since the most recent client to take control wins.
func TestTerminalSessionArgsTakeOverOnlyForControl(t *testing.T) {
	cases := []struct {
		name    string
		control bool
		want    []string
	}{
		{"an observer changes nothing", false, []string{"--session", "cfo", "terminal", "session", "observe", "term_1", "--cols", "120", "--rows", "40"}},
		{"a controller sizes the pane and takes it over", true, []string{"--session", "cfo", "terminal", "session", "control", "term_1", "--cols", "120", "--rows", "40", "--takeover"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := terminalSessionArgs("cfo", "term_1", c.control, 120, 40); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("args = %q, want %q", got, c.want)
			}
		})
	}
}

// A controller the board closes is still delivering the size it handed the
// pane back; killing it at once lost that size, so the pane kept the board's.
func TestClosingATerminalLetsItDeliverWhatItWasSent(t *testing.T) {
	// Arrange
	record := filepath.Join(t.TempDir(), "received")
	process, err := startTerminal(context.Background(), os.Args[0], "-test.run=^TestTerminalHelperProcess$", "--", record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := process.Next(); err != nil {
		t.Fatalf("the process never drew its first screen: %v", err)
	}
	sent := TerminalCommand{Type: "terminal.resize", Cols: 132, Rows: 43}

	// Act
	if err := process.Send(sent); err != nil {
		t.Fatal(err)
	}
	_ = process.Close()

	// Assert
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("the process was killed before it delivered what it was sent: %v", err)
	}
	var got TerminalCommand
	if err := json.Unmarshal(data, &got); err != nil || got != sent {
		t.Fatalf("the process delivered %q, want %+v", data, sent)
	}
}

// TestTerminalHelperProcess stands in for the Herdr CLI when a test runs it
// with a file after "--": it draws a first screen, then takes a moment to
// deliver the first command it reads, recording it in that file, and exits.
func TestTerminalHelperProcess(t *testing.T) {
	record := ""
	for index, arg := range os.Args {
		if arg == "--" && index+1 < len(os.Args) {
			record = os.Args[index+1]
		}
	}
	if record == "" {
		return
	}
	fmt.Println(`{"type":"terminal.frame","seq":1,"encoding":"ansi","width":80,"height":24,"full":true,"bytes":""}`)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	time.Sleep(300 * time.Millisecond)
	_ = os.WriteFile(record, []byte(line), 0o600)
	os.Exit(0)
}
