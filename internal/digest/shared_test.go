package digest

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// Issue 2365: the CEO's digest shows the shared services on one line, as
// the supervisor last checked them, with how many goblins wait on each, and
// says when the record is old enough that the supervisor may not be checking.
func TestTheDigestShowsTheSharedServicesAndWhoWaitsOnThem(t *testing.T) {
	checkedAt := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)
	record := supervisor.SharedServices{Schema: "ceo-shared-services.v1", CheckedAt: checkedAt, Services: []supervisor.SharedService{
		{Name: supervisor.SharedGateDaemon, State: "busy", PID: 276, Runs: 3, Waiting: []string{"fern", "huey"}},
		{Name: supervisor.SharedHeavyLine, State: "short", Holders: []string{"wanda"}, Queued: []string{"fern"}, AvailableBytes: 4 << 30, FloorBytes: 5 << 30},
		{Name: supervisor.SharedDocker, State: "running", Holders: []string{"fern"}},
		{Name: supervisor.SharedScrawl, State: "spinning", PID: 20764, CorePercent: 112},
	}}
	for _, c := range []struct {
		name       string
		isRecorded bool
		age        time.Duration
		want       string
	}{
		{"a fresh record", true, time.Minute, "SHARED: gate-daemon busy (pid 276, 3 runs), 2 goblin(s) waiting; heavy-line short (1 held, 1 in line, 4.0 of 5.0 GB); docker running (held by fern); scrawl spinning (pid 20764, 112% of a core)\n"},
		{"an old record", true, 10 * time.Minute, "; checked 10m0s ago, so the supervisor may not be checking\n"},
		{"no record yet", false, 0, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			if c.isRecorded {
				data, err := json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(stateDir, "shared-services.json"), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer

			// Act
			writeShared(stateDir, checkedAt.Add(c.age), &werr{w: &out})

			// Assert
			if c.want == "" && out.Len() != 0 || !strings.Contains(out.String(), c.want) {
				t.Fatalf("shared line = %q, want %q", out.String(), c.want)
			}
		})
	}
}

// The CEO's session digest, long and brief, carries the SHARED line, and a
// daemon whose database could not count its runs says so, never 0 runs.
func TestTheLongAndBriefDigestsCarryTheSharedLine(t *testing.T) {
	for _, test := range []struct {
		name    string
		compose func(t *testing.T, h home.Home) string
	}{
		{name: "the long digest", compose: func(t *testing.T, h home.Home) string {
			var out bytes.Buffer
			if err := Compose(h, os.Getpid(), "s1", &out); err != nil {
				t.Fatalf("Compose: %v", err)
			}
			return out.String()
		}},
		{name: "the brief digest", compose: composeBrief},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h := newDigestHome(t)
			record := supervisor.SharedServices{Schema: "ceo-shared-services.v1", CheckedAt: time.Now(), Services: []supervisor.SharedService{
				{Name: supervisor.SharedGateDaemon, State: "busy", PID: 276, Runs: -1, Waiting: []string{"huey"}},
			}}
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(h.State, "shared-services.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			out := test.compose(t, h)

			// Assert
			if want := "SHARED: gate-daemon busy (pid 276, runs its database could not count), 1 goblin(s) waiting\n"; !strings.Contains(out, want) {
				t.Fatalf("%s carries no %q:\n%s", test.name, want, out)
			}
		})
	}
}
