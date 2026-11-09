package sandbox

import (
	"path/filepath"
	"strings"
)

// hostPrograms are the programs a sandbox task's goblin runs on the host:
// cfo to report, gate and contain its work, and git and gh to deliver it.
// Everything else runs in its container, through cfo sandbox run.
var hostPrograms = map[string]bool{"cfo": true, "git": true, "gh": true}

// hostRefusedCommands are the cfo commands that run the project's own code
// on the host, which a sandbox task runs in its container instead: the gate
// checks this repository runs, the checks cfo verify runs before a push,
// the security tier cfo security runs, the build cfo deploy runs, and a
// local services stack, whose compose file the project writes.
var hostRefusedCommands = map[string]bool{"deploy": true, "gate": true, "security": true, "services": true, "verify": true}

// hostRefusedGitOptions are the options before git's command that have it
// read another repository, configuration or programs than the worktree's
// own, as a repository the container made in a folder of the worktree.
var hostRefusedGitOptions = map[string]bool{"-C": true, "-c": true, "--attr-source": true, "--config-env": true, "--exec-path": true, "--git-dir": true, "--work-tree": true}

// HostCommandRefusal is why command, a shell command a sandbox task's goblin
// is about to run on the host from cwd, is refused, or empty when it runs.
// Each of its commands, at ;, |, & and line breaks outside quotes, must
// start cfo, git or gh by name, or by an absolute path outside worktree and
// scratch, the folders the container writes, with no variable set in front
// of it, and none may run a command of its own outside single quotes, as
// $(...), a backtick, a process substitution or PowerShell's (...) and
// @(...) do. Git may not be pointed at another repository, configuration or
// programs, or recurse into submodules. cfo sandbox run, and cfo gate turn
// starting only it, are how the project's code runs; the cfo commands that
// run it on the host are refused.
func HostCommandRefusal(command, cwd, worktree, scratch string) string {
	segments, nested := hostSegments(command)
	if nested != "" {
		return "this command runs a command of its own with " + nested + ", which a sandbox task never runs on the host: run it in its container with cfo sandbox run -- <command>"
	}
	for _, words := range segments {
		program := programName(words[0])
		if !hostPrograms[program] {
			return "a sandbox task runs " + words[0] + " only in its container, since its code is the adversary: run it with cfo sandbox run -- <command>, and only cfo, git and gh on the host"
		}
		if refusal := programPathRefusal(words[0], cwd, worktree, scratch); refusal != "" {
			return refusal
		}
		switch {
		case program == "git":
			if refusal := gitRefusal(words[1:]); refusal != "" {
				return refusal
			}
		case program != "cfo" || len(words) < 2:
		case words[1] == "gate" && sandboxTurnProgram(words[2:]) != "":
			if refusal := programPathRefusal(sandboxTurnProgram(words[2:]), cwd, worktree, scratch); refusal != "" {
				return refusal
			}
		case hostRefusedCommands[words[1]]:
			return "cfo " + words[1] + " runs the project's own code on the host, which a sandbox task runs in its container: use cfo sandbox run -- <command>, or cfo gate turn --detach <log-file> -- cfo sandbox run -- <command> in the background"
		}
	}
	return ""
}

