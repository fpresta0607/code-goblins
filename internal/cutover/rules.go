package cutover

import (
	"regexp"
	"slices"
	"strings"
)

// The supervisor's role was called the CFO until 2026-11-09 and is the CEO
// since. This package is the one place outside recorded history that still
// spells the old name, since each thing it moves is found by it.
const (
	oldName = "cfo"
	newName = "ceo"
)

var (
	anyOldName = regexp.MustCompile(`(?i)c(f)o`)
	anyNewName = regexp.MustCompile(`(?i)c(e)o`)
)

// newSpelling is s with every old name in it spelled the new way, each letter
// keeping its case.
func newSpelling(s string) string {
	return respell(anyOldName, s, "e", "E")
}

// oldSpelling is s with every new name in it spelled the old way, each
// letter keeping its case.
func oldSpelling(s string) string {
	return respell(anyNewName, s, "f", "F")
}

func respell(pattern *regexp.Regexp, s, lower, upper string) string {
	return pattern.ReplaceAllStringFunc(s, func(match string) string {
		middle := lower
		if strings.ToUpper(match[1:2]) == match[1:2] {
			middle = upper
		}
		return match[:1] + middle + match[2:]
	})
}

// names are the names, as the new build spells them, of the files, markers,
// programs and identifiers the home and the harnesses hold that a build
// before the rename wrote with the old name: each is renamed wherever it is
// named, whole.
var names = []string{
	// The state folder's own files.
	"ceo-engine.json", "ceo-conversation.json", "ceo-conversation-left.json", "ceo-back.json",
	"ceo-claude-settings.json", "ceo-update.json", "ceo-wake-skips.jsonl", "ceo-receipts.jsonl",
	"ceo-harness", "ceo-harness.pid", ".ceo-rewoken", ".ceo-wake-typed", ".ceo-launch", ".ceo-launch.lock",
	// The home's markers.
	".ceo-home", ".ceo-narrowing-unfinished",
	// What the install and the native hooks write in a harness's folder.
	"ceo-native.ts", "ceo-native-hook.ps1", ".ceo-native.backup", ".ceo-install.bak", ".ceo-install.rules",
	// The programs.
	"ceo.exe", "ceo-hook.exe",
	// Identifiers the state's records hold as values.
	"ceo_answer", "ceo_message", "ceo_wait", "primary-ceo", "previous-ceo", "board-ceo",
	// The copy of the old build the last update kept as its way back.
	"previous-ceo.exe",
	// The schemas of the state's records, which the new build reads only
	// under its own names.
	"ceo-fleet-wakes.v1", "ceo-merge-train.v1", "ceo-monitor.v1", "ceo-monitor-polls.v1", "ceo-monitor-tally.v1",
	"ceo-reap.v2", "ceo-update.v1",
}

// prefixes begin the names of a kind of file or record the new build names
// with the rest of the name its own, such as a handoff note's day.
var prefixes = []string{"ceo-handoff-", "ceo-unwoken-", "ceo-stopped-", "ceo-answer-"}

// markers are words a build before the rename wrote to mark what it owns,
// such as the native hooks in a harness's settings.
var markers = []string{"CEO native hooks"}

// rules rewrite the old command, its settings and the names it owns in a
// file's text, leaving every other word as it is: a task's id, a branch's
// name or a sentence about the role keeps its words.
type rules struct {
	command *regexp.Regexp
	skills  []string
}

var (
	variable   = regexp.MustCompile(`\b` + strings.ToUpper(oldName) + `_([A-Z0-9])`)
	backticked = regexp.MustCompile("`" + oldName + "`")
	nameToken  = regexp.MustCompile(`(^|[^A-Za-z0-9_-])(\.?(?i:` + oldName + `)[-_.][A-Za-z0-9_.-]*)`)
	jsonKey    = regexp.MustCompile(`"((?:[a-z0-9]+_)*` + oldName + `(?:_[a-z0-9]+)*)"(\s*:)`)
	jsonValue  = regexp.MustCompile(`"` + oldName + `(: [^"]*)?"`)
	// aside is the name an update gives a program it moved out of the way
	// while it ran: the program's name, a number and .update-old.
	aside = regexp.MustCompile(`^(.+)(\.\d+\.update-old)$`)
	// ledName finds the names whose old name follows another word, such as
	// the last update's previous copy, which nameToken leaves alone since a
	// task's id is spelled the same way.
	ledName     = ledNames()
	quotedWords = regexp.MustCompile(`(?m)^[ \t]*>.*$|"[^"\n]*"|“[^”]*”`)
)

