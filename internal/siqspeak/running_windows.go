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
	if err == nil {
		windows.CloseHandle(handle)
	}
	return mutexExists(err)
}

func mutexExists(err error) (bool, error) {
	switch {
	case err == nil, errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return true, nil
	case errors.Is(err, windows.ERROR_FILE_NOT_FOUND):
		return false, nil
	}
	return false, err
}
