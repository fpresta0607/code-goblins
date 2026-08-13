package guard

import (
	"regexp"
	"strings"
)

// watcherTokenRe matches a watcher invocation against the normalized form of
// a command (see normalizeToken). fm-watch is upstream's legacy script name,
// kept during the transition; cfo(.exe)? watch is the Windows-native form.
var watcherTokenRe = regexp.MustCompile(`(?i)\b(fm-watch|cfo(\.exe)?\s+watch)\b`)

// normalizeToken prepares command for watcher-token detection only: structure
// detection (separators, parens, redirections) uses the original string.
// Backslashes become forward slashes rather than being deleted, because
// deleting them would glue a Windows path into its neighbor and destroy the
// word boundary the token regexp needs (C:\dev\repo\cfo.exe would collapse
// into repocfo.exe). Quote characters are deleted so an ANSI-C quoted
// invocation like $'cfo watch' still exposes the token.
func normalizeToken(command string) string {
	s := strings.ToLower(command)
	s = strings.ReplaceAll(s, `\`, "/")
	return strings.NewReplacer("'", "", `"`, "", "`", "").Replace(s)
}

// hasTrailingBackground reports whether command ends with a lone background
// operator (&), as opposed to the && bundling operator.
func hasTrailingBackground(command string) bool {
	trimmed := strings.TrimRight(command, " \t")
	return strings.HasSuffix(trimmed, "&") && !strings.HasSuffix(trimmed, "&&")
}

// hasSinglePipe reports whether command contains a pipe (|) that is not part
// of the || bundling operator.
func hasSinglePipe(command string) bool {
	return strings.Contains(strings.ReplaceAll(command, "||", ""), "|")
}

func containsAnyFold(command string, needles ...string) bool {
	lower := strings.ToLower(command)
	for _, n := range needles {
		if strings.Contains(lower, n) {
			return true
		}
	}
	return false
}

func containsAny(command string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(command, n) {
			return true
		}
	}
	return false
}

// ClassifyArm decides whether command, the argument of a Bash tool call, must
// be denied because it invokes the watcher outside the Stop-owned auto-arm
// that is supposed to host it. A command with no watcher token allows
// immediately, whatever else it contains. A watcher-referencing command then
// runs a deny-code ladder, checked in order; v1 posture denies every
// watcher-family invocation, including ones that look benign, because the
// repair path for a down watcher is fixing the hook registration, never
// running the watcher from the agent shell.
func ClassifyArm(command string) (code, reason string, deny bool) {
	if !watcherTokenRe.MatchString(normalizeToken(command)) {
		return "", "", false
	}

	switch {
	case containsAnyFold(command, "pkill", "taskkill", "stop-process"):
		code = "broad-watcher-kill"
	case hasTrailingBackground(command) || containsAnyFold(command, "start-job", "start-process"):
		code = "watcher-background"
	case hasSinglePipe(command):
		code = "watcher-pipeline"
	case containsAny(command, ">"):
		code = "watcher-redirection"
	case containsAny(command, "&&", ";", "||"):
		code = "watcher-bundled"
	case containsAny(command, "$(", "`") || containsAnyFold(command, "eval", "bash -c", "powershell -command"):
		code = "watcher-nested"
	case containsAny(command, "$'", `$"`):
		code = "unclassifiable-protected-command"
	default:
		code = "watcher-direct"
	}
	return code, armReasons[code], true
}

var armReasons = map[string]string{
	"broad-watcher-kill":               "a broad process kill in the same command as a watcher invocation takes supervision down along with its intended target",
	"watcher-background":               "backgrounding the watcher orphans it from the Stop-owned auto-arm that is supposed to host it",
	"watcher-pipeline":                 "piping the watcher's output swallows the wake reason the auto-arm returns",
	"watcher-redirection":              "redirecting the watcher's output swallows the wake reason the auto-arm returns",
	"watcher-bundled":                  "bundling the watcher with other statements hides which half of the command supervision depends on",
	"watcher-nested":                   "nesting the watcher inside a substitution or an interpreter wrapper hides it from this guard's diagnostics",
	"unclassifiable-protected-command": "this command quotes a watcher invocation in a form this guard cannot classify safely",
	"watcher-direct":                   "the watcher is armed by the Stop-owned auto-arm hook, never from the agent shell",
}

