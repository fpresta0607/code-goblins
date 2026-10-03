package fleet

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
)

var (
	checkboxBacklogRow = regexp.MustCompile(`^[-*]\s+\[[ xX]\]\s+(\S+)\s+-\s+(.*)$`)
	boldBacklogRow     = regexp.MustCompile(`^[-*]\s+(?:parked\s+)?\*\*([^*]+)\*\*\s+-\s+(.*)$`)
	urlPattern         = regexp.MustCompile(`https?://[^\s\)\]"<>]+`)
	wrappedURLPattern  = regexp.MustCompile(`<?https?://[^\s\)\]"<>]+>?`)
	reportPattern      = regexp.MustCompile(`data/[^\s\)]+/report\.md`)
	trailingMetadata   = regexp.MustCompile(`(?i)\s*\(\s*(?:(?:repo|kind|priority|hold|hold-kind|harness|model|effort|mode)\s*:\s*[^)]*|(?:since|merged|reported|done)\s+[^)]*)\s*\)\s*$`)
	blockerToken       = regexp.MustCompile(`(?i)\bblocked-by:\s*([^\s\)]+)`)
	levelTwoHeading    = regexp.MustCompile(`^##[ \t]+(.+)$`)
	metadataPatterns   = func() map[string]*regexp.Regexp {
		patterns := map[string]*regexp.Regexp{}
		for _, key := range []string{"repo", "kind", "hold-kind", "harness", "model", "effort", "mode"} {
			patterns[key] = regexp.MustCompile(`(?i)(?:\(|,)\s*` + key + `\s*:\s*([^,)]*)`)
		}
		return patterns
	}()
)

// BacklogRows retains each rendered Plan 3 backlog section in source order.
// Parked is work set aside rather than queued: the rows under ## Parked and
// any row parked in place with hold-kind parked, in file order.
type BacklogRows struct {
	Path        string       `json:"path"`
	Present     bool         `json:"present"`
	Queued      []BacklogRow `json:"queued"`
	Parked      []BacklogRow `json:"parked"`
	Done        []BacklogRow `json:"done"`
	queuedTasks map[string]QueuedTask
	duplicates  map[string]bool
	listed      map[string]bool
}

// BacklogRow is either a typed tasks-axi-compatible record or a raw source
// line that a renderer must keep outside Markdown tables.
type BacklogRow struct {
	Structured    bool     `json:"structured"`
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Repo          string   `json:"repo"`
	Kind          string   `json:"kind"`
	Priority      string   `json:"priority,omitempty"`
	BlockedBy     string   `json:"blocked_by"`
	BlockedByIDs  []string `json:"blocked_by_ids"`
	BlockedReason string   `json:"blocked_reason"`
	Artifact      string   `json:"artifact"`
	// Harness, Model, Effort and Mode are what a row names for its spawn,
	// such as (harness: codex, model: gpt-6-astra).
	Harness string `json:"harness,omitempty"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
	Mode    string `json:"mode,omitempty"`
	Raw     string `json:"raw"`
}

// ReadBacklog parses the supported Queued, Parked and Done records without
// changing their file order. Missing backlog files are a typed empty result.
func ReadBacklog(h home.Home) (BacklogRows, error) {
	path := filepath.Join(h.Data, "backlog.md")
	result := BacklogRows{Path: path, Queued: []BacklogRow{}, Parked: []BacklogRow{}, Done: []BacklogRow{}, queuedTasks: map[string]QueuedTask{}, duplicates: map[string]bool{}, listed: map[string]bool{}}
	data, err := fsx.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return BacklogRows{}, fmt.Errorf("fleet: read backlog: %w", err)
	}
	result.Present = true
	section := ""
	lines := strings.Split(string(data), "\n")
	for index, raw := range lines {
		line := strings.TrimSuffix(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if heading := levelTwoHeading.FindStringSubmatch(trimmed); heading != nil {
			switch strings.TrimSpace(heading[1]) {
			case "Queued":
				section = "queued"
			case "Parked":
				section = "parked"
			case "Done":
				section = "done"
			default:
				section = ""
			}
			continue
		}
		if trimmed == "" {
			continue
		}
		row := parseBacklogRow(trimmed)
		if row.Structured {
			result.listed[row.ID] = true
		}
		if section == "" {
			continue
		}
		row.Raw = line
		switch {
		case section == "parked" || section == "queued" && strings.EqualFold(metadataValue(trimmed, "hold-kind"), "parked"):
			result.Parked = append(result.Parked, row)
		case section == "queued":
			result.Queued = append(result.Queued, row)
			if row.Structured {
				end := index + 1
				for next := end; next < len(lines); next++ {
					if strings.TrimSpace(lines[next]) == "" {
						continue
					}
					if !continuesRow(lines[next]) {
						break
					}
					end = next + 1
				}
				detail := make([]string, 0, end-index-1)
				for _, line := range lines[index+1 : end] {
					detail = append(detail, strings.TrimSpace(line))
				}
				if _, exists := result.queuedTasks[row.ID]; exists {
					result.duplicates[row.ID] = true
				}
				result.queuedTasks[row.ID] = QueuedTask{Row: row, Detail: strings.TrimSpace(strings.Join(detail, "\n")), Revision: fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(lines[index:end], "\n"))))}
			}
		default:
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
	blockedByIDs, blockedReason := parseBlockers(rest)
	blockedBy := ""
	if len(blockedByIDs) > 0 {
		blockedBy = blockedByIDs[0]
	}
	return BacklogRow{
		Structured:    true,
		ID:            strings.TrimSpace(match[1]),
		Title:         backlogTitle(rest),
		Repo:          metadataValue(rest, "repo"),
		Kind:          metadataValue(rest, "kind"),
		Priority:      metadataValue(rest, "priority"),
		BlockedBy:     blockedBy,
		BlockedByIDs:  blockedByIDs,
		BlockedReason: blockedReason,
		Artifact:      backlogArtifact(rest),
		Harness:       metadataValue(rest, "harness"),
		Model:         metadataValue(rest, "model"),
		Effort:        metadataValue(rest, "effort"),
		Mode:          metadataValue(rest, "mode"),
		Raw:           line,
	}
}

func metadataValue(text, key string) string {
	match := metadataPatterns[key].FindStringSubmatch(text)
	if match == nil {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func parseBlockers(text string) ([]string, string) {
	matches := blockerToken.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return []string{}, ""
	}
	blockedByIDs := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		id := text[match[2]:match[3]]
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		blockedByIDs = append(blockedByIDs, id)
	}
	last := matches[len(matches)-1]
	remaining := strings.TrimSpace(text[last[1]:])
	if !strings.HasPrefix(remaining, "-") {
		return blockedByIDs, ""
	}
	return blockedByIDs, cleanBacklogTitle(strings.TrimSpace(strings.TrimPrefix(remaining, "-")))
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
