package pathguard

import "strings"

// grepValueOptions are grep's short options that take a value, which is the
// rest of their word or the next word.
const grepValueOptions = "efmABCdD"

// grepValueLongOptions are grep's long options that take a value, which
// follows an = or is the next word.
var grepValueLongOptions = map[string]bool{
	"--regexp": true, "--file": true, "--max-count": true, "--after-context": true, "--before-context": true, "--context": true,
	"--directories": true, "--devices": true, "--include": true, "--exclude": true, "--exclude-from": true, "--exclude-dir": true,
	"--label": true, "--binary-files": true,
}

// driveRootSearch names a recursive find or grep in command whose search root
// is a drive root, as "grep searches /", or is empty. Such a search walks
// the whole drive: on 2026-11-11 a goblin's grep -rn with a stray / ran for
// 1 hour 50 minutes after its shell ended and held 6.7 million handles. A
// find given -maxdepth has a depth its author chose and is let run. A bare \
// is the drive root only to PowerShell: to Bash it escapes the character
// after it or continues the line.
func driveRootSearch(command string, isPowerShell bool) string {
	for _, isEscaping := range []bool{false, true} {
		for _, segment := range shellSegments(command, isEscaping) {
			index := verbAt(segment)
			if index < 0 {
				continue
			}
			var words []string
			for position := index + 1; position < len(segment); position++ {
				if segment[position].isRedirect {
					// The word after a redirection is its target.
					position++
					continue
				}
				words = append(words, segment[position].text)
			}
			verb := segmentVerb(segment)
			var roots []string
			switch verb {
			case "find":
				roots = findRoots(words)
			case "grep", "egrep", "fgrep":
				roots = recursiveGrepPaths(words)
			}
			for _, root := range roots {
				if isDriveRoot(root) && (isPowerShell || root != `\`) {
					return verb + " searches " + root
				}
			}
		}
	}
	return ""
}

// findRoots are the folders find's words start it from, the words after its
// -H, -L and -P options up to its first expression, or none when it is given
// -maxdepth.
func findRoots(words []string) []string {
	for _, word := range words {
		if word == "-maxdepth" {
			return nil
		}
	}
	var roots []string
	for _, word := range words {
		switch {
		case word == "-H" || word == "-L" || word == "-P":
			continue
		case strings.HasPrefix(word, "-") || word == "!" || word == "(":
			return roots
		}
		roots = append(roots, word)
	}
	return roots
}

// recursiveGrepPaths are the files and folders grep's words have it search
// when they make it recurse, by -r, -R, --recursive,
// --dereference-recursive or -d recurse, and none otherwise. Its first
// operand is its pattern unless -e or -f gave one.
func recursiveGrepPaths(words []string) []string {
	isRecursive, hasPattern := false, false
	var operands []string
	for index := 0; index < len(words); index++ {
		word := words[index]
		switch {
		case word == "--":
			operands = append(operands, words[index+1:]...)
			index = len(words)
		case strings.HasPrefix(word, "--"):
			name, value, hasValue := strings.Cut(word, "=")
			if grepValueLongOptions[name] && !hasValue && index+1 < len(words) {
				index++
				value = words[index]
			}
			isRecursive = isRecursive || name == "--recursive" || name == "--dereference-recursive" || name == "--directories" && value == "recurse"
			hasPattern = hasPattern || name == "--regexp" || name == "--file"
		case strings.HasPrefix(word, "-") && len(word) > 1:
			for position := 1; position < len(word); position++ {
				option := word[position]
				isRecursive = isRecursive || option == 'r' || option == 'R'
				if strings.IndexByte(grepValueOptions, option) < 0 {
					continue
				}
				hasPattern = hasPattern || option == 'e' || option == 'f'
				value := word[position+1:]
				if value == "" && index+1 < len(words) {
					index++
					value = words[index]
				}
				isRecursive = isRecursive || option == 'd' && value == "recurse"
				break
			}
		default:
			operands = append(operands, word)
		}
	}
	if !isRecursive {
		return nil
	}
	if !hasPattern && len(operands) > 0 {
		operands = operands[1:]
	}
	return operands
}

// isDriveRoot reports whether path is the root of a drive: / or \, a Git
// Bash drive such as /c or /c/, or a Windows drive such as C:\ or C:/. A bare
// C: is the folder the shell is in on that drive, not its root.
func isDriveRoot(path string) bool {
	trimmed := strings.TrimRight(path, `/\`)
	switch {
	case trimmed == "":
		return path != ""
	case len(trimmed) == 2 && trimmed[0] == '/' && isLetter(trimmed[1]):
		return true
	case len(trimmed) == 2 && isLetter(trimmed[0]) && trimmed[1] == ':':
		return len(path) > len(trimmed)
	}
	return false
}