const cwdRelocationReason = "Claude Code's Bash tool keeps its working directory between calls, so this relocation would outlive the tool call"

// relocationHeads are the command words ClassifyCd denies as a top-level
// statement head.
var relocationHeads = map[string]bool{
	"cd":    true,
	"pushd": true,
	"popd":  true,
}

// cdHead is one top-level-or-nested statement head found by statementHeads,
// paired with the paren depth it was found at.
type cdHead struct {
	word  string
	depth int
}

// ClassifyCd denies a command in which any top-level statement's command
// word is cd, pushd, popd, or Set-Location. Claude Code's Bash tool keeps its
// working directory between calls, so a relocation anywhere in the command
// outlives the tool call, not only in the final statement. A POSIX subshell
// relocation such as (cd sub && make) is exempt because it dies with the
// subshell; Set-Location inside parentheses is NOT exempt, because
// PowerShell parentheses are a grouping expression rather than a subshell
// and the relocation persists, so Set-Location denies regardless of depth.
func ClassifyCd(command string) (code, reason string, deny bool) {
	for _, h := range statementHeads(command) {
		lower := strings.ToLower(h.word)
		if lower == "set-location" || (h.depth == 0 && relocationHeads[lower]) {
			return "cwd-relocation", cwdRelocationReason, true
		}
	}
	return "", "", false
}

// statementHeads walks command once, tracking quote state and paren depth,
// and returns the first word of every statement (top-level and nested). It
// skips single-quoted, double-quoted and backtick-quoted spans before
// looking for the statement separators && ; || | & (doubled or lone) and an
// unquoted newline, so a separator inside a quoted argument never creates a
// phantom statement. A lone & and an unquoted \n or \r are statement
// separators exactly like ; - multi-line Bash is the single most common
// command shape Claude Code produces, and `cmd1 & cmd2` starts cmd2 as a new
// top-level statement just as surely as `cmd1 ; cmd2` does. depth is floored
// at zero on an unbalanced ')' so a stray close paren cannot make everything
// after it read as nested and exempt from the top-level relocation check
// (e.g. ") ; cd x").
//
// Consciously accepted trade-off: heredoc bodies are not quote-tracked, so a
// heredoc line beginning with "cd " now false-denies. That adds no new class
// of false block - a heredoc body containing "; cd" already false-denied
// before this change - and this guard's stated priority is zero false
// ALLOWS, never zero false denies.
func statementHeads(command string) []cdHead {
	var heads []cdHead
	var head strings.Builder
	var quote rune
	depth := 0
	expectingHead := true
	headDepth := 0

	boundary := func() {
		if head.Len() > 0 {
			heads = append(heads, cdHead{word: head.String(), depth: headDepth})
			head.Reset()
			expectingHead = false
		}
	}

	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if (r == '&' || r == '|') && i+1 < len(runes) && runes[i+1] == r {
			boundary()
			expectingHead, headDepth = true, depth
			i++
			continue
		}
		switch {
		case r == ';' || r == '|' || r == '&' || r == '\n' || r == '\r':
			boundary()
			expectingHead, headDepth = true, depth
		case r == '\'' || r == '"' || r == '`':
			boundary()
			quote = r
		case r == '(':
			boundary()
			depth++
		case r == ')':
			boundary()
			depth--
			if depth < 0 {
				depth = 0
			}
		case r == ' ' || r == '\t':
			boundary()
		case expectingHead:
			if head.Len() == 0 {
				headDepth = depth
			}
			head.WriteRune(r)
		}
	}
	boundary()
	return heads
}
