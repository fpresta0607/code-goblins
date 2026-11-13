// Package turnnudge tells a goblin once when its turn ends on a promise of an
// action it never took, such as "I will now run the tests" with no tool call
// after it (issue #2364). Without it the goblin sat idle until the
// supervisor's idle wake reached the CEO. A Claude Code or Codex goblin's
// Stop hook answers with Reply, and a pi goblin's extension asks the same of
// goblins hook goblin-turn-end before its run settles.
//
// A nudge that should not have come costs the goblin one short turn, in
// which it ends its turn again, and nothing else: it changes no record, wakes
// no one, and the turn it continues is never nudged a second time.
package turnnudge

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
)

// Message is what a goblin whose turn ended on a promise is told, as the
// prompt its harness continues the turn with.
const Message = "Your turn ended on a promise to act, with no tool call after it. " +
	"If you meant to do it, do it now. " +
	"If this turn is meant to end here, as on a question to the CEO, a done report or a wait your brief asked for, end it again in one line: this reminder comes once a turn."

// Stop is the part of a harness's Stop hook payload the nudge reads. Claude
// Code and Codex both write last_assistant_message and stop_hook_active, the
// latter true while the turn already goes on because a Stop hook blocked its
// end; Claude Code also names the background work and session timers that
// will wake the session, which Codex has none of.
type Stop struct {
	Event                string            `json:"hook_event_name"`
	LastAssistantMessage string            `json:"last_assistant_message"`
	IsStopHookActive     bool              `json:"stop_hook_active"`
	BackgroundTasks      []json.RawMessage `json:"background_tasks"`
	SessionCrons         []json.RawMessage `json:"session_crons"`
}

// ShouldNudge reports whether the turn this Stop ends is told Message: a
// turn's own end, not one a Stop hook already continued, with nothing in
// the background to wake the session, whose last reply promises an action.
func (s Stop) ShouldNudge() bool {
	return s.Event == "Stop" && !s.IsStopHookActive && len(s.BackgroundTasks) == 0 && len(s.SessionCrons) == 0 && Promises(s.LastAssistantMessage)
}

// block is the Stop hook answer that keeps a turn going with reason as its
// next prompt, which Claude Code and Codex read on stdout with exit 0.
type block struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// Reply is a Stop hook's answer to payload, the JSON its harness wrote it:
// a block carrying Message when the Stop is nudged, and otherwise an empty
// object, which lets the turn end. A payload that cannot be read lets it end.
func Reply(payload []byte) string {
	var stop Stop
	if json.Unmarshal(payload, &stop) != nil || !stop.ShouldNudge() {
		return "{}"
	}
	data, err := json.Marshal(block{Decision: "block", Reason: Message})
	if err != nil {
		return "{}"
	}
	return string(data)
}

// fencedCode matches a fenced code block, or one left open at the reply's
// end: what it shows is not the goblin's own words.
var fencedCode = regexp.MustCompile("(?s)```.*?(```|$)")

// sentenceEnd ends a sentence, and clauseEnd a clause within one. goAhead
// is taken out before a sentence is split, so "I'll go ahead and commit"
// stays one clause.
var (
	sentenceEnd = regexp.MustCompile(`[.!;]+(\s|$)`)
	clauseEnd   = regexp.MustCompile(`(?i)[,:]\s|\s(and|then)\s|:$`)
	goAhead     = regexp.MustCompile(`(?i)\bgo ahead and\s`)
)

// Promises reports whether reply, the last words of a turn, end on a promise
// of an action: a clause of its last paragraph that opens with "I will",
// "I'll", "I'm going to", "I am about to", "Let me", "Let's" or "Time to" and
// a verb of doing, as in "Now let me run the tests", or a clause such as
// "Running the full suite now". A last paragraph that asks anything promises
// nothing, since the goblin waits for its answer. Nor does a wait, a clause
// whose verb is one such as wait, stand by, hold, watch or report back, or a
// sentence that holds its action until something else happens ("once CI is
// green", "when the CEO answers", "later"), and what follows either in the
// paragraph is held by it: "I'll wait for CI. Then I'll merge." promises
// nothing of this turn.
func Promises(reply string) bool {
	paragraph := lastParagraph(reply)
	if paragraph == "" || strings.Contains(paragraph, "?") {
		return false
	}
	for _, line := range strings.Split(paragraph, "\n") {
		for _, sentence := range sentenceEnd.Split(line, -1) {
			if defers(wordsOf(sentence)) {
				return false
			}
			for _, clause := range clauseEnd.Split(goAhead.ReplaceAllString(sentence, ""), -1) {
				isPromise, isWait := readClause(wordsOf(clause))
				if isPromise || isWait {
					return isPromise
				}
			}
		}
	}
	return false
}

