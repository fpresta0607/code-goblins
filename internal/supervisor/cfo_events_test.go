package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
)

// registerNativeCFO registers this test process as a CFO of harness in
// native terminal cfo, in native session session, holding the home's session
// lock as its registration leaves it.
func registerNativeCFO(t *testing.T, stateDir, harness, session string) {
	t.Helper()
	process, err := lock.AcquireOwner(stateDir, os.Getpid(), session)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release(stateDir) })
	data, err := json.Marshal(primaryRegistration{Host: NativeCFOTerminal, Agent: harness, Process: *process})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "primary.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// spoolCFOEvents spools one event of a CFO session's native hook for each
// hook name, a millisecond apart, and returns them.
func spoolCFOEvents(t *testing.T, h home.Home, harness, session string, names ...string) []nativehook.Event {
	t.Helper()
	now := time.Now()
	var events []nativehook.Event
	for index, name := range names {
		payload, err := json.Marshal(map[string]string{"hook_event_name": name, "session_id": session, "cwd": h.Root})
		if err != nil {
			t.Fatal(err)
		}
		// stall-accepted: each event's timestamp, a millisecond after the one before, which no wait compares with the clock.
		e, err := nativehook.Normalize(bytes.NewReader(payload), nativehook.Context{Harness: harness, Now: now.Add(time.Duration(index) * time.Millisecond)})
		if err != nil {
			t.Fatal(err)
		}
		if err := nativehook.Spool(h.State, e); err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	return events
}

// cfoEventService is a supervisor of h that reads 8 GB of memory and commit
// free and hands each compaction it is told to make to compacted.
func cfoEventService(t *testing.T, h home.Home, compacted *[]nativehook.Event) *Service {
	t.Helper()
	store, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	plenty := func() (Memory, error) { return Memory{Available: 8 * gigabyte, CommitAvailable: 8 * gigabyte}, nil }
	return &Service{Store: store, work: make(chan struct{}, 1), Options: Options{
		Dispatch: &Dispatch{Memory: plenty},
		CompactCFO: func(_ home.Home, e nativehook.Event, _ time.Time) error {
			*compacted = append(*compacted, e)
			return nil
		},
	}}
}

// A Codex or pi CFO's native hook only spools its turn end, so the
// supervisor raises the idle wake as it ingests it: when the registered CFO's
// turn ends with no goblin at work while work waits and memory is free, and
// nothing after it began another turn. A Claude CFO's turn-end guard raises
// its own, and another session's turn end is not the CFO's (issue #1213).
func TestTheSupervisorRaisesTheIdleWakeFromANativeCFOsTurnEnd(t *testing.T) {
	cases := []struct {
		name, registered, harness, session string
		hooks                              []string
		isWoken                            bool
	}{
		{name: "a Codex CFO's turn end", registered: "codex", harness: "codex", session: "native-cfo", hooks: []string{"SessionStart", "Stop"}, isWoken: true},
		{name: "a pi CFO's turn end", registered: "pi", harness: "pi", session: "native-cfo", hooks: []string{"session_start", "agent_settled"}, isWoken: true},
		{name: "a turn end another turn followed", registered: "codex", harness: "codex", session: "native-cfo", hooks: []string{"SessionStart", "Stop", "UserPromptSubmit"}},
		{name: "a Claude CFO's turn end", registered: "claude", harness: "claude", session: "native-cfo", hooks: []string{"SessionStart", "Stop"}},
		{name: "another session's turn end", registered: "codex", harness: "codex", session: "another-session", hooks: []string{"SessionStart", "Stop"}},
		{name: "no CFO registered", harness: "codex", session: "native-cfo", hooks: []string{"SessionStart", "Stop"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			h := bareHome(t)
			queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins)", plainBrief)
			if testCase.registered != "" {
				registerNativeCFO(t, h.State, testCase.registered, "native-cfo")
			}
			var compacted []nativehook.Event
			s := cfoEventService(t, h, &compacted)
			spoolCFOEvents(t, h, testCase.harness, testCase.session, testCase.hooks...)

			// Act
			s.cycle(context.Background(), false)

			// Assert
			if s.lastError != "" {
				t.Fatalf("the cycle met %q", s.lastError)
			}
			if session := s.Store.Snapshot().Sessions[testCase.harness+"/"+testCase.session]; session.LastEventID == "" {
				t.Fatal("the cycle ingested none of the session's events, so it proves nothing")
			}
			wakes := fleetWakeRecords(t, h, "idle")
			if !testCase.isWoken {
				if len(wakes) != 0 {
					t.Fatalf("idle wakes %+v, want none", wakes)
				}
				return
			}
			if len(wakes) != 1 || wakes[0].Key != "turn" || !strings.Contains(wakes[0].Detail, "next: start next-task") {
				t.Fatalf("idle wakes %+v, want the one keyed turn naming next-task", wakes)
			}
		})
	}
}

