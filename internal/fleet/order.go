package fleet

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// ErrQueueChanged is an order that is not the queue backlog.md holds now: a
// row was added, removed or renamed after the board showed it.
var ErrQueueChanged = errors.New("the queue changed after the board showed it")

// attentionFile keeps the Overlord's order of the goblins in progress, top
// first, as a JSON list of task IDs.
const attentionFile = "attention.json"

// queuedBlock is one queued row of backlog.md with the indented lines that
// belong to it, lines [start, end).
type queuedBlock struct {
	id         string
	start, end int
}

// ReorderQueued rewrites data\backlog.md's Queued section so its rows run in
// order, top first, which is the order the CFO dispatches in. A row moves
// with its indented detail lines; notes, parked rows, the rows of tasks in
// live, which are in progress rather than queued, and every other section
// stay where they are. added holds the row to write for each task in order
// that has a brief and no row yet. An order that is not exactly the queue
// the file holds, with added, is ErrQueueChanged and writes nothing.
func ReorderQueued(h home.Home, order []string, added map[string]string, live map[string]bool) error {
	path := filepath.Join(h.Data, "backlog.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("fleet: read backlog: %w", err)
	}
	next, err := reorderedBacklog(string(data), order, added, live)
	if err != nil || next == string(data) {
		return err
	}
	if err := fsx.AtomicWriteFile(path, []byte(next)); err != nil {
		return fmt.Errorf("fleet: write backlog: %w", err)
	}
	return nil
}

func reorderedBacklog(text string, order []string, added map[string]string, live map[string]bool) (string, error) {
	lines := strings.Split(text, "\n")
	blocks, anchor := queuedBlocks(lines, live)
	current := make(map[string]queuedBlock, len(blocks))
	for _, block := range blocks {
		if _, twice := current[block.id]; twice {
			return "", fmt.Errorf("fleet: backlog.md queues %s twice; order it by hand", block.id)
		}
		current[block.id] = block
	}
	placed := make(map[string]bool, len(order))
	for _, id := range order {
		_, queued := current[id]
		_, adding := added[id]
		if placed[id] || queued == adding {
			return "", ErrQueueChanged
		}
		placed[id] = true
	}
	if len(placed) != len(current)+len(added) {
		return "", ErrQueueChanged
	}
	ending := ""
	if strings.HasSuffix(lines[0], "\r") {
		ending = "\r"
	}
	groups := make([][]string, len(order))
	for i, id := range order {
		if block, ok := current[id]; ok {
			groups[i] = lines[block.start:block.end]
		} else {
			groups[i] = []string{added[id] + ending}
		}
	}
	if len(blocks) == 0 {
		if len(order) == 0 {
			return text, nil
		}
		if anchor < 0 {
			return "", errors.New("fleet: backlog.md has no Queued section to add rows to")
		}
		out := slices.Clone(lines[:anchor+1])
		for _, group := range groups {
			out = append(out, group...)
		}
		return strings.Join(append(out, lines[anchor+1:]...), "\n"), nil
	}
	// Each row's place in the file takes the next row in order; the last
	// place also takes the rows added after it.
	out := make([]string, 0, len(lines)+len(added))
	next := 0
	for slot, block := range blocks {
		out = append(out, lines[next:block.start]...)
		last := slot + 1
		if slot == len(blocks)-1 {
			last = len(groups)
		}
		for _, group := range groups[slot:last] {
			out = append(out, group...)
		}
		next = block.end
	}
	return strings.Join(append(out, lines[next:]...), "\n"), nil
}

// queuedBlocks finds the rows ReadBacklog lists as queued, in file order,
// except those of tasks in live, and the last written line of the first
// Queued section, or -1 without one.
func queuedBlocks(lines []string, live map[string]bool) ([]queuedBlock, int) {
	var blocks []queuedBlock
	anchor := -1
	section, first := "", true
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if heading := levelTwoHeading.FindStringSubmatch(trimmed); heading != nil {
			if section == "queued" {
				first = false
			}
			section = ""
			if strings.TrimSpace(heading[1]) == "Queued" {
				section = "queued"
				if first {
					anchor = i
				}
			}
			continue
		}
		if section != "queued" || trimmed == "" {
			continue
		}
		if first {
			anchor = i
		}
		row := parseBacklogRow(trimmed)
		if !row.Structured || live[row.ID] || strings.EqualFold(metadataValue(trimmed, "hold-kind"), "parked") {
			continue
		}
		end := i + 1
		for next := end; next < len(lines); next++ {
			if strings.TrimSpace(lines[next]) == "" {
				continue
			}
			if !continuesRow(lines[next]) {
				break
			}
			end = next + 1
		}
		blocks = append(blocks, queuedBlock{id: row.ID, start: i, end: end})
		if first {
			anchor = end - 1
		}
		i = end - 1
	}
	return blocks, anchor
}

// continuesRow says an indented line belongs to the row above it: a detail
// line, not a heading or a nested row of its own.
func continuesRow(line string) bool {
	trimmed := strings.TrimSpace(line)
	return (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) &&
		levelTwoHeading.FindStringSubmatch(trimmed) == nil && !parseBacklogRow(trimmed).Structured
}

// ReadAttention reads the Overlord's order of the goblins in progress, top
// first; a home that never saved one has none.
func ReadAttention(h home.Home) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(h.State, attentionFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fleet: read attention order: %w", err)
	}
	var order []string
	if err := json.Unmarshal(data, &order); err != nil {
		return nil, fmt.Errorf("fleet: read attention order: %w", err)
	}
	return order, nil
}

// WriteAttention saves the Overlord's order of the goblins in progress.
func WriteAttention(h home.Home, order []string) error {
	data, err := json.Marshal(order)
	if err != nil {
		return err
	}
	if err := fsx.AtomicWriteFile(filepath.Join(h.State, attentionFile), data); err != nil {
		return fmt.Errorf("fleet: write attention order: %w", err)
	}
	return nil
}

// SortByAttention puts the items order names first, in its order, and keeps
// the rest after them in the order they had.
func SortByAttention[T any](items []T, order []string, id func(T) string) {
	rank := func(item T) int {
		if at := slices.Index(order, id(item)); at >= 0 {
			return at
		}
		return len(order)
	}
	slices.SortStableFunc(items, func(a, b T) int { return cmp.Compare(rank(a), rank(b)) })
}