// lastParagraph is the last block of non-blank lines of reply, once its
// fenced code is taken out, so "I'll run:" above a code block is read.
func lastParagraph(reply string) string {
	text := strings.TrimSpace(fencedCode.ReplaceAllString(strings.ReplaceAll(reply, "\r\n", "\n"), ""))
	lines := strings.Split(text, "\n")
	start := len(lines) - 1
	for start > 0 && strings.TrimSpace(lines[start-1]) != "" {
		start--
	}
	return strings.Join(lines[start:], "\n")
}

// wordsOf is text as lower-case words, with a typographic apostrophe read as
// a plain one and the markdown and quotes around each word taken off.
func wordsOf(text string) []string {
	var words []string
	for _, field := range strings.Fields(strings.ToLower(strings.ReplaceAll(text, "’", "'"))) {
		if word := strings.Trim(field, "-*_`\"'()[]>#+.,:;!“”"); word != "" {
			words = append(words, word)
		}
	}
	return words
}

// openers are the words a promise opens with.
var openers = [][]string{
	{"i", "will"}, {"i'll"},
	{"i", "am", "going", "to"}, {"i'm", "going", "to"}, {"going", "to"},
	{"i", "am", "about", "to"}, {"i'm", "about", "to"}, {"about", "to"},
	{"let", "me"}, {"let's"}, {"let", "us"},
	{"time", "to"},
}

// fillers are the words a clause or its verb may stand behind: "Now, let
// me", "OK so I'll", "I'll now just".
var fillers = []string{"now", "next", "then", "so", "ok", "okay", "alright", "right", "first", "great", "good", "also", "and", "but", "finally", "just", "quickly", "immediately", "still"}

// idleVerbs are the verbs after an opener that promise no action of the
// turn's own: a wait, an end, or words to someone else ("let me know").
var idleVerbs = []string{"wait", "await", "stand", "hold", "pause", "idle", "sit", "sleep", "stop", "end", "leave", "be", "know", "hear", "keep", "watch", "monitor", "rest", "remain", "stay", "let", "hand", "not", "never"}

// backVerbs are the verbs that, with "back", say the goblin comes back to
// this later: "I'll report back", "Let me get back to you".
var backVerbs = []string{"report", "get", "check", "come", "circle", "call", "write"}

// waitGerunds are a wait in -ing, as "Waiting on CI now" or "Standing by"
// opens with, and nonVerbs the words ending in -ing that are no verb.
var (
	waitGerunds = []string{"waiting", "awaiting", "standing", "holding", "pausing", "idling", "sitting", "sleeping", "stopping", "ending", "leaving", "keeping", "watching", "monitoring", "resting", "remaining", "staying"}
	nonVerbs    = []string{"nothing", "something", "anything", "everything", "thing", "string", "pending", "being"}
)

// askingVerbs ask whether rather than hold an action: "Let me check if it
// passed" asks, where "I'll merge if it passes" holds the merge.
var askingVerbs = []string{"check", "see", "verify", "confirm", "test", "know", "ask", "find", "out", "look"}

// defers reports whether words, one sentence, hold its action until
// something else happens or for another time.
func defers(words []string) bool {
	for at, word := range words {
		previous, next := "", ""
		if at > 0 {
			previous = words[at-1]
		}
		if at+1 < len(words) {
			next = words[at+1]
		}
		switch word {
		case "when", "if":
			if !slices.Contains(askingVerbs, previous) {
				return true
			}
		case "once":
			if next != "more" && next != "again" {
				return true
			}
		case "whenever", "after", "afterwards", "until", "till", "unless", "upon", "later", "tomorrow", "eventually":
			return true
		}
	}
	return hasPhrase(words, "as", "soon", "as") || hasPhrase(words, "next", "time")
}

// readClause reads words, one clause: isPromise when they open with an
// opener and a verb of doing, or a verb of doing in -ing with "now", and
// isWait when the opener's verb is one of waiting or the -ing is.
func readClause(words []string) (isPromise, isWait bool) {
	rest := skipFillers(words)
	for _, opener := range openers {
		if len(rest) > len(opener) && slices.Equal(rest[:len(opener)], opener) {
			action := skipFillers(rest[len(opener):])
			if len(action) == 0 {
				return false, false
			}
			isWait = slices.Contains(idleVerbs, action[0]) || (len(action) > 1 && action[1] == "back" && slices.Contains(backVerbs, action[0]))
			return !isWait, isWait
		}
	}
	switch {
	case len(rest) == 0 || len(rest[0]) <= 4 || !strings.HasSuffix(rest[0], "ing") || slices.Contains(nonVerbs, rest[0]):
		return false, false
	case slices.Contains(waitGerunds, rest[0]):
		return false, true
	}
	return slices.Contains(words, "now"), false
}

// skipFillers is words after the fillers they open with.
func skipFillers(words []string) []string {
	for len(words) > 0 && slices.Contains(fillers, words[0]) {
		words = words[1:]
	}
	return words
}

// hasPhrase reports whether words hold phrase, word for word.
func hasPhrase(words []string, phrase ...string) bool {
	for at := 0; at+len(phrase) <= len(words); at++ {
		if slices.Equal(words[at:at+len(phrase)], phrase) {
			return true
		}
	}
	return false
}
