package sandbox

import (
	"fmt"
	"path/filepath"
	"strings"
)

// hostPrograms are the programs a sandbox task's goblin runs on the host:
// ceo to report, gate and contain its work, and git and gh to deliver it.
// Everything else runs in its container, through ceo sandbox run.
var hostPrograms = map[string]bool{"ceo": true, "git": true, "gh": true}

// hostRefusedCommands are the ceo commands that run the project's own code
// on the host, which a sandbox task runs in its container instead: the gate
// checks this repository runs, the checks ceo verify runs before a push,
// the security tier ceo security runs, the build ceo deploy runs, and a
// local services stack, whose compose file the project writes.
var hostRefusedCommands = map[string]bool{"deploy": true, "gate": true, "security": true, "services": true, "verify": true}

// hostGitCommands are the git commands a sandbox task's goblin delivers its
// work with, the only ones its host runs.
var hostGitCommands = map[string]bool{
	"status": true, "diff": true, "log": true, "show": true, "add": true, "commit": true, "push": true, "fetch": true, "pull": true, "switch": true, "checkout": true,
	"branch": true, "rebase": true, "merge": true, "reset": true, "restore": true, "stash": true, "tag": true, "rev-parse": true, "ls-files": true, "blame": true,
}

// hostRefusedGitOptions are the long options of those git commands that
// start a program of the command's choosing, or reach a submodule, whose
// repository the container could have made. Git takes any unambiguous start
// of a long option for it, so each start is refused too.
var hostRefusedGitOptions = []string{"--exec", "--receive-pack", "--recurse-submodules", "--recursive", "--upload-pack"}

// hostRefusedGhCommands are the gh commands that run a command or program
// of the goblin's choosing as gh: an alias and an extension.
var hostRefusedGhCommands = map[string]bool{"alias": true, "extension": true, "extensions": true, "ext": true}

// whyNotPlain is why a character a host command holds outside quotes is
// refused, for the characters a shell reads as more than text.
var whyNotPlain = map[byte]string{
	';': "a ;, which starts another command", '|': "a |, which starts another command", '&': "a single &, which runs or calls another command",
	'\n': "a line break, which starts another command", '\r': "a line break, which starts another command",
	'>': "a >, which redirects", '<': "a <, which redirects", '$': "a $, which expands a variable or runs a command", '`': "a backtick, which runs a command",
	'(': "a (, which runs a command of its own", ')': "a ), which ends a command of its own", '{': "a {, which groups commands", '}': "a }, which ends a group of commands",
	'*': "a *, which expands to file names", '?': "a ?, which expands to file names", '[': "a [, which expands to file names", '#': "a #, which starts a comment",
	'!': "a !, which Bash expands from its history", '\'': "a quote no other quote ends", '"': "a quote no other quote ends",
	'\\': "a backslash outside quotes, which Bash takes off and PowerShell keeps: write a path with / instead", ',': "a , outside quotes, which PowerShell reads as a list of words",
}

// HostCommandRefusal is why command, a shell command a sandbox task's goblin
// is about to run on the host from cwd, is refused, or empty when it runs.
// It allows what Bash and PowerShell read alike, and refuses everything
// else: simple commands joined by &&, each of words of plain characters and
// quoted strings (hostCommands), that start ceo, git or gh by name or by an
// absolute path outside worktree and scratch, the folders the container
// writes. Git runs only the commands a goblin delivers with (gitRefusal),
// and gh no alias or extension (ghRefusal). ceo sandbox run, and ceo gate
// turn starting only it, are how the project's code runs; the ceo commands
// that run it on the host are refused.
func HostCommandRefusal(command, cwd, worktree, scratch string) string {
	commands, refusal := hostCommands(command)
	if refusal != "" {
		return "this command holds " + refusal + ", and a sandbox task's host runs only ceo, git and gh commands of plain words and quoted text joined by &&: run anything else in its container with ceo sandbox run -- <command>"
	}
	for _, words := range commands {
		program := programName(words[0])
		if !hostPrograms[program] {
			return "a sandbox task runs " + words[0] + " only in its container, since its code is the adversary: run it with ceo sandbox run -- <command>, and only ceo, git and gh on the host"
		}
		if refusal := programPathRefusal(words[0], cwd, worktree, scratch); refusal != "" {
			return refusal
		}
		switch {
		case program == "git":
			if refusal := gitRefusal(words[1:]); refusal != "" {
				return refusal
			}
		case program == "gh":
			if refusal := ghRefusal(words[1:]); refusal != "" {
				return refusal
			}
		case len(words) < 2:
		case words[1] == "gate" && sandboxTurnProgram(words[2:]) != "":
			if refusal := programPathRefusal(sandboxTurnProgram(words[2:]), cwd, worktree, scratch); refusal != "" {
				return refusal
			}
		case hostRefusedCommands[words[1]]:
			return "ceo " + words[1] + " runs the project's own code on the host, which a sandbox task runs in its container: use ceo sandbox run -- <command>, or ceo gate turn --detach <log-file> -- ceo sandbox run -- <command> in the background"
		}
	}
	return ""
}

