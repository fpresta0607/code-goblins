package siqspeak

import (
	"errors"

	"golang.org/x/sys/windows"
)

func running() (bool, error) {
	name, err := windows.UTF16PtrFromString(`Global\SIQspeak_SingleInstance_v1`)
	if err != nil {
		return false, err
	}
	// Open the app's existing singleton without acquiring or creating it.
	handle, err := windows.OpenMutex(windows.SYNCHRONIZE, false, name)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(handle)
	return true, nil
}
