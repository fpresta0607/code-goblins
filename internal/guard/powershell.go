package guard

import (
	"regexp"
	"strings"
)

// Claude Code on Windows has a PowerShell tool beside its Bash tool, and the
// CFO's own session uses it. The guards below are ClassifyArm and ClassifyCd
// for the command of a PowerShell call: the same rules, read as PowerShell
// writes them. A Bash reading of a PowerShell command line is wrong in both
// directions: it takes a backtick for the start of a quoted span, which
// hides the rest of a continued line, and it knows neither Start-Process nor
// the commands PowerShell relocates with.

var (
	// powerShellParameter matches a parameter name, as in -ArgumentList or
	// -FilePath:, but not the --flag of a program's own arguments.
	powerShellParameter = regexp.MustCompile(`(^|\s)-[a-z][a-z0-9]*:?`)
	// powerShellStarter matches the commands that start a program apart from
	// the call: Start-Process and Start-Job, their aliases, a thread job,
	// and the Start of .NET's Process.
	powerShellStarter = regexp.MustCompile(`(?i)\b(start-process|saps|start-job|sajb|start-threadjob)\b|::start\b`)
	// splitWatcher matches a cfo program and a watch argument that a starter
	// was handed apart, as a splatted table hands them.
	splitWatcher = regexp.MustCompile(`(?s)\bcfo(\.exe)?\b.*\bwatch\b`)
	// powerShellKill matches the commands that end processes by name:
	// Stop-Process and its aliases, and the programs a Bash call would use.
	powerShellKill = regexp.MustCompile(`(?i)\b(stop-process|spps|kill|taskkill|pkill)\b`)
	// powerShellInterpreter matches a command that runs its argument as a
	// script: Invoke-Expression, Invoke-Command, and another shell.
	powerShellInterpreter = regexp.MustCompile(`(?i)\b(invoke-expression|iex|invoke-command|icm)\b|\b(powershell|pwsh|cmd|bash|sh|wsl)(\.exe)?\s`)
)

// normalizePowerShellToken prepares the command of a PowerShell call for
// watcher-token detection. Beyond what normalizeToken does, the commas,
// parentheses and @ of an argument array and the * of a wildcard become
// spaces and parameter names are dropped, so a program and its first
// argument stand side by side however they were handed over:
// Start-Process cfo -ArgumentList 'watch', ::Start('cfo.exe', 'watch') and
// -like '*cfo*watch*' all read as cfo watch.
func normalizePowerShellToken(command string) string {
	s := strings.NewReplacer(",", " ", "(", " ", ")", " ", "@", " ", "*", " ").Replace(normalizeToken(command))
	return powerShellParameter.ReplaceAllString(s, " ")
}

// ClassifyPowerShellArm is ClassifyArm for the command of a PowerShell tool
// call. A command that names no watcher allows at once. One that does is
// denied whatever its shape, and the ladder says which shape it is in
// PowerShell's words: Stop-Process for a kill, Start-Process, Start-Job or a
// trailing & for a background start, a script block, a subexpression,
// Invoke-Expression or another shell for nesting, and a here-string for a
// quoted form the guard does not read.
func ClassifyPowerShellArm(command string) (code, reason string, deny bool) {
	normalized := normalizePowerShellToken(command)
	isStarted := powerShellStarter.MatchString(command)
	if !watcherTokenRe.MatchString(normalized) && !(isStarted && splitWatcher.MatchString(normalized)) {
		return "", "", false
	}

	switch {
	case powerShellKill.MatchString(command):
		code = "broad-watcher-kill"
	case hasTrailingBackground(command) || isStarted:
		code = "watcher-background"
	case hasSinglePipe(command):
		code = "watcher-pipeline"
	case containsAny(command, ">"):
		code = "watcher-redirection"
	case containsAny(command, "&&", ";", "||"):
		code = "watcher-bundled"
	case containsAny(command, "$(", "{") || powerShellInterpreter.MatchString(command):
		code = "watcher-nested"
	case containsAny(command, "@'", `@"`):
		code = "unclassifiable-protected-command"
	default:
		code = "watcher-direct"
	}
	return code, armReasons[code], true
}

const powerShellRelocationReason = "Claude Code's PowerShell tool keeps its working directory between calls, so this relocation would outlive the tool call"

