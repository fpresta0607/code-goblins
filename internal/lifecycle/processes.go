package lifecycle

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

type Identity struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
}

// UnfinishedSweep is a stop that ended the task's terminal, which ends its
// harness and the job under it and frees what the goblin held, while the
// sweep for the task's other processes did not finish, as on a machine so
// short of memory that reading its processes runs out of time.
type UnfinishedSweep struct{ Err error }

func (e UnfinishedSweep) Error() string {
	return "its terminal ended, but the sweep for its other processes did not finish: " + e.Err.Error()
}

func (e UnfinishedSweep) Unwrap() error { return e.Err }

type Process struct {
	PID       int
	ParentPID int
	Name      string
	Started   time.Time
	Directory string
	Arguments []string
}

// OwnedProcesses are the processes a task's teardown ends: those in its
// terminal's job, and those at work in its directories or running a program
// from them. A machine service the goblin started is never among them, nor
// what runs under it, since it serves the whole machine: Docker Desktop
// started from a worktree runs there and in that job, and so does a
// no-mistakes daemon a goblin's gate started, with every other gate's agents
// under it. A daemon's agent at work in the task's own directories, its own
// gate run's, is still the task's.
func OwnedProcesses(processes []Process, directories []string, job []Identity) []Process {
	running := make([]proc.ServiceProcess, 0, len(processes))
	for _, process := range processes {
		running = append(running, proc.ServiceProcess{PID: process.PID, ParentPID: process.ParentPID, ExeBase: process.Name, Arguments: process.Arguments, Start: process.Started})
	}
	services := proc.ServicesOf(running)
	var owned []Process
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
		isService := service == proc.DockerDesktop || service == proc.GateDaemon && (proc.ServiceOf(process.Name, process.Arguments) == proc.GateDaemon || !isClaimed)
		if (isJobMember || isClaimed) && !isService {
			owned = append(owned, process)
		}
	}
	return owned
}

func withinDirectory(path, directory string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	path, directory = strings.ToLower(filepath.Clean(path)), strings.ToLower(filepath.Clean(directory))
	return path == directory || strings.HasPrefix(path, directory+string(filepath.Separator))
}
