package install

import "path/filepath"

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