// hostCommands splits command into its commands at &&, each as its words
// with their quotes taken off, or says why it is refused. A word is made of
// plain characters, letters, digits and . _ / : = @ % + - ~, and of single-
// or double-quoted strings that hold no backslash, $, backtick, parenthesis,
// quote of the other kind or quote PowerShell reads as its own. Bash and
// PowerShell then read the same words, and what either would read
// differently is refused: a backslash outside quotes, which Bash takes off
// and PowerShell keeps; a word that starts with a quoted string and goes on
// after it, which PowerShell reads as two words; an empty word, which
// Windows PowerShell drops; a comma, which PowerShell reads as a list of
// words; an @ that starts a word, which PowerShell splats; a word that
// starts with - and holds a ., which Windows PowerShell splits at the .;
// and --%, after which PowerShell stops reading.
func hostCommands(command string) ([][]string, string) {
	var commands [][]string
	var words []string
	var word strings.Builder
	// isBare is whether the word starts with a plain character, which
	// PowerShell reads as a word of its own grammar, not a string.
	isWord, isBare := false, false
	flush := func() string {
		text := word.String()
		word.Reset()
		if !isWord {
			return ""
		}
		wasBare := isBare
		isWord, isBare = false, false
		switch {
		case text == "":
			return "an empty quoted string, which Windows PowerShell drops before it starts a program"
		case wasBare && text == "--%":
			return "--%, after which PowerShell reads the rest of the command as no words of its own"
		case wasBare && strings.HasPrefix(text, "-") && strings.Contains(text, "."):
			return "the word " + text + ", which starts with - and holds a ., where Windows PowerShell splits it in two: quote the whole word"
		}
		words = append(words, text)
		return ""
	}
	for index := 0; index < len(command); index++ {
		c := command[index]
		switch {
		case c == ' ' || c == '\t':
			if refusal := flush(); refusal != "" {
				return nil, refusal
			}
		case c == '&' && index+1 < len(command) && command[index+1] == '&':
			if refusal := flush(); refusal != "" {
				return nil, refusal
			}
			if len(words) > 0 {
				commands = append(commands, words)
			}
			words = nil
			index++
		case c == '@' && !isWord:
			return nil, "an @ that starts a word, which PowerShell reads as a variable or array it splats"
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte(`._/:=@%+-~`, c) >= 0:
			word.WriteByte(c)
			isBare = isBare || !isWord
			isWord = true
		case c == '\'' || c == '"':
			end := strings.IndexByte(command[index+1:], c)
			if end < 0 {
				return nil, whyNotPlain[c]
			}
			text := command[index+1 : index+1+end]
			if at := strings.IndexAny(text, "\\$`()'\""); at >= 0 {
				return nil, fmt.Sprintf("%q in a quoted string, which Bash or PowerShell reads as more than text there", text[at])
			}
			if strings.ContainsAny(text, "\u2018\u2019\u201a\u201b\u201c\u201d\u201e") {
				return nil, "a typographic quote in a quoted string, which PowerShell reads as a quote that ends it"
			}
			after := index + 1 + end + 1
			if !isWord && after < len(command) && command[after] != ' ' && command[after] != '\t' && command[after] != '&' {
				return nil, "a word that starts with a quoted string and goes on after it, which PowerShell reads as two words and Bash as one: quote the whole word"
			}
			word.WriteString(text)
			isWord = true
			index += end + 1
		case whyNotPlain[c] != "":
			return nil, whyNotPlain[c]
		default:
			return nil, fmt.Sprintf("%q outside quotes, which is no plain character", c)
		}
	}
	if refusal := flush(); refusal != "" {
		return nil, refusal
	}
	if len(words) > 0 {
		commands = append(commands, words)
	}
	return commands, ""
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
	return "a sandbox task never runs " + word + " on the host, since the container can write it: run ceo, git and gh by name or from where they are installed"
}

