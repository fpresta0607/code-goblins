package lifecycle

import (
	"errors"
	"testing"
)

func TestAnUnreadableOrEmptyWindowListingShowsEveryProcessAWindow(t *testing.T) {
	testCases := []struct {
		name         string
		windowOwners map[int]bool
		windowsErr   error
		pid          int
		want         bool
	}{
		{name: "an empty listing protects any process", windowOwners: map[int]bool{}, pid: 4242, want: true},
		{name: "no listing protects any process", windowOwners: nil, pid: 4242, want: true},
		{name: "a failed listing protects any process", windowsErr: errors.New("proc: list the desktop's windows: access denied"), pid: 4242, want: true},
		{name: "a listing shows its owner a window", windowOwners: map[int]bool{100: true}, pid: 100, want: true},
		{name: "a listing shows no window to a process it leaves out", windowOwners: map[int]bool{100: true}, pid: 4242, want: false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := showsWindow(testCase.windowOwners, testCase.windowsErr, testCase.pid)

			if got != testCase.want {
				t.Fatalf("showsWindow(%v, %v, %d) = %t, want %t", testCase.windowOwners, testCase.windowsErr, testCase.pid, got, testCase.want)
			}
		})
	}
}
