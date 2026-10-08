package monitor

import (
	"fmt"
	"regexp"
	"strings"
)

// askingPhrase matches a sentence that asks the CFO something or offers it a
// choice without a question mark: a choice it can take ("unless you want"),
// an offer ("want me to"), a wait on its answer ("Your answer decides",
// "Waiting for the CFO's choice") or a question the goblin says it asked. A
// goblin that writes one and stops waits on an answer. Each phrase was read
// off real turn endings of the fleet; a report that only names the CFO, such
// as "waiting for your merge", "parked per your decision" or "PR 301 still
// needs your answer" about a page it will file again, asks nothing.
var askingPhrase = regexp.MustCompile(`(?i)` + strings.Join([]string{
	`\bunless (you|the CFO)\b`,
	`\bif you(?:'d| would) rather\b`,
	`\bif you prefer\b`,
	`\bif you(?:'d like| would like| want) me to\b`,
	`\b(?:do|would) you (?:want|like|prefer)\b`,
	`\bwant me to\b`,
	`\b(?:should|shall) I\b`,
	`\blet me know\b`,
	`\bsay the word\b`,
	`\btell me (?:which|whether|what)\b`,
	`\bwhich (?:do|would) you\b`,
	`\bor would you\b`,
	`\b(?:it's|that's) your call\b`,
	`\bI(?:'ve| have)? asked\b[^.?!]*\b(?:whether|which)\b`,
	`\bwait(?:ing)? (?:for|on) (?:your|the CFO's) (?:answer|decision|choice|go-ahead|approval|call|word|reply|ok)\b`,
	`\byour (?:answer|decision|choice) (?:decides|will decide)\b`,
	`\bonce you (?:answer|decide|choose|pick|confirm|approve)\b`,
	`\bawaiting your\b`,
	`\boptions:`,
	`\(recommended\)`,
}, "|"))

// notWords are a sentence's code spans and links, which can hold a question
// mark or an asking phrase that is not the goblin's own words.
var notWords = regexp.MustCompile("`[^`]*`|https?://\\S+")

// paragraphBreak is a blank line between a reply's paragraphs.
var paragraphBreak = regexp.MustCompile(`\n\s*\n`)

// askedIn returns the sentences that ask the CFO something or offer it a
// choice in the last two paragraphs of reply, where a goblin that stops to
// ask puts its question, in reply's order; none for a reply that only
// reports. A sentence asks when it ends with a question mark or holds an
// asking phrase, read without its code spans and links.
func askedIn(reply string) []string {
	var paragraphs []string
	for _, paragraph := range paragraphBreak.Split(strings.ReplaceAll(strings.TrimSpace(reply), "\r\n", "\n"), -1) {
		if strings.TrimSpace(paragraph) != "" {
			paragraphs = append(paragraphs, paragraph)
		}
	}
	if len(paragraphs) > 2 {
		paragraphs = paragraphs[len(paragraphs)-2:]
	}
	var asked []string
	for _, paragraph := range paragraphs {
		for _, line := range strings.Split(paragraph, "\n") {
			for _, sentence := range sentences(line) {
				if asks(sentence) {
					asked = append(asked, sentence)
				}
			}
		}
	}
	return asked
}

// sentences splits one line of a reply into its sentences, as written but
// for the Markdown that only dresses them: a list marker and bold.
func sentences(line string) []string {
	line = strings.ReplaceAll(strings.TrimSpace(line), "**", "")
	line = strings.TrimSpace(strings.TrimLeft(line, "-*•"))
	var split []string
	start := 0
	for i := 0; i+1 < len(line); i++ {
		if strings.ContainsRune(".?!", rune(line[i])) && line[i+1] == ' ' {
			split = append(split, strings.TrimSpace(line[start:i+1]))
			start = i + 1
		}
	}
	if rest := strings.TrimSpace(line[start:]); rest != "" {
		split = append(split, rest)
	}
	return split
}

// asks reports whether sentence asks the CFO something or offers it a choice.
func asks(sentence string) bool {
	words := strings.ReplaceAll(notWords.ReplaceAllString(sentence, ""), "’", "'")
	if strings.HasSuffix(strings.TrimRight(words, " *_\"')”"), "?") {
		return true
	}
	return askingPhrase.MatchString(words)
}

// askQuoteLimit bounds the question a goblin_asks wake quotes.
const askQuoteLimit = 600

// askDetail is the goblin_asks wake: which goblin asked, what to do, and its
// question in its own words, which ends the wake so the board can read it
// back whole.
func askDetail(id string, asked []string) string {
	return fmt.Sprintf("%s ended its turn asking in prose instead of with cfo notify --blocked and waits at its prompt for the answer; next: answer it with cfo send %s \"<your answer>\" (cfo answer takes only a notify's question). It asked: \"%s\"",
		id, id, bounded(strings.Join(asked, " "), askQuoteLimit))
}
