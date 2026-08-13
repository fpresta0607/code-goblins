package fleet

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// RenderJSON writes the complete typed snapshot. It does not inspect files,
// endpoints, terminal output, monitor data, or clocks.
func RenderJSON(w io.Writer, snapshot Snapshot) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(snapshot)
}

// RenderMarkdown writes the human fleet view from an already-built snapshot.
// It intentionally has no dependency other than the provided typed value.
func RenderMarkdown(w io.Writer, snapshot Snapshot) error {
	if err := writeLine(w, "# Fleet View"); err != nil {
		return err
	}
	if err := writeLine(w, ""); err != nil {
		return err
	}
	if err := writeLine(w, "Schema: "+dash(snapshot.Schema)); err != nil {
		return err
	}
	if err := writeLine(w, "Home: "+dash(snapshot.Home)); err != nil {
		return err
	}
	if err := writeLine(w, ""); err != nil {
		return err
	}
	if err := writeLine(w, "## Under Way"); err != nil {
		return err
	}
	if err := renderTasks(w, snapshot.Tasks); err != nil {
		return err
	}
	if err := writeLine(w, ""); err != nil {
		return err
	}
	if err := writeLine(w, "## Queued"); err != nil {
		return err
	}
	if err := renderBacklogRows(w, snapshot.Backlog.Queued, "No queued backlog records found."); err != nil {
		return err
	}
	if err := writeLine(w, ""); err != nil {
		return err
	}
	if err := writeLine(w, "## Done"); err != nil {
		return err
	}
	if err := renderBacklogRows(w, snapshot.Backlog.Done, "No done backlog records found."); err != nil {
		return err
	}
	if err := writeLine(w, ""); err != nil {
		return err
	}
	if err := writeLine(w, "## Secondmates"); err != nil {
		return err
	}
	return writeLine(w, "Secondmates are not supported in Plan 3.")
}

func renderTasks(w io.Writer, tasks []TaskRow) error {
	if len(tasks) == 0 {
		return writeLine(w, "No live task metadata found.")
	}
	if err := writeLine(w, "| ID | Current | Health | Stale | Last Seen | Escalation | Deep Inspection | Kind | Project | Backend | Endpoint | Artifact | Path | Peek |"); err != nil {
		return err
	}
	if err := writeLine(w, "| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |"); err != nil {
		return err
	}
	for _, task := range tasks {
		fields := []string{
			task.ID,
			currentText(task),
			dash(string(task.Monitor.Health)),
			staleText(task.Monitor.StaleSeconds),
			lastSeenText(task.Monitor.LastSeen),
			strconv.Itoa(task.Monitor.Escalation),
			boolText(task.Monitor.DemandDeepInspection),
			task.Kind,
			task.Project,
			task.Backend,
			endpointText(task.Endpoint.Exists),
			task.Artifact,
			task.Path,
			task.Actions.Peek,
		}
		if err := writeLine(w, "| "+strings.Join(markdownFields(fields), " | ")+" |"); err != nil {
			return err
		}
	}
	return nil
}

func renderBacklogRows(w io.Writer, rows []BacklogRow, empty string) error {
	if len(rows) == 0 {
		return writeLine(w, empty)
	}
	inTable := false
	needsTableGap := false
	for _, row := range rows {
		if !row.Structured {
			if inTable {
				if err := writeLine(w, ""); err != nil {
					return err
				}
				inTable = false
			}
			if err := writeLine(w, row.Raw); err != nil {
				return err
			}
			needsTableGap = true
			continue
		}
		if !inTable {
			if needsTableGap {
				if err := writeLine(w, ""); err != nil {
					return err
				}
				needsTableGap = false
			}
			if err := writeLine(w, "| ID | Title | Repo | Kind | Blocked By | Artifact |"); err != nil {
				return err
			}
			if err := writeLine(w, "| --- | --- | --- | --- | --- | --- |"); err != nil {
				return err
			}
			inTable = true
		}
		fields := markdownFields([]string{row.ID, row.Title, row.Repo, row.Kind, blockerText(row), row.Artifact})
		if err := writeLine(w, "| "+strings.Join(fields, " | ")+" |"); err != nil {
			return err
		}
	}
	return nil
}

func markdownFields(values []string) []string {
	fields := make([]string, len(values))
	for index, value := range values {
		fields[index] = strings.ReplaceAll(strings.ReplaceAll(dash(value), "\r", " "), "\n", " ")
		fields[index] = strings.ReplaceAll(fields[index], "|", "\\|")
	}
	return fields
}

func currentText(task TaskRow) string {
	return dash(string(task.Current.State)) + " / " + dash(string(task.Current.Source))
}

func staleText(seconds int64) string {
	if seconds <= 0 {
		return "-"
	}
	return (time.Duration(seconds) * time.Second).String()
}

func lastSeenText(lastSeen *time.Time) string {
	if lastSeen == nil || lastSeen.IsZero() {
		return "-"
	}
	return lastSeen.UTC().Format(time.RFC3339)
}

func boolText(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func endpointText(exists *bool) string {
	if exists == nil {
		return "unknown"
	}
	if *exists {
		return "present"
	}
	return "absent"
}

func blockerText(row BacklogRow) string {
	if row.BlockedBy == "" {
		return "-"
	}
	if row.BlockedReason == "" {
		return row.BlockedBy
	}
	return row.BlockedBy + " - " + row.BlockedReason
}

func dash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func writeLine(w io.Writer, line string) error {
	_, err := fmt.Fprintln(w, line)
	return err
}