// powerShellRelocations are the commands that move a PowerShell session:
// Set-Location, Push-Location and Pop-Location, the aliases PowerShell gives
// them, and the two functions it defines to step up and to the root.
var powerShellRelocations = map[string]bool{
	"set-location":  true,
	"sl":            true,
	"cd":            true,
	"chdir":         true,
	"push-location": true,
	"pushd":         true,
	"pop-location":  true,
	"popd":          true,
	"cd..":          true,
	`cd\`:           true,
}

// driveFunction matches the function PowerShell defines for each drive
// letter, C: and the rest, which moves the session to that drive.
var driveFunction = regexp.MustCompile(`^[a-z]:$`)

// ClassifyPowerShellCd is ClassifyCd for the command of a PowerShell tool
// call, which keeps its location between calls as the Bash tool keeps its
// working directory. A command word that relocates is denied wherever it
// stands: PowerShell has no subshell, so a relocation inside parentheses or
// a script block moves the session too. A module-qualified name, as in
// Microsoft.PowerShell.Management\Set-Location, is read by its last part.
func ClassifyPowerShellCd(command string) (code, reason string, deny bool) {
	for _, word := range powerShellCommands(command) {
		word = strings.ToLower(word)
		name := word[strings.LastIndex(word, `\`)+1:]
		if powerShellRelocations[word] || powerShellRelocations[name] || driveFunction.MatchString(word) {
			return "cwd-relocation", powerShellRelocationReason, true
		}
	}
	return "", "", false
}

// powerShellReader walks a PowerShell script once and collects its command
// words.
type powerShellReader struct {
	runes []rune
	at    int
	words []string
}

// powerShellCommands returns the command word of every command in script, at
// any depth: the first word of each statement and of each element of a
// pipeline, inside parentheses, subexpressions and script blocks alike, and
// of the right side of an assignment. It reads the script as PowerShell does
// wherever that decides what a command word is:
//
//   - a backtick escapes the character after it, a line end too, so a
//     continued line is one statement and c`d is cd;
//   - a single-quoted string ends at its quote, and a doubled quote is a
//     quote inside it;
//   - a double-quoted string is read the same but for a $( ) inside it,
//     whose commands run;
//   - a here-string runs to '@ or "@ at the start of a line;
//   - # starts a comment that runs to the end of its line, and <# #> is one.
//
// A quoted string where a command word is expected is that word, as in
// & 'Set-Location' x. Like statementHeads it would rather deny a command
// that does not relocate than allow one that does: a hash table key or a
// script block handed to a job reads as a command word here.
func powerShellCommands(script string) []string {
	reader := &powerShellReader{runes: []rune(script)}
	reader.code(false)
	return reader.words
}

// peek is the rune at the reader's place, or zero at the end.
func (p *powerShellReader) peek() rune {
	if p.at < len(p.runes) {
		return p.runes[p.at]
	}
	return 0
}

// code reads commands to the end of the script or, inside a subexpression,
// to the parenthesis that closes it.
func (p *powerShellReader) code(isSubexpression bool) {
	var word strings.Builder
	isExpected, isInToken, depth := true, false, 0
	end := func() {
		if word.Len() > 0 {
			// The dot-source operator takes the command after it.
			isExpected = word.String() == "."
			p.words = append(p.words, word.String())
			word.Reset()
		}
		isInToken = false
	}
	quoted := func(text string) {
		if isExpected && !isInToken {
			word.WriteString(text)
		}
		end()
		isExpected = false
	}
	for p.at < len(p.runes) {
		r := p.runes[p.at]
		p.at++
		switch {
		case r == '`':
			escaped := p.peek()
			p.at++
			if escaped == '\r' && p.peek() == '\n' {
				p.at++
			}
			if escaped == '\n' || escaped == '\r' || escaped == 0 {
				end()
				continue
			}
			if isExpected {
				word.WriteRune(escaped)
			}
			isInToken = true
		case r == '<' && p.peek() == '#':
			end()
			for p.at < len(p.runes) && !(p.runes[p.at-1] == '#' && p.runes[p.at] == '>') {
				p.at++
			}
			p.at++
		case r == '#' && !isInToken:
			for p.at < len(p.runes) && p.runes[p.at] != '\n' {
				p.at++
			}
		case r == '@' && p.hereString():
			end()
			isExpected = false
		case r == '\'':
			quoted(p.single())
		case r == '"':
			quoted(p.double())
		case r == ';' || r == '\n' || r == '\r' || r == '|' || r == '&' || r == '{' || r == '}':
			end()
			isExpected = true
		case r == '(':
			end()
			isExpected = true
			depth++
		case r == ')':
			end()
			if depth == 0 && isSubexpression {
				return
			}
			depth = max(depth-1, 0)
			isExpected = false
		case r == ' ' || r == '\t':
			end()
		case r == '=' && !isInToken && (p.peek() == ' ' || p.peek() == '\t'):
			isExpected = true
		default:
			if isExpected {
				word.WriteRune(r)
			}
			isInToken = true
		}
	}
	end()
}

// single reads a single-quoted string whose opening quote was just read, and
// returns its text.
func (p *powerShellReader) single() string {
	var text strings.Builder
	for p.at < len(p.runes) {
		r := p.runes[p.at]
		p.at++
		if r == '\'' {
			if p.peek() != '\'' {
				break
			}
			p.at++
		}
		text.WriteRune(r)
	}
	return text.String()
}

// double reads a double-quoted string whose opening quote was just read, and
// returns its text. A subexpression inside it is code, and is read as code.
func (p *powerShellReader) double() string {
	var text strings.Builder
	for p.at < len(p.runes) {
		r := p.runes[p.at]
		p.at++
		switch {
		case r == '`':
			text.WriteRune(p.peek())
			p.at++
		case r == '"' && p.peek() == '"':
			text.WriteRune(r)
			p.at++
		case r == '"':
			return text.String()
		case r == '$' && p.peek() == '(':
			p.at++
			p.code(true)
		default:
			text.WriteRune(r)
		}
	}
	return text.String()
}

// hereString skips a here-string whose opening @ was just read, and reports
// whether one starts there: a quote that ends its line, closed by the same
// quote and @ at the start of a later line.
func (p *powerShellReader) hereString() bool {
	quote := p.peek()
	if quote != '\'' && quote != '"' {
		return false
	}
	at := p.at + 1
	for at < len(p.runes) && (p.runes[at] == ' ' || p.runes[at] == '\t' || p.runes[at] == '\r') {
		at++
	}
	if at >= len(p.runes) || p.runes[at] != '\n' {
		return false
	}
	for ; at+2 < len(p.runes); at++ {
		if p.runes[at] == '\n' && p.runes[at+1] == quote && p.runes[at+2] == '@' {
			p.at = at + 3
			return true
		}
	}
	p.at = len(p.runes)
	return true
}
