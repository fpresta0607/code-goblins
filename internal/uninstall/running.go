package uninstall

import (
	"cmp"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/lifecycle"
)

// Running is one program running now.
type Running struct {
	lifecycle.Process
	// Image is the program it runs, its full path.
	Image string
}

// Terminal is a native terminal of the home, with what its host runs as and
// the marks and folders that tie a program to it.
type Terminal struct {
	ID          string
	HostPID     int
	Marks       []lifecycle.Mark
	Directories []string
}

// The kinds of program the uninstall stops, in the order it stops them: the
// window first, which would start the board again, then the board, which
// would start the terminals again, then the terminals, then what they left.
const (
	kindWindow = iota
	kindBoard
	kindTerminal
	kindProgram
)

// owned are the programs of rest that are this home's own, each with its
// kind and words: a program running from the home's folders, or from Code
// Goblins' other folders, and a program a terminal of the home started, by
// the terminal's mark, its folders or its parent, as a task's teardown judges
// it. Self, the uninstall itself, is never among them.
func owned(rest []Running, terminals []Terminal, folders []string, self int) []ownedProgram {
	var marks []lifecycle.Mark
	var directories []string
	hosts := map[int]string{}
	for _, terminal := range terminals {
		marks = append(marks, terminal.Marks...)
		directories = append(directories, terminal.Directories...)
		if terminal.HostPID != 0 {
			hosts[terminal.HostPID] = terminal.ID
		}
	}
	processes := make([]lifecycle.Process, 0, len(rest))
	for _, program := range rest {
		processes = append(processes, program.Process)
	}
	byTerminal := map[int]bool{}
	for _, process := range lifecycle.OwnedProcesses(processes, directories, nil, marks) {
		byTerminal[process.PID] = true
	}
	var programs []ownedProgram
	for _, program := range rest {
		if program.PID == self {
			continue
		}
		fromFolders := slices.ContainsFunc(folders, func(folder string) bool { return program.Image != "" && within(program.Image, folder) })
		if !fromFolders && !byTerminal[program.PID] {
			continue
		}
		programs = append(programs, classify(program, hosts, fromFolders))
	}
	slices.SortStableFunc(programs, func(a, b ownedProgram) int { return cmp.Compare(a.kind, b.kind) })
	return programs
}

// ownedProgram is a program the uninstall stops.
type ownedProgram struct {
	Running
	kind int
	what string
	// terminal is the native terminal a host runs.
	terminal string
}

func classify(program Running, hosts map[int]string, fromFolders bool) ownedProgram {
	name := strings.ToLower(filepath.Base(program.Image))
	if name == "." || name == "" {
		name = strings.ToLower(program.Name)
	}
	switch {
	case fromFolders && name == "goblins-window.exe":
		return ownedProgram{Running: program, kind: kindWindow, what: "the window"}
	case fromFolders && slices.Contains(program.Arguments, "serve"):
		return ownedProgram{Running: program, kind: kindBoard, what: "the board"}
	case hosts[program.PID] == "cfo":
		return ownedProgram{Running: program, kind: kindTerminal, what: "the CFO's terminal", terminal: "cfo"}
	case hosts[program.PID] != "":
		id := hosts[program.PID]
		return ownedProgram{Running: program, kind: kindTerminal, what: id + "'s terminal", terminal: id}
	case fromFolders && slices.Contains(program.Arguments, "host"):
		return ownedProgram{Running: program, kind: kindTerminal, what: "a terminal of Code Goblins"}
	case fromFolders:
		return ownedProgram{Running: program, kind: kindProgram, what: "a program of Code Goblins, " + name}
	}
	return ownedProgram{Running: program, kind: kindProgram, what: "a program a goblin started, " + name}
}

// within reports whether path is folder or under it, without regard to case,
// as Windows compares paths.
func within(path, folder string) bool {
	path, folder = filepath.Clean(path), filepath.Clean(folder)
	if strings.EqualFold(path, folder) {
		return true
	}
	return len(path) > len(folder) && strings.EqualFold(path[:len(folder)], folder) && (path[len(folder)] == '\\' || path[len(folder)] == '/' || strings.HasSuffix(folder, `\`))
}
