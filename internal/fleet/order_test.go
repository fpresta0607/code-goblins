package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReorderQueuedWritesTheOrderIntoTheQueuedSection(t *testing.T) {
	const before = "# Backlog\n\n" +
		"## Queued\n\n" +
		"Priority note the CFO wrote.\n" +
		"- 2026-09-26: a dated note\n\n" +
		"- **alpha** - First task (repo: code-goblins)\n" +
		"  detail: alpha's detail\n" +
		"  second detail line\n" +
		"- **beta** - Second task blocked-by: alpha (repo: code-goblins)\n" +
		"- [ ] gamma - Third task (repo: PrecisionDocs-AI)\n" +
		"  detail: gamma's detail\n" +
		"- **held** - Parked in place (hold-kind: parked)\n\n" +
		"## Parked\n\n" +
		"- **parked** - Set aside\n\n" +
		"## Done\n" +
		"- **done** - Finished\n"
	tests := []struct {
		name  string
		order []string
		added map[string]string
		want  string
	}{
		{
			name:  "rows move with their detail lines and everything else stays",
			order: []string{"gamma", "alpha", "beta"},
			want: "# Backlog\n\n" +
				"## Queued\n\n" +
				"Priority note the CFO wrote.\n" +
				"- 2026-09-26: a dated note\n\n" +
				"- [ ] gamma - Third task (repo: PrecisionDocs-AI)\n" +
				"  detail: gamma's detail\n" +
				"- **alpha** - First task (repo: code-goblins)\n" +
				"  detail: alpha's detail\n" +
				"  second detail line\n" +
				"- **beta** - Second task blocked-by: alpha (repo: code-goblins)\n" +
				"- **held** - Parked in place (hold-kind: parked)\n\n" +
				"## Parked\n\n" +
				"- **parked** - Set aside\n\n" +
				"## Done\n" +
				"- **done** - Finished\n",
		},
		{
			name:  "a task with a brief and no row gets a row where it was placed",
			order: []string{"beta", "brief-only", "alpha", "gamma"},
			added: map[string]string{"brief-only": "- **brief-only** - brief-only (repo: code-goblins)"},
			want: "# Backlog\n\n" +
				"## Queued\n\n" +
				"Priority note the CFO wrote.\n" +
				"- 2026-09-26: a dated note\n\n" +
				"- **beta** - Second task blocked-by: alpha (repo: code-goblins)\n" +
				"- **brief-only** - brief-only (repo: code-goblins)\n" +
				"- **alpha** - First task (repo: code-goblins)\n" +
				"  detail: alpha's detail\n" +
				"  second detail line\n" +
				"- [ ] gamma - Third task (repo: PrecisionDocs-AI)\n" +
				"  detail: gamma's detail\n" +
				"- **held** - Parked in place (hold-kind: parked)\n\n" +
				"## Parked\n\n" +
				"- **parked** - Set aside\n\n" +
				"## Done\n" +
				"- **done** - Finished\n",
		},
		{
			name:  "the order it already has leaves the file as it was",
			order: []string{"alpha", "beta", "gamma"},
			want:  before,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h := snapshotHome(t)
			path := writeBacklog(t, h.Data, before)

			// Act
			err := ReorderQueued(h, test.order, test.added)

			// Assert
			if err != nil {
				t.Fatalf("ReorderQueued: %v", err)
			}
			if got := readFile(t, path); got != test.want {
				t.Fatalf("backlog.md =\n%s\nwant\n%s", got, test.want)
			}
			backlog, err := ReadBacklog(h)
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, row := range backlog.Queued {
				if row.Structured {
					ids = append(ids, row.ID)
				}
			}
			if !reflect.DeepEqual(ids, test.order) {
				t.Fatalf("queued rows read back as %v, want %v", ids, test.order)
			}
		})
	}
}

func TestReorderQueuedKeepsWindowsLineEndings(t *testing.T) {
	// Arrange
	h := snapshotHome(t)
	path := writeBacklog(t, h.Data, "## Queued\r\n- **a** - A\r\n  detail: a\r\n- **b** - B\r\n")

	// Act
	err := ReorderQueued(h, []string{"b", "a"}, nil)

	// Assert
	if err != nil {
		t.Fatalf("ReorderQueued: %v", err)
	}
	if got, want := readFile(t, path), "## Queued\r\n- **b** - B\r\n- **a** - A\r\n  detail: a\r\n"; got != want {
		t.Fatalf("backlog.md = %q, want %q", got, want)
	}
}

