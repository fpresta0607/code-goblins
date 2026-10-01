package siqspeak

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestMutexExistsMapsOpenMutexResults(t *testing.T) {
	for _, test := range []struct {
		name      string
		err       error
		isRunning bool
		isFailure bool
	}{
		{name: "opened", isRunning: true},
		{name: "access denied", err: windows.ERROR_ACCESS_DENIED, isRunning: true},
		{name: "not found", err: windows.ERROR_FILE_NOT_FOUND},
		{name: "other failure", err: windows.ERROR_INVALID_HANDLE, isFailure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			isRunning, err := mutexExists(test.err)

			if isRunning != test.isRunning || (err != nil) != test.isFailure {
				t.Fatalf("mutexExists(%v) = %v, %v", test.err, isRunning, err)
			}
		})
	}
}
