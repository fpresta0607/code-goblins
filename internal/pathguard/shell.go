package pathguard

import (
	"path/filepath"
	"regexp"
	"strings"
)

// credentialPrograms read Windows Credential Manager, the credential store
// the fleet's secrets live in, through its own programs and interfaces
// rather than by a path.
var credentialPrograms = regexp.MustCompile(`(?i)\b(cmdkey|vaultcmd|get-storedcredential|passwordvault|credread|credenumerate)\b`)

// Programs whose every argument is a file they write, create or remove.
var writesEveryArgument = map[string]bool{
	"rm": true, "rmdir": true, "mkdir": true, "touch": true, "tee": true, "truncate": true, "chmod": true,
	"remove-item": true, "ri": true, "del": true, "erase": true, "rd": true, "new-item": true, "ni": true, "md": true,
	"set-content": true, "sc": true, "add-content": true, "ac": true, "out-file": true, "clear-content": true, "tee-object": true,
}

// Programs that write the last file they are given, or the one a
// -Destination parameter names.
var writesLastArgument = map[string]bool{
	"cp": true, "mv": true, "ln": true, "copy-item": true, "cpi": true, "copy": true, "move-item": true, "mi": true,
	"move": true, "rename-item": true, "rni": true, "ren": true, "xcopy": true, "robocopy": true,
}

// movesShell are the commands that move a shell to another folder.
var movesShell = map[string]bool{"cd": true, "pushd": true, "chdir": true, "set-location": true, "sl": true, "push-location": true}

// pathParameters are the PowerShell parameters whose value is a path the
// command acts on; any other parameter's value is not a file.
var pathParameters = map[string]bool{"-path": true, "-literalpath": true, "-filepath": true, "-destination": true}

// switchParameters are the PowerShell parameters a file-writing command
// takes without a value, so the word after one is still a file.
var switchParameters = map[string]bool{"-force": true, "-recurse": true, "-append": true, "-noclobber": true, "-whatif": true, "-confirm": true, "-nonewline": true, "-passthru": true}

// sinks are the redirection targets that write no file.
var sinks = map[string]bool{"/dev/null": true, "$null": true, "nul": true, "/dev/stderr": true, "/dev/stdout": true, "&1": true, "&2": true}

// ShellRefusal is why command, a shell tool's command in Bash or PowerShell,
// is refused, or empty when it may run. It refuses a command that names a
// path in a secret folder anywhere in it, or reaches Credential Manager
// through its programs, and one that writes outside the writable folders by
// a redirection or a program that writes the files it is given. It reads the
// command's words, not what the programs it starts do, as PowerShell reads
// them and as Bash does, and refuses what either reading reaches.
func (b Boundary) ShellRefusal(command string) string {
	if program := credentialPrograms.FindString(command); program != "" {
		return "it reaches " + CredentialStoreName + " through " + program + ", which no goblin or gate step reads"
	}
	for _, isEscaping := range []bool{false, true} {
		if reason := b.shellReadingRefusal(shellSegments(command, isEscaping)); reason != "" {
			return reason
		}
	}
	return ""
}

// shellReadingRefusal is why a command, read as segments, is refused, or
// empty when it may run, as ShellRefusal says.
func (b Boundary) shellReadingRefusal(segments [][]shellWord) string {
	for _, segment := range segments {
		moved, isMove := b.movedTo(segment)
		for _, word := range segment {
			if word.isRedirect {
				continue
			}
			value := word.pathValue()
			if value == "" {
				continue
			}
			if reason := b.Refusal(value, false); reason != "" {
				return reason
			}
			if reason := b.namedSSH(value); reason != "" {
				return reason
			}
		}
		for _, target := range writeTargets(segment) {
			if reason := b.Refusal(target, true); reason != "" {
				return reason
			}
		}
		if isMove {
			b.Cwd = moved
		}
	}
	return ""
}

