// Package routing reads the fleet's standing policy: the execution lanes a
// spawn routes through (which harness, model and effort each kind of work
// gets), and what to do when a goblin's harness starts erroring, which
// provider failures are recognized, and which of them the Supreme Overlord
// has already decided the answer to.
package routing

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// FileName is the policy file under the CFO home's data directory.
const FileName = "routing.json"

// Fault names a kind of provider failure seen in a goblin's pane.
type Fault string

const (
	// RateLimit is the provider refusing on quota: the standing case the
	// Overlord has a rule for.
	RateLimit Fault = "rate-limit"
	// Auth is a rejected or expired credential.
	Auth Fault = "auth"
	// Provider is the model service failing on its own side (a 5xx, an
	// overload, a gateway error). Switching harnesses may help here.
	Provider Fault = "provider"
	// ThirdParty is a git-platform (GitHub and friends) rate limit or
	// outage. It is the platform's own problem, never the model provider's,
	// so it must never route to a harness switch.
	ThirdParty Fault = "third-party"
)

// faultPatterns are matched against a pane's tail, lowercased. Order matters:
// a quota message that also carries a 403 is a rate limit, not an auth
// failure, so the rate-limit patterns are tried first.
//
// Every pattern is a phrase a provider actually emits, never a bare status
// code: "429" alone matches "line 429 of parser.go" and would report a
// healthy goblin as rate-limited.
//
// ponytail: substring matching over a pane tail. Every rate-limit phrase also
// turns up in a goblin's prose, in a harness's own dialogs and in settings
// names, so Detect takes one only on a line shaped like a provider's refusal
// (see errorShaped); the auth and provider patterns keep the bounded
// substring ceiling (a wake carrying a recommendation).
var faultPatterns = []struct {
	fault    Fault
	patterns []string
}{
	{RateLimit, []string{
		"rate limit", "rate_limit", "ratelimit_error", "too many requests",
		"quota exceeded", "insufficient quota", "over quota", "out of quota",
		"usage limit",
	}},
	{Auth, []string{
		"401 unauthorized", "invalid api key", "invalid_api_key",
		"authentication_error", "authentication failed", "api key not valid",
		"invalid x-api-key",
	}},
	{Provider, []string{
		"internal server error", "502 bad gateway", "503 service unavailable",
		"service unavailable", "overloaded_error", "upstream connect error",
	}},
}

// SteerPrefix is stamped on every line the CFO delivers into a goblin's pane
// with cfo send, and OverlordPrefix is what the Overlord types before a line
// of their own. Detect blanks lines carrying either, and the indented
// continuation lines a pane wraps a long prompt into, before it looks for a
// fault: text an operator wrote about a provider is not evidence about the
// harness. The sender (fleet.Sender.Text) stamps with this same constant and
// its test asserts the stamp through it, so a prefix change here changes the
// stamp and the exclusion together rather than leaving one behind.
const (
	SteerPrefix    = "CFO: "
	OverlordPrefix = "Overlord: "
)

// Detect reports the provider fault a pane's tail shows, if any. It reads the
// tail the watcher already captured rather than probing anything. Operator
// lines are blanked first, then third-party platform failures (GitHub and
// other git hosts) are checked before the model provider's own faults,
// because a platform rate limit or 5xx is a wait/backoff case that must never
// route to a harness switch.
func Detect(paneTail string) (Fault, string, bool) {
	lowered := redactOperatorLines(strings.ToLower(paneTail))
	if index, ok := thirdPartyFault(lowered); ok {
		return ThirdParty, evidence(paneTail, index), true
	}
	for _, group := range faultPatterns {
		for _, pattern := range group.patterns {
			// Every occurrence is weighed, so prose above a real refusal
			// cannot hide it.
			for _, index := range allMatches(lowered, pattern) {
				if group.fault == RateLimit && !errorShaped(lineAt(lowered, index)) {
					continue
				}
				return group.fault, evidence(paneTail, index), true
			}
		}
	}
	return "", "", false
}

// thirdPartyMarkers name a git platform or its CI on a line: GitHub Actions
// and a workflow run are the platform too, since an Actions runner refused
// on quota is GitHub's rate limit, never the harness's.
var thirdPartyMarkers = []string{"github", "api.github.com", "gitlab", "bitbucket", "actions run", "actions workflow", "actions runner", "workflow run"}

