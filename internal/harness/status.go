package harness

import (
	"strconv"
	"strings"
)

// Status is what a harness's terminal shows over its composer: the context
// gauge, in its own words, and the notices after it (#2236).
type Status struct {
	Gauge   string   `json:"gauge,omitempty"`
	Notices []Notice `json:"notices,omitempty"`
}

// Notice is one notice of a status, without its mark, and the check of the
// harness's own that the supervisor runs for it outside the session, if it
// has one, as "update" for a failed update (#2333).
type Notice struct {
	Text  string `json:"text"`
	Check string `json:"check,omitempty"`
}

// Retry is a turn waiting to send a request that failed again: the failure,
// as the harness names it, and its attempt of the attempts it makes.
type Retry struct {
	Reason   string `json:"reason"`
	Attempt  int    `json:"attempt"`
	Attempts int    `json:"attempts"`
}

// Status reads the status at the end of the row over screen's composer, after
// whatever the row starts with, as a turn row in a narrow terminal, and false
// while none shows there.
func (s Screens) Status(screen []string) (Status, bool) {
	top, _, isRuled := s.composerRules(screen)
	if s.StatusLine == nil || !isRuled || top == 0 {
		return Status{}, false
	}
	line := strings.TrimSpace(screen[top-1])
	if gap := strings.LastIndex(line, "   "); gap >= 0 {
		line = strings.TrimSpace(line[gap:])
	}
	match := s.StatusLine.FindStringSubmatch(line)
	if match == nil || match[1] == "" && match[2] == "" {
		return Status{}, false
	}
	status := Status{Gauge: match[1]}
	for part := range strings.SplitSeq(match[2], " · ") {
		text := strings.TrimSpace(strings.TrimLeft(part, "✘× "))
		if text == "" {
			continue
		}
		notice := Notice{Text: text, Check: s.check(text)}
		// What the terminal says to do about a failure the supervisor checks
		// itself is that failure's own, and no second notice.
		if last := len(status.Notices) - 1; notice.Check != "" && last >= 0 && status.Notices[last].Check == notice.Check {
			continue
		}
		status.Notices = append(status.Notices, notice)
	}
	return status, true
}

// check is the check the supervisor runs for notice, also when the terminal
// cut the notice short with an ellipsis, as "Run claude do…", once most of
// its words show.
func (s Screens) check(notice string) string {
	cut, isCut := strings.CutSuffix(notice, "…")
	for words, check := range s.Checks {
		if notice == words || isCut && len(cut) >= len(words)*2/3 && strings.HasPrefix(words, cut) {
			return check
		}
	}
	return ""
}

// Retry reads the turn row of screen while it waits to retry, and false while
// no turn shows or its turn row shows anything else.
func (s Screens) Retry(screen []string) (Retry, bool) {
	row, isTurn := s.turnRow(screen)
	if !isTurn || s.Retrying == nil {
		return Retry{}, false
	}
	match := s.Retrying.FindStringSubmatch(row)
	if match == nil {
		return Retry{}, false
	}
	attempt, attemptErr := strconv.Atoi(match[2])
	attempts, attemptsErr := strconv.Atoi(match[3])
	if attemptErr != nil || attemptsErr != nil {
		return Retry{}, false
	}
	return Retry{Reason: match[1], Attempt: attempt, Attempts: attempts}, true
}
