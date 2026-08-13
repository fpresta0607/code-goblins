// Package crewstate resolves the current task state from typed metadata, a
// verified Herdr endpoint, and only then the append-only status event log.
package crewstate

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type State string
type Source string

const (
	Working State = "working"
	Parked  State = "parked"
	Done    State = "done"
	Blocked State = "blocked"
	Paused  State = "paused"
	Failed  State = "failed"
	Unknown State = "unknown"
)

const (
	SourceNone     Source = "none"
	SourceMetadata Source = "metadata"
	SourceEndpoint Source = "endpoint"
	SourceStatus   Source = "status"
)

type Current struct {
	State  State
	Source Source
	Detail string
}

// Endpoint supplies base liveness only. Its Exists result proves that the
// recorded target is addressable, not that it is the exact task endpoint.
type Endpoint interface {
	Exists(ctx context.Context, target herdr.Target) (bool, error)
	BusyState(ctx context.Context, target herdr.Target) (herdr.BusyState, error)
}

// StructuralValidator is an intentionally narrow optional extension for
// endpoints that can prove every metadata identity component. Status-log
// fallback is never allowed without this proof.
type StructuralValidator interface {
	Validate(ctx context.Context, meta state.TaskMeta) (bool, error)
}

// Decision is one still-open keyed decision from an append-only status log.
type Decision struct {
	Key    string
	Verb   string
	Detail string
}

// Resolve applies the Plan 3 precedence order. Expected metadata, worktree,
// and endpoint failures are current-state evidence, not process failures, so
// they classify as Unknown without returning an error.
func Resolve(ctx context.Context, stateDir, id string, endpoint Endpoint) (Current, error) {
	meta, err := state.ReadTaskMeta(stateDir, id)
	if err != nil {
		return Current{State: Unknown, Source: SourceMetadata}, nil
	}
	info, err := os.Stat(meta.Worktree)
	if err != nil || !info.IsDir() {
		return Current{State: Unknown, Source: SourceMetadata}, nil
	}
	target := herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID}
	exists, err := endpoint.Exists(ctx, target)
	if err != nil || !exists {
		return Current{State: Unknown, Source: SourceNone}, nil
	}
	busy, err := endpoint.BusyState(ctx, target)
	if err != nil || busy == herdr.BusyUnknown {
		return Current{State: Unknown, Source: SourceNone}, nil
	}
	if busy == herdr.BusyWorking {
		return Current{State: Working, Source: SourceEndpoint}, nil
	}
	if busy != herdr.BusyIdle {
		return Current{State: Unknown, Source: SourceNone}, nil
	}
	validator, ok := endpoint.(StructuralValidator)
	if !ok {
		return Current{State: Unknown, Source: SourceNone}, nil
	}
	valid, err := validator.Validate(ctx, meta)
	if err != nil || !valid {
		return Current{State: Unknown, Source: SourceNone}, nil
	}

	lines, err := state.TailStatus(stateDir, id, 200)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Current{State: Unknown, Source: SourceStatus}, nil
		}
		return Current{State: Unknown, Source: SourceNone}, nil
	}
	for i := len(lines) - 1; i >= 0; i-- {
		verb, detail, ok := ParseStatusLine(lines[i])
		if !ok {
			continue
		}
		return Current{State: mapVerb(verb), Source: SourceStatus, Detail: detail}, nil
	}
	return Current{State: Unknown, Source: SourceStatus}, nil
}

// ParseStatusLine parses one colon-delimited status event, accepting the
// documented before-colon and note-head key forms while returning only the
// status verb and human detail.
func ParseStatusLine(line string) (verb, detail string, ok bool) {
	before, after, found := strings.Cut(strings.TrimSpace(line), ":")
	if !found {
		return "", "", false
	}
	fields := strings.Fields(before)
	if len(fields) == 0 {
		return "", "", false
	}
	verb = fields[0]
	detail = strings.TrimSpace(after)
	if len(fields) == 1 {
		if key, rest, has := noteHeadKey(detail); has && validDecisionKey(key) {
			detail = rest
		}
	}
	return verb, detail, true
}

// FoldOpenDecisions retains open keyed needs-decision and blocked events until
// an exact keyed resolved or captain-held event closes them.
func FoldOpenDecisions(lines []string) []Decision {
	var open []Decision
	for _, line := range lines {
		verb, key, detail, ok := decisionEvent(line)
		if !ok {
			continue
		}
		switch verb {
		case "needs-decision", "blocked":
			open = removeDecision(open, key)
			open = append(open, Decision{Key: key, Verb: verb, Detail: detail})
		case "resolved", "captain-held":
			open = removeDecision(open, key)
		}
	}
	return open
}

func mapVerb(verb string) State {
	switch verb {
	case "working":
		return Working
	case "needs-decision":
		return Parked
	case "blocked":
		return Blocked
	case "paused":
		return Paused
	case "done":
		return Done
	case "failed":
		return Failed
	default:
		return Unknown
	}
}

func decisionEvent(line string) (verb, key, detail string, ok bool) {
	verb, detail, ok = ParseStatusLine(line)
	if !ok {
		return "", "", "", false
	}
	if verb != "needs-decision" && verb != "blocked" && verb != "resolved" && verb != "captain-held" {
		return "", "", "", false
	}
	before, after, _ := strings.Cut(strings.TrimSpace(line), ":")
	fields := strings.Fields(before)
	key = "default"
	if len(fields) > 1 {
		if len(fields) != 2 {
			return "", "", "", false
		}
		var stated bool
		key, stated = keyToken(fields[1])
		if !stated || !validDecisionKey(key) {
			return "", "", "", false
		}
		return verb, key, detail, true
	}
	if stated, rest, has := noteHeadKey(strings.TrimSpace(after)); has {
		if !validDecisionKey(stated) {
			return "", "", "", false
		}
		key = stated
		detail = rest
	}
	return verb, key, detail, true
}

func keyToken(token string) (string, bool) {
	if !strings.HasPrefix(token, "[key=") || !strings.HasSuffix(token, "]") {
		return "", false
	}
	return strings.TrimSuffix(strings.TrimPrefix(token, "[key="), "]"), true
}

func noteHeadKey(detail string) (key, rest string, ok bool) {
	fields := strings.Fields(detail)
	if len(fields) == 0 {
		return "", detail, false
	}
	key, ok = keyToken(fields[0])
	if !ok {
		return "", detail, false
	}
	return key, strings.TrimSpace(strings.TrimPrefix(detail, fields[0])), true
}

func validDecisionKey(key string) bool {
	if key == "" {
		return false
	}
	for _, ch := range key {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '.' || ch == '_' || ch == '-' {
			continue
		}
		return false
	}
	return true
}

func removeDecision(decisions []Decision, key string) []Decision {
	out := decisions[:0]
	for _, decision := range decisions {
		if decision.Key != key {
			out = append(out, decision)
		}
	}
	return out
}
