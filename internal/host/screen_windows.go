package host

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/conpty"
)

// screenTimeout bounds one read of a terminal's screen.
var screenTimeout = 10 * time.Second

// holdScreenRead runs as a read holds the terminal's screens, before it asks
// for the screen, so a test can act on the terminal while a read is in
// flight.
var holdScreenRead = func() {}

// screen is a screen frame's payload: the rows of the terminal's window, or
// why they could not be read.
type screen struct {
	Rows  []string `json:"rows,omitempty"`
	Error string   `json:"error,omitempty"`
}

// ReadScreen reads the screen of the terminal record's host runs, as the
// terminal's console holds it: one string per row of its window, each as wide
// as the window. A read that fails is an error naming the terminal, never an
// empty screen.
func ReadScreen(record Record) ([]string, error) {
	rows, err := requestScreen(record)
	if err != nil {
		return nil, fmt.Errorf("host: read the screen of terminal %s: %w", record.ID, err)
	}
	return rows, nil
}

func requestScreen(record Record) ([]string, error) {
	client, _, err := dial(record, hello{Version: Version, Token: record.Token, Screen: true})
	if err != nil {
		return nil, err
	}
	defer client.Close()
	_ = client.pipe.SetReadDeadline(time.Now().Add(screenTimeout + handshakeTimeout))
	kind, payload, err := readFrame(client.pipe)
	if err != nil {
		return nil, err
	}
	if kind != frameScreen {
		return nil, fmt.Errorf("its host answered with frame %q, not a screen; a host started by an older cfo cannot read screens", kind)
	}
	var answer screen
	if err := json.Unmarshal(payload, &answer); err != nil {
		return nil, err
	}
	if answer.Error != "" {
		return nil, errors.New(answer.Error)
	}
	return answer.Rows, nil
}

// ScreenTail is a screen's last lines rows, or all of them for lines 0 or
// less, without the trailing blanks of each row or the blank rows below the
// last one written.
func ScreenTail(screen []string, lines int) string {
	rows := make([]string, len(screen))
	for i, row := range screen {
		rows[i] = strings.TrimRight(row, " ")
	}
	for len(rows) > 0 && rows[len(rows)-1] == "" {
		rows = rows[:len(rows)-1]
	}
	if lines > 0 && lines < len(rows) {
		rows = rows[len(rows)-lines:]
	}
	if len(rows) == 0 {
		return ""
	}
	return strings.Join(rows, "\n") + "\n"
}

// serveScreen answers a screen request once its token is checked: the screen
// of the terminal's console, or why it could not be read. It reads while
// holding screens shared, and never once the terminal's program has ended,
// since its console closes with it.
func serveScreen(connection *os.File, console *conpty.Console, screens *sync.RWMutex) {
	if writeHello(connection, hello{Version: Version}) != nil {
		return
	}
	var answer screen
	screens.RLock()
	select {
	case <-console.Done():
		answer.Error = "the terminal has ended"
	default:
		holdScreenRead()
		rows, err := console.Screen(screenTimeout)
		answer.Rows = rows
		if err != nil {
			answer = screen{Error: err.Error()}
		}
	}
	screens.RUnlock()
	payload, err := json.Marshal(answer)
	if err != nil {
		return
	}
	_ = writeFrame(connection, frameScreen, payload)
}
