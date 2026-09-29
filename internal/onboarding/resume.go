package onboarding

import (
	"fmt"
	"regexp"
)

var sessionID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func ResumeArgs(name, session string) ([]string, error) {
	if !sessionID.MatchString(session) {
		return nil, fmt.Errorf("the exact conversation ID is missing or invalid; open %s and select the conversation to resume", name)
	}
	switch name {
	case "claude":
		return []string{"--resume", session}, nil
	case "codex":
		return []string{"resume", session}, nil
	case "pi":
		return []string{"--session", session}, nil
	default:
		return nil, fmt.Errorf("exact conversation resume is unsupported for %q", name)
	}
}
