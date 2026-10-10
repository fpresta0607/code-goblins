package proc

import (
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// This process is its own user's, in its own session, and Windows says both
// of it another way: the user of its own token and the session of its PID.
func TestOwnerOfReadsThisProcesssUserAndSession(t *testing.T) {
	// Arrange
	start, ok := StartTime(os.Getpid())
	if !ok {
		t.Fatal("this process's start time could not be read")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	var session uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &session); err != nil {
		t.Fatal(err)
	}

	// Act
	owner, err := OwnerOf(os.Getpid(), start)

	// Assert
	if err != nil || owner.User != user.User.Sid.String() || owner.Session != session {
		t.Errorf("OwnerOf = %+v, %v, want user %s in session %d", owner, err, user.User.Sid, session)
	}
}

// A PID names another program once its process has exited, so an owner is
// read only of the process that was created when its caller says.
func TestOwnerOfRefusesAProcessCreatedAtAnotherTime(t *testing.T) {
	// Arrange
	start, ok := StartTime(os.Getpid())
	if !ok {
		t.Fatal("this process's start time could not be read")
	}

	// Act
	_, reused := OwnerOf(os.Getpid(), start.Add(-time.Hour))
	_, gone := OwnerOf(-1, start)

	// Assert
	if reused == nil || !strings.Contains(reused.Error(), "so it is another program") {
		t.Errorf("OwnerOf at another start time = %v, want it refused as another program", reused)
	}
	if gone == nil {
		t.Error("OwnerOf read an owner of a process that is not there")
	}
}