// thirdPartyFault recognizes a git-platform (GitHub and friends) rate limit or
// outage: those are the platform's own quota, not the model provider's, so the
// recommended action is wait/backoff rather than a harness switch. Every
// occurrence of a marker is examined, not only the first: a pane that names
// a pull request URL early and reports an API rate limit later must read
// the later line, or the provider rule below claims it as a harness fault.
func thirdPartyFault(lowered string) (int, bool) {
	for _, marker := range thirdPartyMarkers {
		for _, index := range allMatches(lowered, marker) {
			if thirdPartyLine(lowered, index) {
				return index, true
			}
		}
	}
	// gh CLI errors begin a line with "gh:"; a bare
	// substring would also flag words like "high:", "weigh:", or "sigh:".
	for _, index := range lineStartMatches(lowered, "gh:") {
		if thirdPartyLine(lowered, index) {
			return index, true
		}
	}
	return 0, false
}

// httpStatusForm is a status code written the way an HTTP client prints one,
// as gh does ("HTTP 502: 502 Bad Gateway"): after the word http, or directly
// before a colon and not as part of a path, reference or longer token.
var httpStatusForm = regexp.MustCompile(`\bhttp (?:429|502|503)\b|(?:^|[^a-z0-9/#-])(?:429|502|503):`)

// thirdPartyLine reports whether the line containing index is a git-platform
// outage: a framed status code, or a prose keyword with error framing.
func thirdPartyLine(lowered string, index int) bool {
	line := lineAt(lowered, index)
	// A git host line names pull request, issue and run numbers all the time,
	// so a status code counts only when it is framed as one: written in HTTP
	// status form, or beside an HTTP status phrase or an error word.
	if httpStatusForm.MatchString(line) {
		return true
	}
	framed := thirdPartyErrorWord(line, "")
	for _, phrase := range []string{"bad gateway", "service unavailable", "too many requests", "gateway timeout", "internal server error"} {
		framed = framed || strings.Contains(line, phrase)
	}
	for _, code := range []string{"429", "503", "502"} {
		if framed && hasStatusCode(line, code) {
			return true
		}
	}
	// A prose keyword must carry a status code or error word on the same
	// line, so a conversational mention of a git host is not an outage.
	for _, indicator := range []string{"rate limit", "secondary rate", "exceeded", "abuse"} {
		if strings.Contains(line, indicator) && thirdPartyFramed(line, indicator) {
			return true
		}
	}
	return false
}

// hasStatusCode reports whether code stands on the line as a whole token, not
// inside a longer number or word and not a path segment, reference or suffix:
// a run id such as 17742950312, a commit hash, /pull/502, #429 or issue-503
// is not a status.
func hasStatusCode(line, code string) bool {
	isWordByte := func(b byte) bool {
		return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
	}
	for _, index := range allMatches(line, code) {
		end := index + len(code)
		if (index == 0 || !isWordByte(line[index-1]) && !strings.ContainsRune("/#-", rune(line[index-1]))) && (end == len(line) || !isWordByte(line[end])) {
			return true
		}
	}
	return false
}

// allMatches returns every index at which needle occurs.
func allMatches(haystack, needle string) []int {
	var indexes []int
	for start := 0; start < len(haystack); {
		index := strings.Index(haystack[start:], needle)
		if index < 0 {
			return indexes
		}
		indexes = append(indexes, start+index)
		start += index + len(needle)
	}
	return indexes
}

// redactOperatorLines blanks every line an operator wrote, a cfo send steer
// or an Overlord line, together with the indented lines a pane wraps the
// rest of that prompt into. An indented line that opens with a harness result
// or turn marker (⎿ ● ✻ ◐ ⏺ ❯ › >) is the harness answering, not the prompt
// wrapping, so it ends the redaction. Lengths are kept, so an index into the
// result still names the same place in the original tail. A pane's own
// prompt markers before the prefix are ignored.
func redactOperatorLines(lowered string) string {
	lines := strings.Split(lowered, "\n")
	redacting := false
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t>›❯│")
		switch {
		case strings.HasPrefix(trimmed, strings.ToLower(SteerPrefix)) || strings.HasPrefix(trimmed, strings.ToLower(OverlordPrefix)):
			redacting = true
		case redacting && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) && strings.IndexAny(strings.TrimLeft(line, " \t"), "⎿●✻◐⏺❯›>") != 0:
		default:
			redacting = false
		}
		if redacting {
			lines[i] = strings.Repeat(" ", len(line))
		}
	}
	return strings.Join(lines, "\n")
}

