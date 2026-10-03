package state

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

type PauseCondition struct {
	Reason string    `json:"reason"`
	Until  string    `json:"until,omitempty"`
	At     time.Time `json:"at"`
}

func NewPauseCondition(reason, until string, at time.Time) (PauseCondition, error) {
	condition := PauseCondition{Reason: reason, Until: until, At: at}
	switch reason {
	case "memory", "overlord":
		if until != "" {
			return condition, fmt.Errorf("%s pause does not take --until", reason)
		}
	case "allowance":
		if _, err := time.Parse(time.RFC3339, until); err != nil {
			return condition, errors.New("allowance pause requires --until <reset time in RFC3339>")
		}
	case "dependency":
		kind, target, _ := strings.Cut(until, ":")
		switch kind {
		case "task":
			if err := ValidTaskID(target); err != nil {
				return condition, err
			}
		case "pr":
			if err := validateWaitURL(kind, target); err != nil {
				return condition, err
			}
		case "date":
			if _, err := time.Parse(time.RFC3339, target); err != nil {
				return condition, errors.New("date dependency requires an RFC3339 timestamp")
			}
		default:
			return condition, errors.New("dependency pause requires --until task:<id>, pr:<URL> or date:<RFC3339>")
		}
	case "question":
		if until == "" || len(until) > 128 || strings.ContainsAny(until, "\x00\r\n") {
			return condition, errors.New("question pause requires --until <question id>")
		}
	case "ci", "deploy":
		wait, head, _ := strings.Cut(until, "@")
		if len(head) != 40 || strings.Trim(head, "0123456789abcdef") != "" {
			return condition, errors.New("CI or deploy pause requires --until pr:<URL>@<head SHA> or run:<URL>@<head SHA>")
		}
		kind, target, _ := strings.Cut(wait, ":")
		if err := validateWaitURL(kind, target); err != nil {
			return condition, err
		}
	default:
		return condition, errors.New("pause requires --reason memory, allowance, overlord, dependency, question, ci or deploy")
	}
	return condition, nil
}

func validateWaitURL(kind, target string) error {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil || parsed.RawPath != "" {
		return errors.New("wait requires a GitHub pull request or Actions run URL")
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if parsed.Path != "/"+strings.Join(parts, "/") {
		return errors.New("wait requires a canonical GitHub pull request or Actions run URL")
	}
	var number string
	switch {
	case kind == "pr" && len(parts) == 4 && parts[2] == "pull":
		number = parts[3]
	case kind == "run" && len(parts) == 5 && parts[2] == "actions" && parts[3] == "runs":
		number = parts[4]
	default:
		return errors.New("wait requires pr:<GitHub pull request URL> or run:<GitHub Actions run URL>")
	}
	if parts[0] == "" || parts[1] == "" || number == "" || strings.Trim(number, "0123456789") != "" || strings.Trim(number, "0") == "" {
		return errors.New("wait requires a GitHub pull request or Actions run URL with a positive number")
	}
	return nil
}

func (condition PauseCondition) Description() string {
	switch condition.Reason {
	case "memory":
		return "Memory; resumes after two readings with 5 GB of memory and commit free"
	case "allowance":
		return "Allowance; resumes at " + condition.Until
	case "overlord":
		return "Paused by the Overlord; resumes on Resume"
	case "question":
		return "Waiting for the Overlord's answer to " + condition.Until
	case "dependency":
		return "Waiting on " + condition.Until
	case "ci", "deploy":
		return "Waiting on " + condition.Reason + "; resumes when the awaited head finishes"
	}
	return ""
}
