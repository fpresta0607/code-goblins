package janitor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/reap"
)

func TestSweepReportsABacklogWhoseHistoryPassesItsLimit(t *testing.T) {
	note := "a note written weeks ago, kept for the record\n"
	large := strings.Repeat(note, BacklogHistoryLimit/len(note)+1)
	small := strings.Repeat(note, BacklogHistoryLimit/len(note)/2)
	tests := []struct {
		name      string
		section   string
		isStray   bool
		wantsText string
	}{
		{name: "history over the limit", section: "## Notes from last month\n" + large, isStray: true, wantsText: `"Notes from last month" at line 6`},
		{name: "history under the limit", section: "## Notes from last month\n" + small},
		{name: "a large section with an open row", section: "## Fleet defects\n- [ ] fd1 - Fix it\n" + large},
		{name: "a large In flight section", section: "## In flight\n" + large},
		{name: "a large Done section", section: "## Done\n- [x] d1 - Done\n" + large},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newSweepFixture(t)
			writeFiles(t, f.home.Data, map[string]string{"backlog.md": "# Backlog\n\n## Queued\n- [ ] q1 - Open work\n\n" + test.section})

			// Act
			record := Sweep(context.Background(), f.config(reap.Inventory{}))

			// Assert
			item, isStray := has(record.Strays, filepath.Join(f.home.Data, "backlog.md"))
			if isStray != test.isStray || isStray && !strings.Contains(item.Detail, test.wantsText) {
				t.Errorf("strays = %+v, want a report of the backlog: %t, naming %q", record.Strays, test.isStray, test.wantsText)
			}
		})
	}
}
