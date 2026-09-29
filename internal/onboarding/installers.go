package onboarding

import "fmt"

func Package(name string) (string, error) {
	switch name {
	case "claude":
		return "@anthropic-ai/claude-code@2.1.284", nil
	case "codex":
		return "@openai/codex@0.154.0", nil
	case "pi":
		return "@earendil-works/pi-coding-agent@0.85.1", nil
	default:
		return "", fmt.Errorf("unknown agent %q", name)
	}
}