// namedSSH refuses a word that names a .ssh folder outside the writable
// folders, which a relative path from a folder the shell moved to reaches.
func (b Boundary) namedSSH(value string) string {
	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == '/' || r == '\\' }) {
		if !strings.EqualFold(part, ".ssh") {
			continue
		}
		resolved, ok := b.resolve(value)
		if !ok {
			return "it names " + value + ", a .ssh folder, from a folder the command moved to that it does not say"
		}
		for _, root := range b.Writable {
			if root != "" && within(resolved, root) {
				return ""
			}
		}
		return "it names " + value + ", a .ssh folder, which no goblin or gate step reads or writes"
	}
	return ""
}

// movedTo is the folder a segment that moves the shell, as cd does, moves
// it to, empty when the command does not say, as for cd - or a folder a
// subcommand names. The paths of the segments after it are read from there.
func (b Boundary) movedTo(segment []shellWord) (string, bool) {
	if !movesShell[strings.ToLower(segment[0].text)] {
		return "", false
	}
	var arguments []string
	for _, word := range segment[1:] {
		if word.isRedirect {
			break
		}
		if !strings.HasPrefix(word.text, "-") {
			arguments = append(arguments, word.text)
		}
	}
	switch {
	case len(arguments) == 0 && len(segment) == 1:
		return b.UserHome, true
	case len(arguments) != 1 || strings.Contains(arguments[0], "$("):
		return "", true
	}
	resolved, ok := b.resolve(arguments[0])
	if !ok {
		return "", true
	}
	return resolved, true
}

// writeTargets are the files a segment, one command of a shell line,
// writes: each redirection's target, and the files a file-writing program
// is given.
func writeTargets(segment []shellWord) []string {
	var targets []string
	for index, word := range segment {
		if word.isRedirect && index+1 < len(segment) && !segment[index+1].isRedirect && !sinks[strings.ToLower(segment[index+1].text)] {
			targets = append(targets, segment[index+1].text)
		}
	}
	verbIndex := verbAt(segment)
	if verbIndex < 0 {
		return targets
	}
	verb := segmentVerb(segment)
	if !writesEveryArgument[verb] && !writesLastArgument[verb] {
		return targets
	}
	var files, destinations []string
	for index := verbIndex + 1; index < len(segment); index++ {
		word := segment[index]
		if word.isRedirect {
			index++
			continue
		}
		lower := strings.ToLower(word.text)
		if !strings.HasPrefix(lower, "-") {
			files = append(files, word.text)
			continue
		}
		name, _, hasValue := strings.Cut(lower, ":")
		if !hasValue {
			name, _, hasValue = strings.Cut(lower, "=")
		}
		if hasValue {
			if pathParameters[name] {
				destinations = append(destinations, word.text[len(name)+1:])
			}
			continue
		}
		if !pathParameters[name] && (switchParameters[name] || !strings.Contains(verb, "-")) {
			continue
		}
		if index+1 < len(segment) && !segment[index+1].isRedirect {
			index++
			if pathParameters[name] {
				destinations = append(destinations, segment[index].text)
			}
		}
	}
	switch {
	case writesEveryArgument[verb]:
		return append(append(targets, destinations...), files...)
	case len(destinations) > 0:
		return append(targets, destinations...)
	case len(files) > 0:
		return append(targets, files[len(files)-1])
	}
	return targets
}

