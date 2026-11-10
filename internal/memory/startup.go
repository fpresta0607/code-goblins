package memory

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
)

// scopesRoom bounds the scope counts a startup carries, as Routes is bounded.
const scopesRoom = 2000

// StartupMap is the memory a CEO session reads at its start: every warm
// entry, a count of what each scope holds cold, and how many rulings no
// scope has been ruled for yet. The budget measures exactly this text.
func (catalog Catalog) StartupMap() (string, error) {
	if err := catalog.checkFresh(); err != nil {
		return "", err
	}
	var output strings.Builder
	catalog.writePreamble(&output)
	selected, unclassified := map[string]bool{}, 0
	for _, source := range catalog.Sources {
		for _, entry := range source.Entries {
			selected[entry.ID] = entry.Location == "warm"
			if isUnclassified(source.Path, entry) {
				unclassified++
			}
		}
	}
	catalog.writeShort(&output, selected)
	output.WriteString(catalog.Scopes(scopesRoom))
	if unclassified > 0 {
		fmt.Fprintf(&output, "Rulings not classified yet, which load until they are: %d; cfo memory scope --list shows them.\n", unclassified)
	}
	if err := catalog.checkFresh(); err != nil {
		return "", err
	}
	return output.String(), nil
}

// Unclassified lists, in the startup map's form, the rulings no scope has
// been ruled for yet.
func (catalog Catalog) Unclassified() (string, error) {
	if err := catalog.checkFresh(); err != nil {
		return "", err
	}
	selected := map[string]bool{}
	for _, source := range catalog.Sources {
		for _, entry := range source.Entries {
			selected[entry.ID] = isUnclassified(source.Path, entry)
		}
	}
	var output strings.Builder
	catalog.writeShort(&output, selected)
	return output.String(), nil
}

// isUnclassified reports a ruling that loads only because nobody has ruled
// whether it is a project's or the whole fleet's.
func isUnclassified(source string, entry Entry) bool {
	return source == "overlord.md" && entry.Authority == "ruling" && entry.Location == "warm" && len(entry.Projects)+len(entry.Tasks)+len(entry.Topics) == 0
}

// writeShort writes the selected entries in the startup map's form: each
// source named once, then each entry as its id and its exact bytes. An entry
// whose heading another entry holds is told apart by a line naming that
// heading, so a ruling is never read without its date. Stow's diagnostics
// ride in the entry's brackets, and one line after the entries says what
// they ask for. It returns how many entries it wrote.
func (catalog Catalog) writeShort(output *strings.Builder, selected map[string]bool) int {
	isFlagged, written := false, 0
	for _, source := range catalog.Sources {
		content := catalog.content[source.Path]
		var lines []sourceLine
		position, heading, shown := 0, -1, -1
		for _, entry := range source.Entries {
			if !selected[entry.ID] {
				continue
			}
			if lines == nil {
				lines = linesOf(content)
				fmt.Fprintf(output, "\n%s\n", source.Path)
			}
			written++
			for ; position < len(lines) && lines[position].Start < entry.End; position++ {
				if lines[position].IsHeading {
					heading = position
				}
			}
			if heading >= 0 && lines[heading].Start < entry.Start && heading != shown {
				fmt.Fprintf(output, "(under %s)\n", lines[heading].Text)
			}
			shown = heading
			status := catalog.status[entry.ID]
			label := entry.ID
			for _, flag := range []struct {
				name  string
				isSet bool
			}{{"stale", status.IsStale}, {"legacy", status.IsLegacy}, {"grace", status.IsGrace}, {"pending-capture=" + status.PendingCapture, status.PendingCapture != ""}} {
				if flag.isSet {
					label += " " + flag.name
					isFlagged = true
				}
			}
			if entry.TaskState != "" {
				label += " task-state=" + entry.TaskState
			}
			if len(entry.Supersedes) > 0 {
				fmt.Fprintf(output, "Supersession candidates of %s (no automatic retirement): %s\n", entry.ID, strings.Join(entry.Supersedes, ", "))
			}
			body := content[entry.Start:entry.End]
			separator := " "
			if bytes.ContainsRune(bytes.TrimRight(body, "\r\n\t "), '\n') {
				separator = "\n"
			}
			fmt.Fprintf(output, "[%s]%s%s", label, separator, body)
			if !bytes.HasSuffix(body, []byte("\n")) {
				output.WriteByte('\n')
			}
		}
	}
	if isFlagged {
		output.WriteString("STOW REQUIRED: an entry marked stale, legacy, grace or pending-capture stays until the owning pass resolves it.\n")
	}
	return written
}

// Scopes counts the cold entries each routing key holds, so a startup that
// leaves them out still says where every one of them is. It is bounded as
// Routes is, with the scopes past its room counted.
func (catalog Catalog) Scopes(limit int) string {
	authorities := []string{"ruling", "fact", "project", "history"}
	words := map[string]string{"ruling": "rulings", "fact": "facts", "project": "project notes", "history": "history"}
	counts, needsStow := map[string]map[string]int{}, map[string]int{}
	for _, source := range catalog.Sources {
		for _, entry := range source.Entries {
			if entry.Location != "cold" {
				continue
			}
			status := catalog.status[entry.ID]
			for kind, keys := range map[string][]string{"project": entry.Projects, "task": entry.Tasks, "topic": entry.Topics} {
				for _, key := range keys {
					scope := kind + " " + key
					if counts[scope] == nil {
						counts[scope] = map[string]int{}
					}
					counts[scope][entry.Authority]++
					if status.IsStale || status.IsLegacy {
						needsStow[scope]++
					}
				}
			}
		}
	}
	if len(counts) == 0 {
		return ""
	}
	scopes := make([]string, 0, len(counts))
	for scope := range counts {
		scopes = append(scopes, scope)
	}
	slices.Sort(scopes)
	var output strings.Builder
	output.WriteString("Left out above, by scope; each entry stays verbatim in its source:\n")
	left := 0
	for _, scope := range scopes {
		var held []string
		for _, authority := range authorities {
			if count := counts[scope][authority]; count > 0 {
				held = append(held, fmt.Sprintf("%s %d", words[authority], count))
			}
		}
		line := scope + ": " + strings.Join(held, ", ")
		if needsStow[scope] > 0 {
			line += fmt.Sprintf("; Stow required for %d", needsStow[scope])
		}
		line += "\n"
		if output.Len()+len(line) > limit {
			left++
		} else {
			output.WriteString(line)
		}
	}
	if left > 0 {
		fmt.Fprintf(&output, "%d more scopes: cfo memory routes lists every cold entry.\n", left)
	}
	return output.String()
}
