package host

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/awake"
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
	Rows       []string `json:"rows,omitempty"`
	Error      string   `json:"error,omitempty"`
	Submission string   `json:"submission,omitempty"`
	HasStarted bool     `json:"started,omitempty"`
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
	answer, err := readScreen(record, hello{Version: Version, Token: record.Token, Screen: true})
	return answer.Rows, err
}

// Screen reads this viewer's output screen and latched turn evidence under
// the same lock, so a later console screen cannot overtake earlier output.
func (c *Client) Screen(record Record, submission string) (rows []string, observed string, hasStarted bool, err error) {
	if c.view == 0 {
		return nil, "", false, errors.New("host: restart this terminal to read its output screen")
	}
	answer, err := readScreen(record, hello{Version: Version, Token: record.Token, Screen: true, View: c.view, Submission: submission})
	return answer.Rows, answer.Submission, answer.HasStarted, err
}

func readScreen(record Record, greeting hello) (screen, error) {
	client, _, err := dial(record, greeting)
	if err != nil {
		return screen{}, err
	}
	defer client.Close()
	lift := awake.Deadline(client.pipe.SetReadDeadline, screenTimeout+handshakeTimeout)
	kind, payload, err := readFrame(client.pipe)
	lift()
	if err != nil {
		return screen{}, err
	}
	if kind != frameScreen {
		return screen{}, fmt.Errorf("its host answered with frame %q, not a screen; a host started by an older cfo cannot read screens", kind)
	}
	var answer screen
	if err := json.Unmarshal(payload, &answer); err != nil {
		return screen{}, err
	}
	if answer.Error != "" {
		return screen{}, errors.New(answer.Error)
	}
	return answer, nil
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
func serveScreen(connection *os.File, console *conpty.Console, screens *sync.RWMutex, output *history, request hello) {
	if writeHello(connection, hello{Version: Version}) != nil {
		return
	}
	var answer screen
	if request.View != 0 {
		output.mu.Lock()
		answer.Error = "the terminal view has ended"
		for viewer := range output.viewers {
			if viewer.id == request.View {
				answer = screen{Rows: output.screen.Rows()}
				if request.Submission == viewer.submission {
					answer.Submission, answer.HasStarted = viewer.submission, viewer.hasStarted
				}
				break
			}
		}
		output.mu.Unlock()
	} else {
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
	}
	payload, err := json.Marshal(answer)
	if err != nil {
		return
	}
	_ = writeFrame(connection, frameScreen, payload)
}
