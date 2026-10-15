package tickets

import (
	"regexp"
	"strings"
)

// The roots the fleet files by itself, from its own signals that already
// name a cause. Each is keyed by that cause, so a later sighting comments on
// the issue the first one opened.

// FailedOnceRoot is the root issue of a test that failed once and passed on
// its second try, as a workflow names it in a warning, "job: suite test".
// The job is left out of its key, since the same test failing by chance in
// another job is the same root.
func FailedOnceRoot(named, evidence string) Root {
	test := strings.TrimSpace(named)
	if _, after, isJob := strings.Cut(test, ": "); isJob {
		test = strings.TrimSpace(after)
	}
	suite, name := "", test
	if at := strings.LastIndex(test, " "); at > 0 {
		suite, name = test[:at], test[at+1:]
	}
	short := suite[strings.LastIndex(suite, "/")+1:]
	where := "`" + name + "`"
	if suite != "" {
		where += " in `" + suite + "`"
	}
	return Root{
		Title:    strings.TrimSpace(short+" "+name) + " fails by chance: it failed once and passed on its second try",
		Body:     "## What happens\n\n" + where + " failed once and passed on its second try, so the run it was in went green with a failure by chance inside it.\n\n## Done when\n\nThe cause of the failure by chance is found and fixed, with the test failing on the old code under load and passing after the fix.",
		Key:      "test:" + test,
		Evidence: evidence,
	}
}

// RedMainRoot is the root issue of a workflow whose push run on a
// repository's default branch failed in jobs, as the supervisor names them:
// "job a" or "jobs a, b".
func RedMainRoot(workflow, jobs, evidence string) Root {
	return Root{
		Title:    workflow + " is red on the default branch: " + jobs,
		Body:     "## What happens\n\nThe push run of workflow `" + workflow + "` on the default branch failed, " + jobs + ".\n\n## Done when\n\nThe cause is fixed and the workflow passes on the default branch again.",
		Key:      "ci:" + workflow + ": " + jobs,
		Evidence: evidence,
	}
}

var (
	errorPath   = regexp.MustCompile(`(?i)(?:\b[a-z]:[\\/]|\\\\|/[a-z]/)\S*`)
	errorQuoted = regexp.MustCompile(`"[^"]*"|'[^']*'`)
	errorHex    = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	errorNumber = regexp.MustCompile(`\d+`)
)

// ErrorClass is a line of the supervisor's errors with what changes from one
// sighting to the next taken out: paths, quoted text, hashes and numbers. Two
// lines of one class are one root.
func ErrorClass(line string) string {
	class := errorPath.ReplaceAllString(line, "<path>")
	class = errorQuoted.ReplaceAllString(class, "<text>")
	class = errorHex.ReplaceAllString(class, "<hash>")
	class = errorNumber.ReplaceAllString(class, "N")
	return strings.Join(strings.Fields(class), " ")
}

// RepeatedErrorRoot is the root issue of an error the supervisor met again
// long after it first met it, by its class.
func RepeatedErrorRoot(class, evidence string) Root {
	title := class
	if len(title) > 80 {
		title = title[:80]
		if at := strings.LastIndex(title, " "); at > 0 {
			title = title[:at]
		}
		title += "..."
	}
	return Root{
		Title:    "The supervisor meets the same error again: " + title,
		Body:     "## What happens\n\nThe supervisor of a Code Goblins home met this error again an hour or more after it first met it:\n\n```\n" + class + "\n```\n\n## Done when\n\nIts cause is found and fixed, with a test that reproduces it, so the supervisor stops meeting it.",
		Key:      "error:" + class,
		Evidence: evidence,
	}
}
