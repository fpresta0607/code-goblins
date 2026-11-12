package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// endWithParent ends this process, which does an update's work, when the
// update that bounds it ends first, as when its terminal is closed: an update
// ended is not one that goes on out of sight with nothing bounding it. The
// journal keeps the way back, as for any update that ends part way.
func endWithParent() {
	parent, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(os.Getppid()))
	if err != nil {
		return
	}
	go func() {
		if event, err := windows.WaitForSingleObject(parent, windows.INFINITE); err == nil && event == windows.WAIT_OBJECT_0 {
			os.Exit(1)
		}
	}()
}