// verbAt is the index of the program a segment starts, past the variables
// it sets for it and sudo or command, or -1 when it starts none.
func verbAt(segment []shellWord) int {
	for index, word := range segment {
		if word.isRedirect {
			return -1
		}
		if name, _, isAssignment := strings.Cut(word.text, "="); isAssignment && !strings.ContainsAny(name, `/\`) {
			continue
		}
		if strings.EqualFold(word.text, "sudo") || strings.EqualFold(word.text, "command") {
			continue
		}
		return index
	}
	return -1
}

// segmentVerb is the program a segment starts, by its name in lower case
// without its folder or .exe, or empty when it starts none.
func segmentVerb(segment []shellWord) string {
	index := verbAt(segment)
	if index < 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(filepath.Base(filepath.FromSlash(segment[index].text)), ".exe"))
}

// shellWord is one word of a shell command, its quotes taken off, or a
// redirection operator.
type shellWord struct {
	text       string
	isRedirect bool
}

// pathValue is the path a word may name, read from the shell's folder when
// it is relative, as a bare .ssh is from the user's home: the word, the
// value of a --flag=value, -Name:value or NAME=value word, and nothing for a
// flag or a URL.
func (w shellWord) pathValue() string {
	text := w.text
	if strings.HasPrefix(text, "-") {
		index := strings.IndexAny(text, ":=")
		if index < 0 {
			return ""
		}
		text = text[index+1:]
	} else if name, value, found := strings.Cut(text, "="); found && !strings.ContainsAny(name, `/\`) {
		text = value
	}
	if strings.Contains(text, "://") {
		return ""
	}
	return text
}

// shellSegments splits command into its commands, at ;, |, &, && and ||
// and line breaks, and each into its words, keeping a quoted span in one
// word and a redirection operator apart from the words around it. It reads
// Bash and PowerShell alike, which is enough to find the paths a command
// names: a word it splits wrongly names no folder it would otherwise miss.
// They differ on a backslash, which Bash reads as an escape and PowerShell
// as a character, so with isEscaping it reads one before a quote or a
// backslash as Bash does, as that character itself, outside quotes and, for
// " and \, in double quotes. A backslash before any other character stays,
// as the folder separator a Windows path holds.
func shellSegments(command string, isEscaping bool) [][]shellWord {
	var segments [][]shellWord
	var segment []shellWord
	var word strings.Builder
	isWord := false
	flushWord := func() {
		if isWord {
			segment = append(segment, shellWord{text: word.String()})
		}
		word.Reset()
		isWord = false
	}
	endSegment := func() {
		flushWord()
		if len(segment) > 0 {
			segments = append(segments, segment)
		}
		segment = nil
	}
	for index := 0; index < len(command); index++ {
		c := command[index]
		switch {
		case isEscaping && c == '\\' && index+1 < len(command) && strings.IndexByte(`"'\`, command[index+1]) >= 0:
			index++
			word.WriteByte(command[index])
			isWord = true
		case c == '\'' || c == '"':
			for index++; index < len(command) && command[index] != c; index++ {
				if isEscaping && c == '"' && command[index] == '\\' && index+1 < len(command) && strings.IndexByte(`"\`, command[index+1]) >= 0 {
					index++
				}
				word.WriteByte(command[index])
			}
			isWord = true
		case c == '>':
			// A file descriptor or & written against the operator is part
			// of it, as in 2>, &> and 1>>.
			if text := word.String(); text == "1" || text == "2" || text == "&" || text == "*" {
				word.Reset()
				isWord = false
			}
			flushWord()
			for index+1 < len(command) && (command[index+1] == '>' || command[index+1] == '|') {
				index++
			}
			segment = append(segment, shellWord{isRedirect: true})
			if index+1 < len(command) && command[index+1] == '&' {
				end := index + 2
				for end < len(command) && command[end] >= '0' && command[end] <= '9' {
					end++
				}
				segment = append(segment, shellWord{text: command[index+1 : end]})
				index = end - 1
			}
		case c == ';' || c == '|' || c == '&' || c == '\n' || c == '\r':
			if c == '&' && index+1 < len(command) && command[index+1] == '>' {
				word.WriteByte(c)
				isWord = true
				continue
			}
			endSegment()
		case c == ' ' || c == '\t' || c == '(' || c == ')' || c == '{' || c == '}' || c == '`' || c == '<' || c == ',':
			flushWord()
		default:
			word.WriteByte(c)
			isWord = true
		}
	}
	endSegment()
	return segments
}
