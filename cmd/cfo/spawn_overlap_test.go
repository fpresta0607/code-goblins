package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

// overlapSpawn runs cfo spawn for task nw-sync against a faked repository
// read, and reports whether the spawn itself ran.
type overlapSpawn struct {
	home     home.Home
	checkout string
	brief    string
	activity tickets.Activity
	readErr  error
	spawnErr error
	spawned  bool
}

func newOverlapSpawn(t *testing.T, task string) *overlapSpawn {
	t.Helper()
	fixture := &overlapSpawn{home: testHome(t), checkout: filepath.Join(t.TempDir(), "northwind-api"), activity: teammateRepository()}
	if err := os.MkdirAll(fixture.checkout, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture.brief = filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(fixture.brief, []byte("# Brief nw-sync\n\n## Task\n\n"+task+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (f *overlapSpawn) run(extra ...string) (int, string, string) {
	deps := testCommandRuntimeForHome(f.home)
	deps.repoActivity = func(context.Context, string, time.Time) (tickets.Activity, error) { return f.activity, f.readErr }
	deps.spawn = func(context.Context, home.Home, spawn.Request) (spawn.Result, error) {
		f.spawned = f.spawnErr == nil
		return spawn.Result{Output: "spawned nw-sync"}, f.spawnErr
	}
	var stdout, stderr bytes.Buffer
	args := append([]string{"spawn", "nw-sync", "--project", f.checkout, "--brief", f.brief, "--harness", "claude"}, extra...)
	code := runWithRuntime(args, &stdout, &stderr, deps)
	return code, stdout.String(), stderr.String()
}

func TestSpawnRefusesWhenATeammateHasWorkInTheBriefsArea(t *testing.T) {
	cases := []struct {
		name  string
		extra []string
	}{
		{name: "no word from the CFO"},
		{name: "a blank reason", extra: []string{"--overlap-ok", "  "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			fixture := newOverlapSpawn(t, "Say why in tasks/billing_sync.py.")

			// Act
			code, _, stderr := fixture.run(tc.extra...)

			// Assert
			if code != 1 || fixture.spawned {
				t.Fatalf("exit = %d, spawned = %v, want a refusal before anything starts; stderr=%s", code, fixture.spawned, stderr)
			}
			for _, want := range []string{"PR #412 by ana-teammate changes tasks/billing_sync.py", `--overlap-ok "<why>"`} {
				if !strings.Contains(stderr, want) {
					t.Fatalf("stderr lacks %q:\n%s", want, stderr)
				}
			}
			if note, _ := tickets.ReadOverlapNote(fixture.home.State, "nw-sync"); note != "" {
				t.Fatalf("an overlap was recorded as accepted for a refused spawn: %q", note)
			}
		})
	}
}

func TestSpawnStartsBesideATeammateWhenTheCFOSaysWhy(t *testing.T) {
	// Arrange
	fixture := newOverlapSpawn(t, "Say why in tasks/billing_sync.py.")

	// Act
	code, _, stderr := fixture.run("--overlap-ok", "it only adds a log line")

	// Assert
	if code != 0 || !fixture.spawned {
		t.Fatalf("exit = %d, spawned = %v, stderr = %s", code, fixture.spawned, stderr)
	}
	note, err := tickets.ReadOverlapNote(fixture.home.State, "nw-sync")
	if err != nil || note != "PR #412: it only adds a log line" {
		t.Fatalf("overlap note = %q, %v, want what it overlaps and why, for the ticket", note, err)
	}
	lines, err := state.TailStatus(fixture.home.State, "nw-sync", 10)
	if err != nil || len(lines) != 1 || !strings.Contains(lines[0], "overlap accepted: it only adds a log line") || !strings.Contains(lines[0], "PR #412 by ana-teammate changes tasks/billing_sync.py") {
		t.Fatalf("status log = %q, %v, want the reason and the overlap it accepted", lines, err)
	}
}

func TestSpawnRecordsNoAcceptedOverlapWhenTheSpawnFails(t *testing.T) {
	// Arrange
	fixture := newOverlapSpawn(t, "Say why in tasks/billing_sync.py.")
	fixture.spawnErr = errors.New("no terminal could be opened")

	// Act
	code, _, _ := fixture.run("--overlap-ok", "it only adds a log line")

	// Assert
	if code != 1 {
		t.Fatalf("exit = %d, want the spawn's own failure", code)
	}
	if note, _ := tickets.ReadOverlapNote(fixture.home.State, "nw-sync"); note != "" {
		t.Fatalf("overlap note = %q for a task that never started", note)
	}
	if lines, _ := state.TailStatus(fixture.home.State, "nw-sync", 10); len(lines) != 0 {
		t.Fatalf("status log = %q for a task that never started, which would hide it from the queue", lines)
	}
}

func TestSpawnStartsWithoutAWordWhereNoTeammateOverlaps(t *testing.T) {
	ana := tickets.Actor{Login: "ana-teammate"}
	overlord := tickets.Actor{Login: "fpresta0607"}
	recently := time.Now().Add(-2 * time.Hour)
	cases := []struct {
		name       string
		task       string
		change     func(*overlapSpawn)
		wantStderr string
	}{
		{name: "the teammate's work is elsewhere", task: "Say why in services/invoice_service.py."},
		{name: "nobody else has worked in the repository for 30 days, though a teammate's old pull request is still open", task: "Say why in tasks/billing_sync.py.", change: func(f *overlapSpawn) {
			f.activity.Events = []tickets.Event{
				{Kind: tickets.EventPullRequest, Number: 420, Author: overlord, At: recently},
				{Kind: tickets.EventPullRequest, Number: 412, Author: ana, At: time.Now().Add(-60 * 24 * time.Hour)},
			}
		}},
		{name: "a teammate works there but only the Overlord's own pull request overlaps", task: "Say why in tasks/billing_sync.py.", change: func(f *overlapSpawn) {
			f.activity.PullRequests[0].Author = overlord
		}},
		{name: "the one overlap is the issue the brief says the task is for", task: "Resolve issue #415: say why the billing sync reports a mismatch.", change: func(f *overlapSpawn) {
			f.activity.PullRequests = nil
			f.activity.Issues = []tickets.Issue{{Number: 415, Title: "Billing sync reports a mismatch", Author: ana, CreatedAt: recently}}
		}},
		{name: "the one overlap is the issue the task already claimed", task: "Say why the billing sync reports a mismatch.", change: func(f *overlapSpawn) {
			f.activity.PullRequests = nil
			f.activity.Issues = []tickets.Issue{{Number: 415, Title: "Billing sync reports a mismatch", Author: ana, CreatedAt: recently}}
			if err := tickets.WriteRecord(f.home.State, tickets.Record{TaskID: "nw-sync", Repository: "fpresta0607/northwind-api", Number: 415, IsClaimed: true}); err != nil {
				panic(err)
			}
		}},
		{name: "GitHub cannot be read", task: "Say why in tasks/billing_sync.py.", change: func(f *overlapSpawn) {
			f.readErr = errors.New("gh api graphql exited 1: HTTP 502")
		}, wantStderr: "HTTP 502"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			fixture := newOverlapSpawn(t, tc.task)
			if tc.change != nil {
				tc.change(fixture)
			}

			// Act
			code, _, stderr := fixture.run()

			// Assert
			if code != 0 || !fixture.spawned {
				t.Fatalf("exit = %d, spawned = %v, want the task started; stderr=%s", code, fixture.spawned, stderr)
			}
			if tc.wantStderr == "" && stderr != "" {
				t.Fatalf("stderr = %q, want nothing said", stderr)
			}
			if tc.wantStderr != "" && (!strings.Contains(stderr, tc.wantStderr) || !strings.Contains(stderr, "unchecked")) {
				t.Fatalf("stderr = %q, want it to say the task starts unchecked and why", stderr)
			}
			if note, _ := tickets.ReadOverlapNote(fixture.home.State, "nw-sync"); note != "" {
				t.Fatalf("overlap note = %q where nothing overlapped", note)
			}
		})
	}
}

func TestTheDefaultRuntimeChecksOverlapBeforeASpawn(t *testing.T) {
	if defaultCommandRuntime().repoActivity == nil {
		t.Fatal("the real cfo has no repository read, so cfo spawn would start every task unchecked")
	}
}
