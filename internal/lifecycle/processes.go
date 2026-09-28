package lifecycle

import (
	"path/filepath"
	"strings"
	"time"
)

type Identity struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
}

type Process struct {
	PID       int
	ParentPID int
	Name      string
	Started   time.Time
	Directory string
	Arguments []string
}

func OwnedProcesses(processes []Process, directories []string, job []Identity) []Process {
	var owned []Process
	for _, process := range processes {
		if process.PID <= 0 || process.Started.IsZero() {
			continue
		}
		isOwned := false
		for _, identity := range job {
			if identity.PID == process.PID && identity.Started.Equal(process.Started) {
				isOwned = true
				break
			}
		}
		for _, directory := range directories {
			if !filepath.IsAbs(directory) || filepath.Dir(filepath.Clean(directory)) == filepath.Clean(directory) {
				continue
			}
			if withinDirectory(process.Directory, directory) {
				isOwned = true
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
					isOwned = true
				}
			}
		}
		if isOwned {
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
