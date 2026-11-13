package spawn

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// cmdProgram runs the script at path with args through cmd /c, which exits
// with it. cmd reads its command line itself, so an argument it would
// interpret is refused. A path with a space in it is quoted on that line, and
// once another argument is quoted too, cmd drops the path's opening quote and
// runs the path's first word instead: such a path runs by its short name, and
// one on a volume that keeps no short names is refused.
func cmdProgram(path string, args ...string) ([]string, error) {
	if strings.ContainsAny(path, " \t") {
		long, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return nil, fmt.Errorf("spawn: %s: %w", path, err)
		}
		buffer := make([]uint16, windows.MAX_LONG_PATH)
		length, err := windows.GetShortPathName(long, &buffer[0], uint32(len(buffer)))
		if err != nil {
			return nil, fmt.Errorf("spawn: read the short name of %s: %w", path, err)
		}
		short := windows.UTF16ToString(buffer[:length])
		if strings.ContainsAny(short, " \t") {
			return nil, fmt.Errorf("spawn: %s has a space in its path and no short name without one, so cmd /c cannot start it by its path", path)
		}
		path = short
	}
	for _, arg := range append([]string{path}, args...) {
		if arg == "" || strings.ContainsAny(arg, "\"&|<>^%!()\r\n") {
			return nil, fmt.Errorf("spawn: %s's argument %q is one cmd would read as more than text", path, arg)
		}
	}
	shell := os.Getenv("ComSpec")
	if shell == "" {
		shell = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	return append([]string{shell, "/c", path}, args...), nil
}

// UserEnvironment is the environment Windows gives a new process of this
// user: the user's and the machine's configured variables, and nothing this
// process inherited from whatever runs it.
func UserEnvironment() ([]string, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &token); err != nil {
		return nil, err
	}
	defer token.Close()
	return token.Environ(false)
}
