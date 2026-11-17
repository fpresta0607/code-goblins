package proc

import (
	"errors"
	"os"
	"strings"
	"time"
)

// Entry is one hop in a process ancestry chain.
type Entry struct {
	PID       int
	ParentPID int
	// Group is the process group it is in, off Windows, where a tree of
	// processes is found by it (execx.StartTree). Windows leaves it 0.
	Group   int
	ExeBase string
	Start   time.Time
}

// Self returns the current process's PID.
func Self() int {
	return os.Getpid()
}

// Owner is whose a process is on this machine: the Windows user it runs as,
// by SID, and the session it runs in. Two programs one person runs at one
// desktop share both, and a program of another user, of a service or of
// another sign-in differs in one of them.
type Owner struct {
	User    string
	Session uint32
}

// Identity is what a process is, read through one handle: the program image
// it runs now, which follows a rename of its file, the arguments it was
// started with, its working directory and its environment.
type Identity struct {
	Image       string
	Arguments   []string
	Directory   string
	Environment []string
}

// Getenv is the value of name in the identity's environment, empty when it
// is not set.
func (i Identity) Getenv(name string) string {
	for _, entry := range i.Environment {
		if key, value, ok := strings.Cut(entry, "="); ok && strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

// ErrDirectoryUnreadable reports that a process's working directory could not
// be read: it exited mid-walk, or it runs at a privilege this process cannot
// open. Callers report the process without its directory rather than dropping
// it, because a listening port with an unknown directory is still a finding.
var ErrDirectoryUnreadable = errors.New("proc: working directory is unreadable")

// ErrCommandLineUnreadable reports that a process's command line could not
// be read, for the same reasons as its working directory.
var ErrCommandLineUnreadable = errors.New("proc: command line is unreadable")