// programPathRefusal is why word, the program a host command starts, is
// refused when it names the program by a path the container could have
// written: one in worktree or scratch, or one that is not absolute, which is
// read from cwd, a folder of the worktree. A program named alone is the one
// PATH names. Git Bash's /c/ drives are read as Windows drives.
func programPathRefusal(word, cwd, worktree, scratch string) string {
	if !strings.ContainsAny(word, `/\`) {
		return ""
	}
	path := filepath.FromSlash(word)
	if len(word) >= 3 && word[0] == '/' && word[2] == '/' && (word[1] >= 'a' && word[1] <= 'z' || word[1] >= 'A' && word[1] <= 'Z') {
		path = word[1:2] + ":" + filepath.FromSlash(word[2:])
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	isWritable := !filepath.IsAbs(path)
	for _, root := range []string{worktree, scratch} {
		isWritable = isWritable || root != "" && isWithin(path, root)
	}
	if !isWritable {
		return ""
	}
	return "a sandbox task never runs " + word + " on the host, since the container can write it: run cfo, git and gh by name or from where they are installed"
}

// isWithin reports whether path is root or a path inside it.
func isWithin(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

// gitRefusal is why git with arguments, those after its name, is refused on
// a sandbox task's host: an option before its command that points it at
// another repository, configuration or programs (hostRefusedGitOptions), its
// submodule command, or --recurse-submodules, each of which can reach a
// repository the container made in a folder of the worktree.
func gitRefusal(arguments []string) string {
	refused := ""
	isCommand := false
	for index := 0; index < len(arguments) && refused == ""; index++ {
		argument := arguments[index]
		name, _, _ := strings.Cut(argument, "=")
		switch {
		case !isCommand && hostRefusedGitOptions[name]:
			refused = "git " + name
		case !isCommand && (argument == "--namespace" || argument == "--super-prefix"):
			index++
		case !isCommand && !strings.HasPrefix(argument, "-"):
			isCommand = true
			if argument == "submodule" {
				refused = "git submodule"
			}
		case name == "--recurse-submodules":
			refused = "git's --recurse-submodules"
		}
	}
	if refused == "" {
		return ""
	}
	return "a sandbox task never runs " + refused + " on the host, which reaches a repository, configuration or program other than its worktree's, one the container could have made: run git on the worktree itself"
}

// hostSegments splits command into its commands, each as its words with
// their quotes taken off, at ;, |, & and line breaks outside quotes, and
// names the first nested command it holds: $(...), a backtick or a process
// substitution outside single quotes, and PowerShell's (...) and @(...)
// outside any quotes. A redirection to a descriptor, as 2>&1 and &> write
// it, ends no command.
func hostSegments(command string) (segments [][]string, nested string) {
	var words []string
	var word strings.Builder
	isWord := false
	flush := func() {
		if isWord {
			words = append(words, word.String())
		}
		word.Reset()
		isWord = false
	}
	end := func() {
		flush()
		if len(words) > 0 {
			segments = append(segments, words)
		}
		words = nil
	}
	quote := byte(0)
	for index := 0; index < len(command); index++ {
		c := command[index]
		if quote != '\'' {
			for _, form := range []string{"$(", "`", "<(", ">("} {
				if strings.HasPrefix(command[index:], form) {
					return nil, form
				}
			}
		}
		if quote == 0 && c == '(' {
			return nil, "("
		}
		switch {
		case quote != 0 && c == quote:
			quote = 0
		case quote != 0:
			word.WriteByte(c)
		case c == '\'' || c == '"':
			quote, isWord = c, true
		case c == '&' && (index > 0 && command[index-1] == '>' || index+1 < len(command) && command[index+1] == '>'):
			word.WriteByte(c)
			isWord = true
		case c == ';' || c == '|' || c == '&' || c == '\n' || c == '\r':
			end()
		case c == ' ' || c == '\t':
			flush()
		default:
			word.WriteByte(c)
			isWord = true
		}
	}
	end()
	return segments, ""
}

// sandboxTurnProgram is the program word of a turn that takes it for cfo
// sandbox run alone, given the arguments after cfo gate, or empty for any
// other.
func sandboxTurnProgram(arguments []string) string {
	if len(arguments) == 0 || arguments[0] != "turn" {
		return ""
	}
	for index, argument := range arguments {
		if argument == "--" {
			rest := arguments[index+1:]
			if len(rest) >= 3 && programName(rest[0]) == "cfo" && rest[1] == "sandbox" && rest[2] == "run" {
				return rest[0]
			}
			return ""
		}
	}
	return ""
}

// programName is the program a command's first word starts, by its name in
// lower case without its folder or .exe.
func programName(word string) string {
	return strings.ToLower(strings.TrimSuffix(filepath.Base(filepath.FromSlash(word)), ".exe"))
}
