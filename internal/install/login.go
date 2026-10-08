package install

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Start at login is on unless the person turned it off: in the setup, with
// cfo install --start-at-login off, on the board or in the tray. Their choice
// is kept in the home, so an install or update never turns back on what they
// turned off.
const (
	StartAtLoginOn  = "on"
	StartAtLoginOff = "off"
)

// startAtLoginChoice is the file in a home's state folder that holds the
// person's choice of Start at login.
const startAtLoginChoice = "start-at-login"

// ReadStartAtLogin reads the choice of Start at login kept in stateDir: on,
// off, or empty where none was made, which is on.
func ReadStartAtLogin(stateDir string) (string, error) {
	data, err := fsx.ReadFile(filepath.Join(stateDir, startAtLoginChoice))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read the choice of Start at login: %w", err)
	}
	switch choice := strings.TrimSpace(string(data)); choice {
	case StartAtLoginOn, StartAtLoginOff:
		return choice, nil
	default:
		return "", fmt.Errorf("the choice of Start at login in %s is %q, not on or off", stateDir, choice)
	}
}

// WriteStartAtLogin keeps the choice of Start at login in stateDir.
func WriteStartAtLogin(stateDir, choice string) error {
	if choice != StartAtLoginOn && choice != StartAtLoginOff {
		return fmt.Errorf("Start at login is on or off, not %q", choice)
	}
	return fsx.AtomicWriteFile(filepath.Join(stateDir, startAtLoginChoice), []byte(choice+"\n"))
}

// startAtLoginCommand is what Windows runs at login for this home: the
// desktop window alone, in the tray, which runs the goblins beside it out of
// sight, so the supervisor starts with no terminal shown and brings back what
// the restart ended. For a window the home only kept, which may be from
// before a window started alone opened the app, it is goblins with --window,
// which opens any window.
func (s Service) startAtLoginCommand() string {
	commands := s.startAtLoginCommands()
	if s.suppliesWindow() {
		return commands[0]
	}
	return commands[1]
}

func (s Service) startAtLoginCommands() [2]string {
	return [2]string{
		`"` + filepath.Join(s.bin(), windowName) + `" --background`,
		`"` + filepath.Join(s.bin(), "goblins.exe") + `" --window --background`,
	}
}
