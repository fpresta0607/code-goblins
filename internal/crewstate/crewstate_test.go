package crewstate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type fakeEndpoint struct {
	exists     bool
	existsErr  error
	busy       herdr.BusyState
	busyErr    error
	structural bool
	structErr  error
}

func (f fakeEndpoint) Exists(context.Context, herdr.Target) (bool, error) {
	return f.exists, f.existsErr
}

func (f fakeEndpoint) BusyState(context.Context, herdr.Target) (herdr.BusyState, error) {
	return f.busy, f.busyErr
}

func (f fakeEndpoint) Validate(context.Context, state.TaskMeta) (bool, error) {
	return f.structural, f.structErr
}

func writeMeta(t *testing.T, dir, id string, worktree string) {
	t.Helper()
	if err := state.WriteTaskMeta(dir, state.TaskMeta{
		ID:               id,
		Worktree:         worktree,
		Backend:          "herdr",
		HerdrSession:     "fleet",
		HerdrWorkspaceID: "ws",
		HerdrTabID:       "tab",
		HerdrPaneID:      "pane",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestResolveStatePrecedenceAndStructuralIdleGate(t *testing.T) {
	ctx := context.Background()
	t.Run("missing metadata", func(t *testing.T) {
		got, err := Resolve(ctx, t.TempDir(), "g1", fakeEndpoint{})
		if err != nil || got.State != Unknown || got.Source != SourceMetadata {
			t.Fatalf("Resolve = %+v, %v; want missing metadata unknown", got, err)
		}
	})
	t.Run("missing worktree", func(t *testing.T) {
		dir := t.TempDir()
		writeMeta(t, dir, "g1", filepath.Join(dir, "missing"))
		got, err := Resolve(ctx, dir, "g1", fakeEndpoint{})
		if err != nil || got.State != Unknown || got.Source != SourceMetadata {
			t.Fatalf("Resolve = %+v, %v; want missing worktree unknown", got, err)
		}
	})
	t.Run("unreadable endpoint never trusts status", func(t *testing.T) {
		dir := t.TempDir()
		worktree := filepath.Join(dir, "worktree")
		if err := os.Mkdir(worktree, 0o755); err != nil {
			t.Fatal(err)
		}
		writeMeta(t, dir, "g1", worktree)
		if err := state.AppendStatus(dir, "g1", "done: old success"); err != nil {
			t.Fatal(err)
		}
		got, err := Resolve(ctx, dir, "g1", fakeEndpoint{exists: true, busy: herdr.BusyUnknown, structural: true})
		if err != nil || got.State != Unknown || got.Source != SourceNone {
			t.Fatalf("Resolve = %+v, %v; want unknown without status fallback", got, err)
		}
	})
	t.Run("exact busy wins over status", func(t *testing.T) {
		dir := t.TempDir()
		worktree := filepath.Join(dir, "worktree")
		if err := os.Mkdir(worktree, 0o755); err != nil {
			t.Fatal(err)
		}
		writeMeta(t, dir, "g1", worktree)
		if err := state.AppendStatus(dir, "g1", "failed: old failure"); err != nil {
			t.Fatal(err)
		}
		got, err := Resolve(ctx, dir, "g1", fakeEndpoint{exists: true, busy: herdr.BusyWorking, structural: true})
		if err != nil || got.State != Working || got.Source != SourceEndpoint {
			t.Fatalf("Resolve = %+v, %v; want live working endpoint", got, err)
		}
	})
	t.Run("idle without structural match never trusts status", func(t *testing.T) {
		dir := t.TempDir()
		worktree := filepath.Join(dir, "worktree")
		if err := os.Mkdir(worktree, 0o755); err != nil {
			t.Fatal(err)
		}
		writeMeta(t, dir, "g1", worktree)
		if err := state.AppendStatus(dir, "g1", "done: old success"); err != nil {
			t.Fatal(err)
		}
		got, err := Resolve(ctx, dir, "g1", fakeEndpoint{exists: true, busy: herdr.BusyIdle})
		if err != nil || got.State != Unknown || got.Source != SourceNone {
			t.Fatalf("Resolve = %+v, %v; want structural mismatch unknown", got, err)
		}
	})
}

func TestResolveMapsStatusOnlyAfterExactStructuralIdle(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		line string
		want State
	}{
		{line: "working: active", want: Working},
		{line: "needs-decision: choose", want: Parked},
		{line: "blocked: missing input", want: Blocked},
		{line: "paused: vendor wait", want: Paused},
		{line: "done: shipped", want: Done},
		{line: "failed: test failure", want: Failed},
		{line: "resolved: stale decision", want: Unknown},
		{line: "unrecognized", want: Unknown},
	}
	for _, test := range tests {
		t.Run(test.line, func(t *testing.T) {
			dir := t.TempDir()
			worktree := filepath.Join(dir, "worktree")
			if err := os.Mkdir(worktree, 0o755); err != nil {
				t.Fatal(err)
			}
			writeMeta(t, dir, "g1", worktree)
			if err := state.AppendStatus(dir, "g1", test.line); err != nil {
				t.Fatal(err)
			}
			got, err := Resolve(ctx, dir, "g1", fakeEndpoint{exists: true, busy: herdr.BusyIdle, structural: true})
			if err != nil {
				t.Fatal(err)
			}
			if got.State != test.want || got.Source != SourceStatus {
				t.Errorf("Resolve = %+v, want state %q from status", got, test.want)
			}
		})
	}
}

func TestParseStatusLineAndFoldOpenDecisionsUseKeyedForms(t *testing.T) {
	verb, detail, ok := ParseStatusLine("needs-decision [key=api.shape]: choose API")
	if !ok || verb != "needs-decision" || detail != "choose API" {
		t.Fatalf("ParseStatusLine = %q, %q, %t", verb, detail, ok)
	}
	if _, _, ok := ParseStatusLine("not a status event"); ok {
		t.Error("ParseStatusLine accepted an invalid status line")
	}

	got := FoldOpenDecisions([]string{
		"needs-decision [key=api.shape]: choose API",
		"blocked: [key=db] migration is blocked",
		"done: unrelated terminal event",
		"resolved: [key=api.shape] captain chose v2",
		"needs-decision: [key=bad/key] invalid key is inert",
		"captain-held [key=db]: durable captain record",
		"needs-decision: [key=still-open] select region",
	})
	want := []Decision{{Key: "still-open", Verb: "needs-decision", Detail: "select region"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FoldOpenDecisions = %#v, want %#v", got, want)
	}

	if _, err := Resolve(context.Background(), t.TempDir(), "g1", fakeEndpoint{existsErr: errors.New("unreadable")}); err != nil {
		t.Errorf("Resolve must classify unavailable metadata without surfacing endpoint fake error: %v", err)
	}
}
