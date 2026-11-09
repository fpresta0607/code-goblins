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
// checks this repository runs, the checks cfo verify runs before a push, and
// a local services stack, whose compose file the project writes.
var hostRefusedCommands = map[string]bool{"gate": true, "services": true, "verify": true}

// HostCommandRefusal is why command, a shell command a sandbox task's goblin
// is about to run on the host, is refused, or empty when it runs. Each of
// its commands, at ;, |, & and line breaks outside quotes, must start cfo,
// git or gh by name or by path, with no variable set in front of it, and
// none may run a command of its own outside single quotes, as $(...), a
// backtick or a process substitution does. cfo sandbox run, and cfo gate
// turn starting only it, are how the project's code runs; the cfo commands
// that run it on the host are refused.
func HostCommandRefusal(command string) string {
	segments, nested := hostSegments(command)
	if nested != "" {
		return "this command runs a command of its own with " + nested + ", which a sandbox task never runs on the host: run it in its container with cfo sandbox run -- <command>"
	}
	for _, words := range segments {
		program := programName(words[0])
		if !hostPrograms[program] {
			return "a sandbox task runs " + words[0] + " only in its container, since its code is the adversary: run it with cfo sandbox run -- <command>, and only cfo, git and gh on the host"
		}
		if program != "cfo" || len(words) < 2 {
			continue
		}
		if words[1] == "gate" && isSandboxTurn(words[2:]) {
			continue
		}
		if hostRefusedCommands[words[1]] {
			return "cfo " + words[1] + " runs the project's own code on the host, which a sandbox task runs in its container: use cfo sandbox run -- <command>, or cfo gate turn --detach <log-file> -- cfo sandbox run -- <command> in the background"
		}
	}
	return ""
}

// hostSegments splits command into its commands, each as its words with
// their quotes taken off, at ;, |, & and line breaks outside quotes, and
// names the first nested command it holds outside single quotes. A
// redirection to a descriptor, as 2>&1 and &> write it, ends no command.
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

// isSandboxTurn reports whether arguments, those after cfo gate, take a turn
// for cfo sandbox run alone.
func isSandboxTurn(arguments []string) bool {
	if len(arguments) == 0 || arguments[0] != "turn" {
		return false
	}
	for index, argument := range arguments {
		if argument == "--" {
			rest := arguments[index+1:]
			return len(rest) >= 3 && programName(rest[0]) == "cfo" && rest[1] == "sandbox" && rest[2] == "run"
		}
	}
	return false
}

// programName is the program a command's first word starts, by its name in
// lower case without its folder or .exe.
func programName(word string) string {
	return strings.ToLower(strings.TrimSuffix(filepath.Base(filepath.FromSlash(word)), ".exe"))
}
