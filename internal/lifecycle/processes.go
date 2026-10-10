package lifecycle

import (
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

type Identity struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
}

// UnfinishedStop is a stop that ended the task's terminal, which ends its
// harness and the job under it and frees what the goblin held, while the
// rest of the stop did not finish: the sweep for the task's other processes,
// which on a machine short of memory runs out of time, or the read of its
// gate's state, which waits on a busy no-mistakes daemon.
type UnfinishedStop struct{ Err error }

func (e UnfinishedStop) Error() string {
	return "its terminal ended, but the rest of its stop did not finish: " + e.Err.Error()
}

func (e UnfinishedStop) Unwrap() error { return e.Err }

type Process struct {
	PID       int
	ParentPID int
	Name      string
	Started   time.Time
	Directory string
	Arguments []string
	// Mark is the terminal the process's environment names with the digest
	// of the proof value it carries for it, empty when it carries none or
	// its environment could not be read.
	Mark Mark
	// IsGateAgent says the process's environment is a gate agent's.
	IsGateAgent bool
	// HasWindow says the process shows a window on the desktop.
	HasWindow bool
}

// Mark names a terminal and the digest of a proof value one of its hosts put
// in the terminal's environment (host.Proofs). Every process started in the
// terminal inherits the value and keeps it when its parent exits, and when
// Git Bash starts it outside the terminal's job, so the mark is what still
// ties a detached process to its owner. A program started by an MSYS program
// that is one itself, as Git Bash's own tools are, has no Windows
// environment to carry it: such a process is the task's by its working
// directory, or as the child of a process that is.
type Mark struct {
	Terminal string
	ProofSum string
}

// OwnedProcesses are the processes a task's teardown ends. A process is the
// task's own by any of four things. Its terminal's job, which holds what the
// harness started itself. Its terminal's mark, which it carries wherever it
// works and whatever became of its parent. Its place, at work in the task's
// directories or running a program from them. Or its parent, when that is
// the task's own and started before it.
//
// Three kinds of process are never the task's by its mark or its parent,
// whatever started them. A machine service the goblin started and what runs
// under it, since it serves the whole machine: Docker Desktop started from a
// worktree runs there and in that job, so does a no-mistakes daemon a
// goblin's gate started, with every other gate's agents under it, and so
// does the Scrawl server every goblin's page is served by. A daemon's agent
// at work in the task's own directories, its own gate run's, is still the
// task's. A gate's agent, which carries the mark of whichever goblin started
// the daemon while it works for any gate. And a desktop program with what it
// started (desktopPrograms), which is the Overlord's to close.
func OwnedProcesses(processes []Process, directories []string, job []Identity, marks []Mark) []Process {
	running := make([]proc.ServiceProcess, 0, len(processes))
	byPID := make(map[int]Process, len(processes))
	for _, process := range processes {
		running = append(running, proc.ServiceProcess{PID: process.PID, ParentPID: process.ParentPID, ExeBase: process.Name, Arguments: process.Arguments, Start: process.Started})
		byPID[process.PID] = process
	}
	services := proc.ServicesOf(running)
	desktop := desktopPrograms(processes)
	isOwned := map[int]bool{}
	canFollow := map[int]bool{}
	for _, process := range processes {
		if process.PID <= 0 || process.Started.IsZero() {
			continue
		}
		isJobMember := false
		for _, identity := range job {
			if identity.PID == process.PID && identity.Started.Equal(process.Started) {
				isJobMember = true
				break
			}
		}
		isClaimed := false
		for _, directory := range directories {
			if !filepath.IsAbs(directory) || filepath.Dir(filepath.Clean(directory)) == filepath.Clean(directory) {
				continue
			}
			if withinDirectory(process.Directory, directory) {
				isClaimed = true
			}
			name := strings.TrimSuffix(strings.ToLower(process.Name), ".exe")
			isRuntime := name == "node" || strings.HasPrefix(name, "python") || name == "pwsh" || name == "powershell" || name == "bash" || name == "sh" || name == "go" || name == "cfo"
			isBrowser := name == "chrome" || name == "msedge"
			for index, argument := range process.Arguments {
				flag, value, hasValue := strings.Cut(argument, "=")
				if !hasValue && index > 0 && strings.HasPrefix(process.Arguments[index-1], "--") {
					flag, value, hasValue = process.Arguments[index-1], argument, true
				}
				isTaskArgument := index == 0
				if hasValue {
					isTaskArgument = isBrowser && flag == "--user-data-dir" || isRuntime && (flag == "--cwd" || flag == "--dir" || flag == "--prefix")
					argument = value
				} else if isRuntime {
					switch strings.ToLower(filepath.Ext(argument)) {
					case ".js", ".mjs", ".cjs", ".ts", ".py", ".ps1", ".sh", ".exe", ".cmd", ".bat":
						isTaskArgument = true
					}
				}
				if isTaskArgument && withinDirectory(argument, directory) {
					isClaimed = true
				}
			}
		}
		service := services[process.PID]
		isOwnGateAgent := service == proc.GateDaemon && isClaimed && proc.ServiceOf(process.Name, process.Arguments) != proc.GateDaemon
		if service != proc.NoService && !isOwnGateAgent {
			continue
		}
		isMarked := !process.IsGateAgent && process.Mark != (Mark{}) && slices.Contains(marks, process.Mark)
		canFollow[process.PID] = !desktop[process.PID]
		isOwned[process.PID] = isJobMember || isClaimed || isMarked && canFollow[process.PID]
	}
	// A process whose parent is the task's own is the task's too. Each pass
	// reaches one generation further down.
	for isGrowing := true; isGrowing; {
		isGrowing = false
		for _, process := range processes {
			parent, hasParent := byPID[process.ParentPID]
			if isOwned[process.PID] || !canFollow[process.PID] || !hasParent || !isOwned[parent.PID] || process.Started.Before(parent.Started) {
				continue
			}
			isOwned[process.PID] = true
			isGrowing = true
		}
	}
	var owned []Process
	for _, process := range processes {
		if isOwned[process.PID] {
			owned = append(owned, process)
		}
	}
	return owned
}

