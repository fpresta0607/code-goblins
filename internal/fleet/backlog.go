package fleet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/home"
)

var (
	checkboxBacklogRow = regexp.MustCompile(`^[-*]\s+\[[ xX]\]\s+(\S+)\s+-\s+(.*)$`)
	boldBacklogRow     = regexp.MustCompile(`^[-*]\s+\*\*([^*]+)\*\*\s+-\s+(.*)$`)
	urlPattern         = regexp.MustCompile(`https?://[^\s\)\]"<>]+`)
	wrappedURLPattern  = regexp.MustCompile(`<?https?://[^\s\)\]"<>]+>?`)
	reportPattern      = regexp.MustCompile(`data/[^\s\)]+/report\.md`)
	trailingMetadata   = regexp.MustCompile(`(?i)\s*\(\s*(?:(?:repo|kind|priority|hold|hold-kind)\s*:\s*[^)]*|(?:since|merged|reported|done)\s+[^)]*)\s*\)\s*$`)
	blockerToken       = regexp.MustCompile(`(?i)\bblocked-by:\s*([^\s\)]+)`)
)

// BacklogRows retains each rendered Plan 3 backlog section in source order.
type BacklogRows struct {
	Path    string       `json:"path"`
	Present bool         `json:"present"`
	Queued  []BacklogRow `json:"queued"`
	Done    []BacklogRow `json:"done"`
}

// BacklogRow is either a typed tasks-axi-compatible record or a raw source
// line that a renderer must keep outside Markdown tables.
type BacklogRow struct {
	Structured    bool   `json:"structured"`
	ID            string `json:"id"`
	Title         string `json:"title"`
	Repo          string `json:"repo"`
	Kind          string `json:"kind"`
	BlockedBy     string `json:"blocked_by"`
	BlockedReason string `json:"blocked_reason"`
	Artifact      string `json:"artifact"`
	Raw           string `json:"raw"`
}

// ReadBacklog parses the supported Queued and Done records without changing
// their file order. Missing backlog files are a typed empty result.
func ReadBacklog(h home.Home) (BacklogRows, error) {
	path := filepath.Join(h.Data, "backlog.md")
	result := BacklogRows{Path: path, Queued: []BacklogRow{}, Done: []BacklogRow{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return BacklogRows{}, fmt.Errorf("fleet: read backlog: %w", err)
	}
	result.Present = true
	section := ""
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSuffix(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "##") {
			switch strings.TrimSpace(strings.TrimPrefix(trimmed, "##")) {
			case "Queued":
				section = "queued"
			case "Done":
				section = "done"
			default:
				section = ""
			}
			continue
		}
		if section == "" || trimmed == "" {
			continue
		}
		row := parseBacklogRow(trimmed)
		row.Raw = line
		if section == "queued" {
			result.Queued = append(result.Queued, row)
		} else {
			result.Done = append(result.Done, row)
		}
	}
	return result, nil
}

func parseBacklogRow(line string) BacklogRow {
	match := checkboxBacklogRow.FindStringSubmatch(line)
	if match == nil {
		match = boldBacklogRow.FindStringSubmatch(line)
	}
	if match == nil {
		return BacklogRow{Raw: line}
	}
	rest := match[2]
	blockedBy, blockedReason := parseBlocker(rest)
	return BacklogRow{
		Structured:    true,
		ID:            strings.TrimSpace(match[1]),
		Title:         backlogTitle(rest),
		Repo:          metadataValue(rest, "repo"),
		Kind:          metadataValue(rest, "kind"),
		BlockedBy:     blockedBy,
		BlockedReason: blockedReason,
		Artifact:      backlogArtifact(rest),
		Raw:           line,
	}
}

func metadataValue(text, key string) string {
	pattern := regexp.MustCompile(`(?i)(?:\(|,)\s*` + regexp.QuoteMeta(key) + `\s*:\s*([^,)]*)`)
	match := pattern.FindStringSubmatch(text)
	if match == nil {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func parseBlocker(text string) (string, string) {
	match := blockerToken.FindStringSubmatchIndex(text)
	if match == nil {
		return "", ""
	}
	blockedBy := text[match[2]:match[3]]
	remaining := strings.TrimSpace(text[match[1]:])
	if !strings.HasPrefix(remaining, "-") {
		return blockedBy, ""
	}
	return blockedBy, cleanBacklogTitle(strings.TrimSpace(strings.TrimPrefix(remaining, "-")))
}

func backlogTitle(rest string) string {
	withoutBlocker := rest
	if match := blockerToken.FindStringIndex(rest); match != nil {
		withoutBlocker = rest[:match[0]]
	}
	withoutURLs := wrappedURLPattern.ReplaceAllString(withoutBlocker, "")
	return cleanBacklogTitle(withoutURLs)
}

func cleanBacklogTitle(value string) string {
	value = strings.TrimSpace(value)
	for trailingMetadata.MatchString(value) {
		value = trailingMetadata.ReplaceAllString(value, "")
	}
	value = reportPattern.ReplaceAllString(value, "")
	value = strings.TrimSpace(strings.TrimSuffix(value, "local main"))
	value = strings.TrimSpace(strings.TrimSuffix(value, "-"))
	return strings.Join(strings.Fields(value), " ")
}

func backlogArtifact(rest string) string {
	for _, url := range urlPattern.FindAllString(rest, -1) {
		if strings.Contains(url, "/pull/") {
			return url
		}
	}
	if report := reportPattern.FindString(rest); report != "" {
		return report
	}
	if strings.Contains(strings.ToLower(rest), "local main") {
		return "local main"
	}
	return ""
}
