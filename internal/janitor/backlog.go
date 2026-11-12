package janitor

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fleet"
)

// BacklogHistoryLimit is the size over which the sweep reports the backlog's
// history: its sections but Queued, Parked, In flight and Done that hold no
// open row, which every read of the backlog pays for.
const BacklogHistoryLimit = 64 << 10

// largestHistoryShown is how many of the history's largest sections its
// report names.
const largestHistoryShown = 5

// reportBacklogHistory reports a backlog whose history is over
// BacklogHistoryLimit, with its largest sections, largest first. It moves
// nothing: a section with no open row can still hold open work written as
// prose, which only the CEO can tell from what is over.
func (cfg Config) reportBacklogHistory(record *Record) {
	backlog, err := fleet.ReadBacklog(cfg.Home)
	if err != nil {
		record.Notes = append(record.Notes, "the backlog's history was not measured: "+err.Error())
		return
	}
	sections := slices.Clone(backlog.History())
	lines, bytes := 0, 0
	for _, section := range sections {
		lines += section.Lines
		bytes += section.Bytes
	}
	if bytes <= BacklogHistoryLimit {
		return
	}
	slices.SortStableFunc(sections, func(a, b fleet.BacklogSection) int { return cmp.Compare(b.Bytes, a.Bytes) })
	var largest []string
	for _, section := range sections[:min(len(sections), largestHistoryShown)] {
		largest = append(largest, fmt.Sprintf("%q at line %d, %.0f KB", section.Heading, section.Line, float64(section.Bytes)/(1<<10)))
	}
	detail := fmt.Sprintf("the backlog holds %d lines, %.0f KB, in %d sections but Queued, Parked, In flight and Done that hold no open row, over the %d KB its history may take, and every read of the backlog pays for them: move what is over to data/archive/backlog-history.md with its heading, and make open work among it a row; the largest: %s", lines, float64(bytes)/(1<<10), len(sections), BacklogHistoryLimit>>10, strings.Join(largest, ", "))
	record.Strays = append(record.Strays, Item{Kind: "backlog", Path: backlog.Path, Bytes: int64(bytes), Detail: detail})
}