func TestReorderQueuedRefusesAnOrderThatIsNotTheQueue(t *testing.T) {
	const content = "## Queued\n- **a** - A\n- **b** - B\n"
	tests := []struct {
		name  string
		order []string
		added map[string]string
	}{
		{name: "a row is missing", order: []string{"b"}},
		{name: "a row is unknown", order: []string{"b", "a", "c"}},
		{name: "a row is named twice", order: []string{"a", "b", "a"}},
		{name: "an added row already has one", order: []string{"b", "a"}, added: map[string]string{"a": "- **a** - A"}},
		{name: "an added row is not placed", order: []string{"b", "a"}, added: map[string]string{"c": "- **c** - C"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h := snapshotHome(t)
			path := writeBacklog(t, h.Data, content)

			// Act
			err := ReorderQueued(h, test.order, test.added)

			// Assert
			if !errors.Is(err, ErrQueueChanged) {
				t.Fatalf("ReorderQueued = %v, want ErrQueueChanged", err)
			}
			if got := readFile(t, path); got != content {
				t.Fatalf("a refused order changed backlog.md to %q", got)
			}
		})
	}
}

func TestReorderQueuedRefusesABacklogThatQueuesATaskTwice(t *testing.T) {
	// Arrange
	h := snapshotHome(t)
	const content = "## Queued\n- **a** - A\n- **b** - B\n- **a** - A again\n"
	path := writeBacklog(t, h.Data, content)

	// Act
	err := ReorderQueued(h, []string{"b", "a"}, nil)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "queues a twice") {
		t.Fatalf("ReorderQueued = %v, want a refusal naming the repeated row", err)
	}
	if got := readFile(t, path); got != content {
		t.Fatalf("a refused order changed backlog.md to %q", got)
	}
}

func TestReorderQueuedOrdersRowsAcrossTwoQueuedSections(t *testing.T) {
	// Arrange
	h := snapshotHome(t)
	path := writeBacklog(t, h.Data, "## Queued\n- **a** - A\n\n## Done\n- **d** - D\n\n## Queued\n- **b** - B\n  detail: b\n")

	// Act
	err := ReorderQueued(h, []string{"b", "a"}, nil)

	// Assert
	if err != nil {
		t.Fatalf("ReorderQueued: %v", err)
	}
	if got, want := readFile(t, path), "## Queued\n- **b** - B\n  detail: b\n\n## Done\n- **d** - D\n\n## Queued\n- **a** - A\n"; got != want {
		t.Fatalf("backlog.md = %q, want %q", got, want)
	}
}

func TestReorderQueuedAddsRowsToAQueueWithNone(t *testing.T) {
	// Arrange
	h := snapshotHome(t)
	path := writeBacklog(t, h.Data, "## Queued\n\nNothing queued yet.\n\n## Done\n")

	// Act
	err := ReorderQueued(h, []string{"x", "y"}, map[string]string{"x": "- **x** - x", "y": "- **y** - y"})

	// Assert
	if err != nil {
		t.Fatalf("ReorderQueued: %v", err)
	}
	if got, want := readFile(t, path), "## Queued\n\nNothing queued yet.\n- **x** - x\n- **y** - y\n\n## Done\n"; got != want {
		t.Fatalf("backlog.md = %q, want %q", got, want)
	}
}

func TestReorderQueuedWithoutAQueuedSectionRefusesToAddRows(t *testing.T) {
	// Arrange
	h := snapshotHome(t)
	writeBacklog(t, h.Data, "## Done\n")

	// Act
	err := ReorderQueued(h, []string{"x"}, map[string]string{"x": "- **x** - x"})

	// Assert
	if err == nil || !strings.Contains(err.Error(), "no Queued section") {
		t.Fatalf("ReorderQueued = %v, want a refusal naming the missing section", err)
	}
}

func TestAttentionOrderRoundTripsAndStartsEmpty(t *testing.T) {
	// Arrange
	h := snapshotHome(t)

	// Act
	empty, emptyErr := ReadAttention(h)
	writeErr := WriteAttention(h, []string{"b", "a"})
	order, readErr := ReadAttention(h)

	// Assert
	if emptyErr != nil || len(empty) != 0 {
		t.Fatalf("ReadAttention on a new home = %v, %v; want no order", empty, emptyErr)
	}
	if writeErr != nil || readErr != nil {
		t.Fatalf("WriteAttention = %v, ReadAttention = %v", writeErr, readErr)
	}
	if !reflect.DeepEqual(order, []string{"b", "a"}) {
		t.Fatalf("attention order = %v, want [b a]", order)
	}
}

func TestSortByAttentionPutsOrderedTasksFirstAndKeepsTheRest(t *testing.T) {
	// Arrange
	ids := []string{"a", "b", "c", "d"}

	// Act
	SortByAttention(ids, []string{"c", "gone", "a"}, func(id string) string { return id })

	// Assert
	if want := []string{"c", "a", "b", "d"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("sorted = %v, want %v", ids, want)
	}
}

func writeBacklog(t *testing.T, dir, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "backlog.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