// A Codex or pi CFO's native hook only spools its compaction, so the
// supervisor writes the checkpoint and queues its wake as it ingests the
// events, once each, and only for the registered CFO's own session while it
// holds the primary home: a compaction of any other session, or of a CFO
// that no longer holds the home, does nothing (issue #1213).
func TestTheSupervisorCompactsOnlyTheNativeCFOThatHoldsTheHome(t *testing.T) {
	cases := []struct {
		name, session                       string
		isPrimary, isHeldElsewhere, isMoved bool
	}{
		{name: "the registered CFO holding the home", session: "native-cfo", isPrimary: true, isMoved: true},
		{name: "another session", session: "another-session", isPrimary: true},
		{name: "a CFO another process took the home from", session: "native-cfo", isPrimary: true, isHeldElsewhere: true},
		{name: "a home that is not primary", session: "native-cfo"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			h := bareHome(t)
			if testCase.isPrimary {
				primaryHome(t, h)
			}
			registerNativeCFO(t, h.State, "codex", "native-cfo")
			if testCase.isHeldElsewhere {
				holdHomeElsewhere(t, h.State)
			}
			var compacted []nativehook.Event
			s := cfoEventService(t, h, &compacted)
			events := spoolCFOEvents(t, h, "codex", testCase.session, "SessionStart", "PreCompact", "PostCompact")

			// Act
			s.cycle(context.Background(), false)
			for _, replayed := range events[1:] {
				if err := nativehook.Spool(h.State, replayed); err != nil {
					t.Fatal(err)
				}
			}
			s.cycle(context.Background(), false)

			// Assert
			if s.lastError != "" {
				t.Fatalf("the cycles met %q", s.lastError)
			}
			if entries, err := os.ReadDir(nativehook.SpoolDir(h.State)); err != nil || len(entries) != 0 {
				t.Fatalf("the native inbox still holds %v (%v), so the compaction events were never ingested", entries, err)
			}
			if issues := s.Store.Snapshot().Issues; len(issues) != 0 {
				t.Fatalf("the cycles quarantined events instead of accepting them: %v", issues)
			}
			if !testCase.isMoved {
				if len(compacted) != 0 {
					t.Fatalf("compacted %+v, want nothing", compacted)
				}
				return
			}
			if len(compacted) != 2 || compacted[0].ID != events[1].ID || compacted[1].ID != events[2].ID {
				t.Fatalf("compacted %+v, want the PreCompact then the PostCompact event once each", compacted)
			}
		})
	}
}

// A compaction starts and ends no turn: the session's record keeps its last
// turn's phase and event, which a goblin's evaluation its Stop asked for must
// still name to run, and the compaction is ingested once however often it is
// replayed.
func TestACompactionLeavesTheSessionsTurnAsItWas(t *testing.T) {
	// Arrange
	s, h := testStore(t)
	now := time.Now().UTC()
	stop := event(t, h, "Stop", "goblin-session", "turn", now.Add(time.Millisecond))
	compacting := event(t, h, "PreCompact", "goblin-session", "turn", now.Add(2*time.Millisecond))
	for _, e := range []nativehook.Event{event(t, h, "SessionStart", "goblin-session", "", now), stop} {
		if err := s.Accept(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := nativehook.Spool(h.State, compacting); err != nil {
		t.Fatal(err)
	}

	// Act
	accepted, err := s.Ingest()
	if err != nil {
		t.Fatal(err)
	}
	if err := nativehook.Spool(h.State, compacting); err != nil {
		t.Fatal(err)
	}
	replayed, err := s.Ingest()
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	if len(accepted) != 1 || accepted[0].ID != compacting.ID || len(replayed) != 0 {
		t.Fatalf("ingested %+v then %+v, want the compaction once", accepted, replayed)
	}
	if session := s.Snapshot().Sessions["codex/goblin-session"]; session.Phase != "settled" || session.LastEventID != stop.ID {
		t.Fatalf("session after its compaction = %+v, want it settled at its Stop", session)
	}
}

// primaryHome makes h a primary home, as cfo install leaves one.
func primaryHome(t *testing.T, h home.Home) {
	t.Helper()
	for _, name := range []string{"AGENTS.md", home.InstalledMarker} {
		if err := os.WriteFile(filepath.Join(h.Root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	repository := exec.Command("git", "init", "-q")
	repository.Dir = h.Root
	if out, err := repository.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}

// holdHomeElsewhere hands the home's session lock to another live process,
// which runs until the test ends.
func holdHomeElsewhere(t *testing.T, stateDir string) {
	t.Helper()
	other := execx.Command("ping", "-n", "30", "127.0.0.1")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = other.Process.Kill()
		_ = other.Wait()
	})
	if err := lock.Release(stateDir); err != nil {
		t.Fatal(err)
	}
	if _, err := lock.AcquireOwner(stateDir, other.Process.Pid, "another-session"); err != nil {
		t.Fatal(err)
	}
}
