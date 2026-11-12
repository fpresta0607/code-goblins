package claudestream

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// Start is what a stream session says of itself before it is given any work:
// the Claude Code that runs it, the Claude plan it is signed in to, how many
// of its SessionStart hooks ran, the CLAUDE.md files it loaded and whether it
// loaded skills. Claude Code's docs say --bare "will become the default for
// -p in a future release", and a bare session never reads the sign-in and
// skips hooks, skills, plugins, MCP servers, auto memory and CLAUDE.md, each
// of which a goblin runs on. So a goblin's stream session proves it is not
// bare before its instruction is written (ProveStart), and one that cannot
// starts in the terminal instead.
type Start struct {
	Version           string
	Subscription      string
	SessionStartHooks int
	MemoryFiles       []string
	HasSkills         bool
}

// The request ids of the two questions ProveStart asks.
const (
	initializeID   = "goblins-initialize"
	contextUsageID = "goblins-context-usage"
)

// ProveStart asks the session that write and next reach what it runs with,
// as Claude Code's initialize and get_context_usage requests, neither of
// which starts a model turn, and checks the answers: next returns each line
// the session writes, in order, and an error once it can read none. It
// returns what the session said and why it is not one a goblin can run on,
// nil when it is: one signed in to a Claude plan, at least one SessionStart
// hook of which ran, that lists skills and that loaded every CLAUDE.md of
// expected, each a path that exists. A Claude Code that starts -p sessions
// bare fails here, never silently later.
func ProveStart(write func(json.RawMessage) error, next func() (json.RawMessage, error), expected []string) (Start, error) {
	var start Start
	for _, request := range []struct{ id, subtype string }{{initializeID, "initialize"}, {contextUsageID, "get_context_usage"}} {
		if err := write(RequestLine(request.id, request.subtype, nil)); err != nil {
			return start, fmt.Errorf("claudestream: ask the session %s: %w", request.subtype, err)
		}
		for isAnswered := false; !isAnswered; {
			line, err := next()
			if err != nil {
				return start, fmt.Errorf("claudestream: read the session's answer to %s: %w", request.subtype, err)
			}
			if isAnswered, err = start.read(line, request.id); err != nil {
				return start, err
			}
		}
	}
	return start, start.check(expected)
}

// startLine is the members of a line that ProveStart reads.
type startLine struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	HookEvent string `json:"hook_event"`
	Response  struct {
		Subtype   string `json:"subtype"`
		RequestID string `json:"request_id"`
		Error     string `json:"error"`
		Response  struct {
			Version string `json:"claude_code_version"`
			Account struct {
				SubscriptionType string `json:"subscriptionType"`
			} `json:"account"`
			MemoryFiles []struct {
				Path string `json:"path"`
			} `json:"memoryFiles"`
			Skills json.RawMessage `json:"skills"`
		} `json:"response"`
	} `json:"response"`
}

// read folds line into s and reports whether it is the answer to requestID.
func (s *Start) read(line json.RawMessage, requestID string) (bool, error) {
	var read startLine
	if err := json.Unmarshal(line, &read); err != nil {
		return false, fmt.Errorf("claudestream: read a %d-byte line: %w", len(line), err)
	}
	if read.Type == "system" && read.Subtype == "hook_response" && read.HookEvent == "SessionStart" {
		s.SessionStartHooks++
	}
	if read.Type != "control_response" || read.Response.RequestID != requestID {
		return false, nil
	}
	if read.Response.Subtype != "success" {
		return true, fmt.Errorf("claudestream: the session refused request %s: %s", requestID, read.Response.Error)
	}
	answer := read.Response.Response
	switch requestID {
	case initializeID:
		s.Version, s.Subscription = answer.Version, answer.Account.SubscriptionType
	case contextUsageID:
		for _, file := range answer.MemoryFiles {
			s.MemoryFiles = append(s.MemoryFiles, file.Path)
		}
		s.HasSkills = len(answer.Skills) > 0 && string(answer.Skills) != "null"
	}
	return true, nil
}

// check names what s went without that a goblin runs on, each in his words,
// or is nil.
func (s Start) check(expected []string) error {
	var missing []string
	if s.Subscription == "" {
		missing = append(missing, "it is not signed in to a Claude plan")
	}
	if s.SessionStartHooks == 0 {
		missing = append(missing, "no SessionStart hook ran")
	}
	if !s.HasSkills {
		missing = append(missing, "it loaded no skills")
	}
	for _, path := range expected {
		if !slices.ContainsFunc(s.MemoryFiles, func(loaded string) bool { return strings.EqualFold(filepath.Clean(loaded), filepath.Clean(path)) }) {
			missing = append(missing, "it did not load "+path)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("claudestream: Claude Code %s started the session without what a goblin runs on, as --bare does: %s", s.Version, strings.Join(missing, "; "))
}