// desktopBrowsers are the browsers a person uses. One started with none of
// automationFlags runs on that person's own profile, in their own windows.
var desktopBrowsers = []string{"chrome", "msedge", "firefox", "brave", "chromium", "opera", "vivaldi"}

// automationFlags are the arguments a tool starts a browser with to drive
// it: such a browser is the tool's, on a profile of its own.
var automationFlags = []string{"--headless", "-headless", "--remote-debugging-pipe", "--remote-debugging-port", "--enable-automation", "--marionette"}

// desktopPrograms are the programs a person uses, with everything under
// them: a program that shows a window, a browser no tool drives, a packaged
// desktop app, and Explorer. A goblin can start one for the Overlord, as
// when a sign-in opens his browser: it then carries the goblin's mark and is
// the goblin's child, and it is still his to close. A browser a tool drives
// is no such program even while it shows a window.
func desktopPrograms(processes []Process) map[int]bool {
	children := make(map[int][]Process, len(processes))
	for _, process := range processes {
		children[process.ParentPID] = append(children[process.ParentPID], process)
	}
	desktop := map[int]bool{}
	var queue []Process
	for _, process := range processes {
		if isDesktopProgram(process) {
			desktop[process.PID] = true
			queue = append(queue, process)
		}
	}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		for _, child := range children[parent.PID] {
			if desktop[child.PID] || child.PID == parent.PID || child.Started.Before(parent.Started) {
				continue
			}
			desktop[child.PID] = true
			queue = append(queue, child)
		}
	}
	return desktop
}

func isDesktopProgram(process Process) bool {
	name := strings.TrimSuffix(strings.ToLower(process.Name), ".exe")
	if slices.Contains(desktopBrowsers, name) {
		for _, argument := range process.Arguments {
			flag, _, _ := strings.Cut(argument, "=")
			// A browser's own child processes follow the browser above them.
			if flag == "--type" || flag == "-contentproc" || slices.Contains(automationFlags, flag) {
				return false
			}
		}
		return true
	}
	if process.HasWindow || name == "explorer" {
		return true
	}
	return len(process.Arguments) > 0 && strings.Contains(strings.ToLower(process.Arguments[0]), `\windowsapps\`)
}

func withinDirectory(path, directory string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	path, directory = strings.ToLower(filepath.Clean(path)), strings.ToLower(filepath.Clean(directory))
	return path == directory || strings.HasPrefix(path, directory+string(filepath.Separator))
}