// lineStartMatches returns the indexes where needle begins a line, after any
// leading whitespace and the harness glyphs a pane prints output under (the
// ones redactOperatorLines knows), as in Claude Code's " ⎿ gh: ...".
func lineStartMatches(haystack, needle string) []int {
	var indexes []int
	for start := 0; start < len(haystack); {
		index := strings.Index(haystack[start:], needle)
		if index < 0 {
			return indexes
		}
		index += start
		lineStart := strings.LastIndexByte(haystack[:index], '\n') + 1
		if strings.TrimLeft(haystack[lineStart:index], " \t⎿●✻◐⏺❯›>│") == "" {
			indexes = append(indexes, index)
		}
		start = index + len(needle)
	}
	return indexes
}

// thirdPartyFramed reports whether a line carries error framing beyond the
// matched keyword itself: a status code or an error word. A prose keyword on
// its own (a CFO steer saying "not a github rate limit") is not an outage.
func thirdPartyFramed(line, keyword string) bool {
	for _, code := range []string{"429", "403", "503", "502"} {
		if hasStatusCode(line, code) {
			return true
		}
	}
	return thirdPartyErrorWord(line, keyword)
}

// thirdPartyErrorWord reports whether a line carries a git-platform error
// word other than the matched keyword itself.
func thirdPartyErrorWord(line, keyword string) bool {
	for _, signal := range []string{"error", "refused", "failed", "quota", "reached", "exceeded", "unable", "denied", "forbidden", "fatal", "abuse"} {
		if signal != keyword && strings.Contains(line, signal) {
			return true
		}
	}
	return false
}

// errorShaped reports whether a line is shaped like a provider's own refusal
// rather than prose about one: a 429 or 403 status, a provider's error type,
// a harness's API error, or a retry or reset time. An error word is not a
// shape: "errors, and rate limits" is a goblin listing doc topics.
func errorShaped(line string) bool {
	for _, code := range []string{"429", "403"} {
		if hasStatusCode(line, code) {
			return true
		}
	}
	for _, shape := range []string{"rate_limit_error", "ratelimit_error", "insufficient_quota", "resource_exhausted", "api error", "retry-after", "retry after", "retrying in", "try again in", "try again at", "try again later", "will reset", "resets at", "resets in", "limit reached"} {
		if strings.Contains(line, shape) {
			return true
		}
	}
	return false
}

// lineAt returns the single line of text containing index.
func lineAt(text string, index int) string {
	start := strings.LastIndexByte(text[:index], '\n') + 1
	end := strings.IndexByte(text[index:], '\n')
	if end < 0 {
		end = len(text)
	} else {
		end += index
	}
	return text[start:end]
}

// evidence returns the line the match landed on, so a report can quote what
// the provider actually said instead of only naming the pattern.
func evidence(tail string, index int) string {
	start := strings.LastIndexByte(tail[:index], '\n') + 1
	end := strings.IndexByte(tail[index:], '\n')
	if end < 0 {
		end = len(tail)
	} else {
		end += index
	}
	line := strings.TrimSpace(tail[start:end])
	if len(line) > 200 {
		line = line[:200]
	}
	return line
}

// Switch is the harness, model, and effort a rule routes to.
type Switch struct {
	Harness string `json:"harness"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
}

// Rule is one standing decision: when this harness hits this fault, go here.
type Rule struct {
	// Harness the rule applies to. Empty matches any harness.
	Harness string `json:"harness,omitempty"`
	// Fault the rule answers.
	Fault Fault `json:"fault"`
	// Switch is where the task goes.
	Switch Switch `json:"switch"`
	// Auto lets the watcher act without asking. Without it the rule is a
	// recommendation the CFO is woken with, which is the safer default for a
	// policy that restarts someone's harness.
	Auto bool `json:"auto,omitempty"`
	// ForceDirty renders --force-dirty in the rule's switch command, so a
	// standing answer still works on a goblin that hit its fault mid-work
	// with uncommitted changes.
	ForceDirty bool `json:"force_dirty,omitempty"`
	// Note explains the rule to whoever reads the file next.
	Note string `json:"note,omitempty"`
}

// Lane is one execution profile: the harness, model and effort a kind of
// work runs on. Model is handed to the harness untouched, so it takes
// whatever the harness takes (claude: fable, opus, sonnet, haiku, or a full
// model id); there is deliberately no allowlist here.
type Lane struct {
	Harness string `json:"harness"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
	// Note says what the lane is for, to whoever reads the file or
	// `cfo doctor` next.
	Note string `json:"note,omitempty"`
}

