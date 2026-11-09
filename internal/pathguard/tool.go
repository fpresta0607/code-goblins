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

// Reaches reports whether a call of tool with input names anything the
// boundary reads: a file tool's path, or in a shell command a path, a
// redirection, a program that writes the files it is given, one that moves
// the shell or one that reads Credential Manager. A call that names none is
// let through without building the boundary, so an ordinary command costs
// no file read.
func Reaches(tool string, input json.RawMessage) bool {
	var arguments struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		Path         string `json:"path"`
		Pattern      string `json:"pattern"`
		Command      string `json:"command"`
	}
	if len(input) > 0 && json.Unmarshal(input, &arguments) != nil {
		return true
	}
	switch tool {
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit", "Glob", "Grep", "read", "write", "edit", "grep", "find", "ls":
		return arguments.FilePath != "" || arguments.NotebookPath != "" || arguments.Path != "" || (tool == "Glob" && globBase(arguments.Pattern) != "")
	case "Bash", "bash", "PowerShell", "Monitor":
	default:
		return false
	}
	if credentialPrograms.MatchString(arguments.Command) {
		return true
	}
	for _, segment := range shellSegments(arguments.Command) {
		if verb := segmentVerb(segment); writesEveryArgument[verb] || writesLastArgument[verb] || movesShell[verb] {
			return true
		}
		for _, word := range segment {
			if word.isRedirect || word.pathValue() != "" {
				return true
			}
		}
	}
	return false
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