// isWithin reports whether path is root or a path inside it.
func isWithin(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

// gitRefusal is why git with arguments, those after its name, is refused on
// a sandbox task's host: an option before its command, which can point it
// at another repository, configuration or program, a command that is none of
// hostGitCommands, rebase's -x, which runs a command at each commit, or one
// of hostRefusedGitOptions.
func gitRefusal(arguments []string) string {
	if len(arguments) == 0 {
		return ""
	}
	command := arguments[0]
	if strings.HasPrefix(command, "-") {
		return "a sandbox task never runs git " + command + " on the host, an option before git's command, which can point git at another repository, configuration or program: run git with its command first"
	}
	if !hostGitCommands[command] {
		return "a sandbox task never runs git " + command + " on the host, which is none of the git commands it delivers with: status, diff, log, show, add, commit, push, fetch, pull, switch, checkout, branch, rebase, merge, reset, restore, stash, tag, rev-parse, ls-files and blame"
	}
	for _, argument := range arguments[1:] {
		if command == "rebase" && strings.HasPrefix(argument, "-") && !strings.HasPrefix(argument, "--") && strings.Contains(argument, "x") {
			return "a sandbox task never runs git rebase " + argument + " on the host, since -x runs a command at each commit: run the command in its container with ceo sandbox run"
		}
	}
	return refusedOption("git "+command, arguments[1:])
}

// ghRefusal is why gh with arguments, those after its name, is refused on a
// sandbox task's host: an alias or extension command, which runs a command
// or program of the goblin's choosing as gh, or one of
// hostRefusedGitOptions, which gh hands to the git it runs.
func ghRefusal(arguments []string) string {
	if len(arguments) > 0 && hostRefusedGhCommands[arguments[0]] {
		return "a sandbox task never runs gh " + arguments[0] + " on the host, which runs a command or program of its own as gh"
	}
	return refusedOption("gh", arguments)
}

// refusedOption is why program is refused one of arguments, an option of
// hostRefusedGitOptions or a start of one, or empty for none.
func refusedOption(program string, arguments []string) string {
	for _, argument := range arguments {
		name, _, _ := strings.Cut(argument, "=")
		if !strings.HasPrefix(name, "--") || len(name) <= 2 {
			continue
		}
		for _, refused := range hostRefusedGitOptions {
			if strings.HasPrefix(refused, name) {
				return "a sandbox task never runs " + program + " with " + argument + " on the host, since " + refused + " starts a program of its own or reaches a submodule"
			}
		}
	}
	return ""
}

// RunsNoHostGit reports whether command, which HostCommandRefusal lets run,
// starts only ceo sandbox run, by itself or in a ceo gate turn, and ceo
// notify, none of which runs git on the host. Those run while a .git the
// container made or a running container refuses every other host command,
// so a goblin can still remove the one in its container, wait its turn and
// report.
func RunsNoHostGit(command string) bool {
	commands, refusal := hostCommands(command)
	if refusal != "" || len(commands) == 0 {
		return false
	}
	for _, words := range commands {
		if programName(words[0]) != "ceo" || len(words) < 2 {
			return false
		}
		isSandboxRun := words[1] == "sandbox" && len(words) > 2 && words[2] == "run"
		if words[1] != "notify" && !isSandboxRun && (words[1] != "gate" || sandboxTurnProgram(words[2:]) == "") {
			return false
		}
	}
	return true
}

// sandboxTurnProgram is the program word of a turn that takes it for ceo
// sandbox run alone, given the arguments after ceo gate, or empty for any
// other.
func sandboxTurnProgram(arguments []string) string {
	if len(arguments) == 0 || arguments[0] != "turn" {
		return ""
	}
	for index, argument := range arguments {
		if argument == "--" {
			rest := arguments[index+1:]
			if len(rest) >= 3 && programName(rest[0]) == "ceo" && rest[1] == "sandbox" && rest[2] == "run" {
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