// Policy is the whole standing routing table.
type Policy struct {
	Rules []Rule `json:"rules"`
	// Lanes are the fleet's execution lanes by name. They are a property of
	// this machine and its subscriptions, like the shared cache roots, so
	// they live here rather than in any one project's manifest; a
	// manifest's routing block overrides them for that project.
	Lanes map[string]Lane `json:"lanes,omitempty"`
	// DefaultLane runs ordinary work; EscalateTo runs high-risk work and
	// retries. Both must name a defined lane.
	DefaultLane string `json:"default_lane,omitempty"`
	EscalateTo  string `json:"escalate_to,omitempty"`
	// Path is where it was loaded from; not serialized.
	Path string `json:"-"`
}

// Load reads the policy from a CFO home's data directory. A missing file is
// not an error: a fleet with no standing rules simply wakes the CFO for every
// fault, which is the behaviour without this package at all.
func Load(dataDir string) (Policy, error) {
	path := filepath.Join(dataDir, FileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Policy{Path: path}, nil
	}
	if err != nil {
		return Policy{}, err
	}
	var policy Policy
	if err := json.Unmarshal(data, &policy); err != nil {
		return Policy{}, fmt.Errorf("routing: %s: %w", path, err)
	}
	policy.Path = path
	for index, rule := range policy.Rules {
		if rule.Fault == "" {
			return Policy{}, fmt.Errorf("routing: %s: rule %d has no fault", path, index)
		}
		if rule.Switch.Harness == "" {
			return Policy{}, fmt.Errorf("routing: %s: rule %d for %s does not say what to switch to", path, index, rule.Fault)
		}
	}
	return policy, nil
}

// Table is the lane table one spawn routes through: the fleet's, or a
// project manifest's override of it.
type Table struct {
	Lanes       map[string]ExecutionLane
	DefaultLane string
	EscalateTo  string
	// Source says where the table came from, for the spawn report.
	Source string
}

// LaneTable returns the fleet's lane table, refusing one a spawn could not
// route through: a lane with no harness, lanes without a default, or a
// default or escalation that names no lane. Load does not check this, so a
// lane typo never costs the watcher its standing switch rules.
func (p Policy) LaneTable() (Table, error) {
	for name, lane := range p.Lanes {
		if lane.Harness == "" {
			return Table{}, fmt.Errorf("routing: %s: lane %q has no harness", p.Path, name)
		}
	}
	if len(p.Lanes) > 0 && p.DefaultLane == "" {
		return Table{}, fmt.Errorf("routing: %s: lanes are defined but default_lane is not", p.Path)
	}
	if _, ok := p.Lanes[p.DefaultLane]; p.DefaultLane != "" && !ok {
		return Table{}, fmt.Errorf("routing: %s: default_lane %q is not a defined lane", p.Path, p.DefaultLane)
	}
	if _, ok := p.Lanes[p.EscalateTo]; p.EscalateTo != "" && !ok {
		return Table{}, fmt.Errorf("routing: %s: escalate_to %q is not a defined lane", p.Path, p.EscalateTo)
	}
	t := Table{Lanes: map[string]ExecutionLane{}, DefaultLane: p.DefaultLane, EscalateTo: p.EscalateTo, Source: "fleet table " + p.Path}
	for name, lane := range p.Lanes {
		t.Lanes[name] = ExecutionLane{Name: name, Harness: lane.Harness, Model: lane.Model, Effort: lane.Effort, Note: lane.Note}
	}
	return t, nil
}

// Match returns the rule that answers this harness hitting this fault. The
// first matching rule wins, and a harness-specific rule is preferred over a
// catch-all regardless of file order.
func (p Policy) Match(harness string, fault Fault) (Rule, bool) {
	// A third-party (git platform) outage is a wait/backoff case, never a
	// harness-switch case, so no standing rule can answer it.
	if fault == ThirdParty {
		return Rule{}, false
	}
	var fallback Rule
	found := false
	for _, rule := range p.Rules {
		if rule.Fault != fault {
			continue
		}
		if strings.EqualFold(rule.Harness, harness) {
			return rule, true
		}
		if rule.Harness == "" && !found {
			fallback, found = rule, true
		}
	}
	return fallback, found
}

// Command renders the cfo switch command a rule calls for, which is what the
// CFO is shown when the rule is a recommendation rather than an automatic.
func (r Rule) Command(id string) string {
	command := "cfo switch " + id + " --harness " + r.Switch.Harness
	if r.Switch.Model != "" {
		command += " --model " + r.Switch.Model
	}
	if r.Switch.Effort != "" {
		command += " --effort " + r.Switch.Effort
	}
	if r.ForceDirty {
		command += " --force-dirty"
	}
	return command
}
