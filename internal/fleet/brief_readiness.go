package fleet

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/auth"
)

var ErrBriefNotReady = errors.New("brief is not ready")

var briefPlaceholder = regexp.MustCompile(`(?m)^[\t ]*(?:[-*] )?\{(TASK|ACCEPTANCE|CONSTRAINTS)\b`)
var briefComment = regexp.MustCompile(`(?s)<!--.*?-->`)

// ValidateBriefReadiness refuses scaffolds until their author finishes the
// requirements and marks the launch choices ready as the final edit.
func ValidateBriefReadiness(content []byte) error {
	text := strings.TrimSpace(string(content))
	if text == "" {
		return fmt.Errorf("%w: the brief is empty", ErrBriefNotReady)
	}
	if placeholder := briefPlaceholder.FindStringSubmatch(text); placeholder != nil {
		return fmt.Errorf("%w: replace the %s placeholder", ErrBriefNotReady, placeholder[1])
	}
	section := ""
	sections := map[string][]string{}
	isReadySeen := false
	isModeSeen := false
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if heading := levelTwoHeading.FindStringSubmatch(line); heading != nil {
			section = heading[1]
			continue
		}
		sections[section] = append(sections[section], line)
		if section != "Delivery" {
			continue
		}
		key, value, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found {
			continue
		}
		if strings.TrimSpace(key) == "mode" {
			mode := strings.TrimSpace(value)
			if isModeSeen || mode != "no-mistakes" && mode != "direct-PR" && mode != "local-only" {
				return fmt.Errorf("%w: choose one valid Delivery mode", ErrBriefNotReady)
			}
			isModeSeen = true
			continue
		}
		if strings.TrimSpace(key) != "ready" {
			continue
		}
		if isReadySeen || strings.TrimSpace(value) != "true" {
			return fmt.Errorf("%w: set Delivery ready: true only after requirements, credentials and launch settings are final", ErrBriefNotReady)
		}
		isReadySeen = true
	}
	for _, name := range []string{"Task", "Acceptance criteria", "Constraints"} {
		if strings.TrimSpace(briefComment.ReplaceAllString(strings.Join(sections[name], "\n"), "")) == "" {
			return fmt.Errorf("%w: author the %s section", ErrBriefNotReady, name)
		}
	}
	need, err := auth.NeedFromBrief(text)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBriefNotReady, err)
	}
	if need.IsUnstated || need.IsMCPUnstated {
		return fmt.Errorf("%w: explicitly name credentials and mcp in Authentication, or write none", ErrBriefNotReady)
	}
	if !isReadySeen {
		return fmt.Errorf("%w: finish the requirements and launch choices, then run cfo brief ready <id>", ErrBriefNotReady)
	}
	if !isModeSeen {
		return fmt.Errorf("%w: explicitly choose the Delivery mode", ErrBriefNotReady)
	}
	return nil
}

// FinalizeBrief validates an author's explicit readiness transition without
// changing any requirement or launch choice.
func FinalizeBrief(content []byte) ([]byte, error) {
	text := string(content)
	section, offset, deliveryEnd := "", 0, -1
	markerStart, markerEnd := -1, -1
	marker := ""
	for _, line := range strings.SplitAfter(text, "\n") {
		plain := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if heading := levelTwoHeading.FindStringSubmatch(plain); heading != nil {
			if section == "Delivery" {
				deliveryEnd = offset
			}
			section = heading[1]
		} else if section == "Delivery" {
			key, value, found := strings.Cut(strings.TrimSpace(plain), ":")
			if found && strings.TrimSpace(key) == "ready" {
				value = strings.TrimSpace(value)
				if markerStart >= 0 || value != "true" && value != "false" {
					return nil, fmt.Errorf("%w: choose one Delivery ready: false or ready: true before finalizing", ErrBriefNotReady)
				}
				markerStart, markerEnd = offset, offset+len(line)
				marker = strings.Replace(line, "false", "true", 1)
			}
		}
		offset += len(line)
	}
	if section == "Delivery" {
		deliveryEnd = len(text)
	}
	if markerStart >= 0 {
		text = text[:markerStart] + marker + text[markerEnd:]
	} else if deliveryEnd >= 0 {
		newline := "\n"
		if strings.Contains(text, "\r\n") {
			newline = "\r\n"
		}
		prefix := text[:deliveryEnd]
		if !strings.HasSuffix(prefix, "\n") {
			prefix += newline
		}
		text = prefix + "ready: true" + newline + text[deliveryEnd:]
	}
	finalized := []byte(text)
	if err := ValidateBriefReadiness(finalized); err != nil {
		return nil, err
	}
	return finalized, nil
}
