package main

import (
	"log"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// keepOutput keeps what the window says where it can be read once it has
// ended: started from Explorer, the Start menu or at login it has no standard
// error, so its standard error and output, and with them its log, Wails' own
// fatal messages and the words of a crash, go to window.log in the fleet's
// state folder, beside the supervisor's serve.log. A window goblins starts
// already writes there and keeps its own. Its first line says which window
// it is, so a later reader can tell one window's end from the next.
func keepOutput(stateDir string) error {
	if handle, err := windows.GetStdHandle(windows.STD_ERROR_HANDLE); err != nil || handle == 0 || handle == windows.InvalidHandle {
		file, err := fsx.OpenAppend(filepath.Join(stateDir, "window.log"), 0o600)
		if err != nil {
			return err
		}
		// The runtime writes a crash to the process's standard error handle,
		// not to os.Stderr, so both point at the file.
		for _, standard := range []uint32{windows.STD_ERROR_HANDLE, windows.STD_OUTPUT_HANDLE} {
			if err := windows.SetStdHandle(standard, windows.Handle(file.Fd())); err != nil {
				return err
			}
		}
		os.Stderr, os.Stdout = file, file
		log.SetOutput(file)
	}
	log.Printf("window started, pid %d", os.Getpid())
	return nil
}
