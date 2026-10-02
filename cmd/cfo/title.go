package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// maxTaskTitle is the longest short title a task takes. The title names the
// task on the board and on its ticket, where teammates read it.
const maxTaskTitle = 100

// shortTitle is a task's short title as the CFO typed it, with its spacing
// tidied. A title of several lines or longer than maxTaskTitle is refused:
// that is a description, and a ticket's title must stay the few words the CFO
// chose for it.
func shortTitle(typed string) (string, error) {
	if strings.ContainsAny(typed, "\r\n") {
		return "", errors.New("a title is one line")
	}
	title := strings.Join(strings.Fields(typed), " ")
	if title == "" {
		return "", errors.New("the title is empty")
	}
	if utf8.RuneCountInString(title) > maxTaskTitle {
		return "", fmt.Errorf("a title is at most %d characters", maxTaskTitle)
	}
	return title, nil
}

// runTitle gives a running task its short title: the board shows it, and the
// supervisor writes it to the ticket it opened for the task.
func runTitle(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) != 2 {
		fmt.Fprintln(stderr, `usage: cfo title <id> "<short title>"`)
		return 2
	}
	id := args[0]
	title, err := shortTitle(args[1])
	if err != nil {
		fmt.Fprintf(stderr, "cfo title: %v\n", err)
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := state.ValidTaskID(id); err != nil {
		fmt.Fprintf(stderr, "cfo title: %v\n", err)
		return 1
	}
	if err := titleTask(h.State, id, title); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(stderr, "cfo title: %s is not a running task; a queued task takes its title from its backlog row or from cfo spawn --title\n", id)
			return 1
		}
		fmt.Fprintf(stderr, "cfo title: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "titled %s: %s\n", id, title)
	return 0
}

// titleTask writes the title into the task's record and leaves every other
// field of it as it is.
func titleTask(stateDir, id, title string) (err error) {
	metaPath := filepath.Join(stateDir, id+".meta")
	if _, err := os.Stat(metaPath); err != nil {
		return err
	}
	lockName := state.MetadataLockName(id)
	if _, err := lock.AcquireExclusiveNamed(stateDir, lockName); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(stateDir, lockName)) }()
	record, err := state.ReadMeta(metaPath)
	if err != nil {
		return err
	}
	record["title"] = title
	return state.WriteMeta(metaPath, record)
}