func ledNames() *regexp.Regexp {
	var led []string
	for _, name := range names {
		if !strings.HasPrefix(strings.TrimPrefix(name, "."), newName) {
			led = append(led, regexp.QuoteMeta(oldSpelling(name)))
		}
	}
	slices.SortFunc(led, func(a, b string) int { return len(b) - len(a) })
	return regexp.MustCompile(`(^|[^A-Za-z0-9_-])(` + strings.Join(led, "|") + `)\b`)
}

// newRules rewrites the old command's calls of commands, and the names of
// skills, the build's skill folders as the new build spells them.
func newRules(commands, skills []string) rules {
	if len(commands) == 0 {
		return rules{skills: skills}
	}
	quoted := make([]string, len(commands))
	for index, command := range commands {
		quoted[index] = regexp.QuoteMeta(command)
	}
	slices.SortFunc(quoted, func(a, b string) int { return len(b) - len(a) })
	return rules{
		command: regexp.MustCompile(`(^|[^A-Za-z0-9_.-])` + oldName + `( +)(` + strings.Join(quoted, "|") + `)\b`),
		skills:  skills,
	}
}

// renamed is the new spelling of name when it is one the old build gave, or
// name itself.
func (r rules) renamed(name string) string {
	respelled := newSpelling(name)
	lower := strings.ToLower(respelled)
	if slices.Contains(names, lower) || slices.Contains(r.skills, lower) {
		return respelled
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			return respelled[:len(prefix)] + name[len(prefix):]
		}
	}
	if parts := aside.FindStringSubmatch(name); parts != nil {
		if program := r.renamed(parts[1]); program != parts[1] {
			return program + parts[2]
		}
	}
	return name
}

// renamedFile is the new name of a file the old build named, which may end
// with one of the names, as a settings file's backup does, or name itself.
func (r rules) renamedFile(name string) string {
	if renamed := r.renamed(name); renamed != name {
		return renamed
	}
	for _, known := range names {
		old := oldSpelling(known)
		if strings.HasPrefix(known, ".") && strings.HasSuffix(strings.ToLower(name), old) {
			return name[:len(name)-len(old)] + known
		}
	}
	return name
}

// text rewrites text, outside the spans protected keeps as written.
func (r rules) text(text string, protected *regexp.Regexp) string {
	if protected == nil {
		return r.words(text)
	}
	var out strings.Builder
	last := 0
	for _, span := range protected.FindAllStringIndex(text, -1) {
		out.WriteString(r.words(text[last:span[0]]))
		out.WriteString(text[span[0]:span[1]])
		last = span[1]
	}
	out.WriteString(r.words(text[last:]))
	return out.String()
}

// json rewrites a JSON document's text: its words as text does, every key
// that names the old role as a word of its own, every value that is the old
// name alone, and every label of the old name's, such as "cfo: running".
func (r rules) json(text string) string {
	text = jsonKey.ReplaceAllStringFunc(text, func(match string) string {
		return newSpelling(match)
	})
	text = jsonValue.ReplaceAllStringFunc(text, func(match string) string {
		return `"` + newName + match[1+len(oldName):]
	})
	return r.words(text)
}

func (r rules) words(text string) string {
	text = variable.ReplaceAllString(text, strings.ToUpper(newName)+"_$1")
	text = backticked.ReplaceAllString(text, "`"+newName+"`")
	if r.command != nil {
		text = r.command.ReplaceAllString(text, "${1}"+newName+"${2}${3}")
	}
	for _, marker := range markers {
		text = strings.ReplaceAll(text, oldSpelling(marker), marker)
	}
	text = ledName.ReplaceAllStringFunc(text, newSpelling)
	return nameToken.ReplaceAllStringFunc(text, func(match string) string {
		parts := nameToken.FindStringSubmatch(match)
		token := strings.TrimRight(parts[2], ".")
		return parts[1] + r.renamed(token) + parts[2][len(token):]
	})
}
