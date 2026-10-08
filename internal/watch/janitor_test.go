package watch

import (
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/janitor"
)

// TestStrayWakeFiresOnlyForAStrayNotReportedBefore: the janitor reports the
// same strays on every pass, and one going away is nothing the CFO must act
// on; only a stray it has not been told about wakes it.
func TestStrayWakeFiresOnlyForAStrayNotReportedBefore(t *testing.T) {
	strays := func(paths ...string) janitor.Record {
		var record janitor.Record
		for _, path := range paths {
			record.Strays = append(record.Strays, janitor.Item{Kind: "worktree", Path: path, Detail: "no task records it"})
		}
		return record
	}
	cases := []struct {
		name     string
		previous janitor.Record
		current  janitor.Record
		wantWake bool
	}{
		{"the first stray", strays(), strays(`C:\dev\a`), true},
		{"the same strays again", strays(`C:\dev\a`, `C:\dev\b`), strays(`C:\dev\b`, `C:\dev\a`), false},
		{"the same stray in another case", strays(`C:\dev\a`), strays(`c:\DEV\a`), false},
		{"one of two went away", strays(`C:\dev\a`, `C:\dev\b`), strays(`C:\dev\a`), false},
		{"a new one beside the old", strays(`C:\dev\a`), strays(`C:\dev\a`, `C:\dev\c`), true},
		{"none left", strays(`C:\dev\a`), strays(), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			detail := strayWake(c.previous, c.current)
			if (detail != "") != c.wantWake {
				t.Fatalf("strayWake = %q, want a wake %v", detail, c.wantWake)
			}
			for _, stray := range c.current.Strays {
				if c.wantWake && !strings.Contains(detail, stray.Path) {
					t.Errorf("strayWake = %q, want every stray named, %s among them", detail, stray.Path)
				}
			}
		})
	}
}
