package pathguard

import (
	"encoding/json"
	"strings"
)

// ToolRefusal is what a session is told of a call of tool with input, the
// tool's arguments as its harness hands them to a hook, which is refused
// before it runs, or empty when it may go on. The file tools, Claude Code's
// and pi's, are read by the paths they name, and a shell tool, Bash, PowerShell, Monitor or pi's bash,
// by its command (ShellRefusal). A tool that names no path goes on.
func (b Boundary) ToolRefusal(tool string, input json.RawMessage) string {
	var arguments struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		Path         string `json:"path"`
		Pattern      string `json:"pattern"`
		Command      string `json:"command"`
	}
	if len(input) > 0 && json.Unmarshal(input, &arguments) != nil {
		return refused(tool, "its arguments could not be read for the paths they name")
	}
	var reason string
	switch tool {
	case "Read":
		reason = b.Refusal(arguments.FilePath, false)
	case "Write", "Edit", "MultiEdit":
		reason = b.Refusal(arguments.FilePath, true)
	case "NotebookEdit":
		reason = b.Refusal(arguments.NotebookPath, true)
	case "read", "grep", "find", "ls":
		reason = b.Refusal(arguments.Path, false)
	case "write", "edit":
		reason = b.Refusal(arguments.Path, true)
	case "Glob", "Grep":
		reason = b.Refusal(arguments.Path, false)
		if base := globBase(arguments.Pattern); reason == "" && tool == "Glob" && base != "" {
			reason = b.Refusal(base, false)
			if reason == "" && arguments.Path != "" {
				reason = b.Refusal(arguments.Path+"/"+base, false)
			}
		}
	case "Bash", "bash", "PowerShell", "Monitor":
		reason = b.ShellRefusal(arguments.Command)
	}
	if reason == "" {
		return ""
	}
	return refused(tool, reason)
}

// refused is the refusal of one tool call for reason.
func refused(tool, reason string) string {
	return "[path-boundary] this " + tool + " call is refused before it ran, so it read and wrote nothing: " + reason + ". A goblin and a gate step never read the credential store or ~/.ssh, and write only inside their worktree, their scratch folder and the caches they were given."
}

// globBase is the part of a glob pattern before its first wildcard, the
// folder the pattern reads.
func globBase(pattern string) string {
	if index := strings.IndexAny(pattern, "*?[{"); index >= 0 {
		pattern = pattern[:index]
	}
	return pattern
}
