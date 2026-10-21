package spawn

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// A goblin's uv, pip and npm resolve only releases a week old, so a hijacked
// release is pulled before it reaches one: on 2026-10-21 one reached three
// goblins within ten minutes of its upload. uv takes no cutoff in a worktree
// that tracks a uv.lock, which it would set aside, and a project that names
// its own cutoff keeps it.
func TestSpawnHoldsTheGoblinsPackageManagersToReleasesAWeekOld(t *testing.T) {
	for _, test := range []struct {
		name     string
		uvLocks  string
		env      map[string]string
		wantUV   string
		wantLine string
	}{
		{name: "a worktree with no uv.lock", wantUV: "7 days"},
		{name: "a worktree that tracks uv.lock", uvLocks: "uv.lock\x00services/api/uv.lock\x00", wantLine: "\ncooldown: uv takes no cutoff from the goblin's terminal, since the worktree tracks uv.lock, services/api/uv.lock and uv sets aside a lock that does not record its terminal's cutoff"},
		{name: "a project that names its own uv cutoff", env: map[string]string{"UV_EXCLUDE_NEWER": "3 days"}, wantUV: "3 days"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newQuickFixture(t)
			f.runner.uvLocks = test.uvLocks
			writeWorktreeManifest(t, f.dataDir, f.project, worktree.Manifest{Project: "primary", Env: test.env})
			started := time.Now()

			// Act
			result, err := f.service.Spawn(context.Background(), f.request)

			// Assert
			if err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			env := named(f.events(t), "env")[0].Env
			if got := env["PIP_UPLOADED_PRIOR_TO"]; got == nil || *got != "P7D" {
				t.Errorf("the goblin started with PIP_UPLOADED_PRIOR_TO = %v, want P7D", got)
			}
			before, err := time.Parse(time.RFC3339, value(env["npm_config_before"]))
			if want := started.Add(-7 * 24 * time.Hour); err != nil || before.Before(want.Add(-time.Second)) || before.After(time.Now().Add(-7*24*time.Hour)) {
				t.Errorf("the goblin started with npm_config_before = %q, %v, want 7 days before it started", value(env["npm_config_before"]), err)
			}
			if got := value(env["UV_EXCLUDE_NEWER"]); got != test.wantUV {
				t.Errorf("the goblin started with UV_EXCLUDE_NEWER = %q, want %q", got, test.wantUV)
			}
			if hasLine := strings.Contains(result.Output, "\ncooldown: "); hasLine != (test.wantLine != "") || !strings.Contains(result.Output, test.wantLine) {
				t.Errorf("output = %q, want %q", result.Output, test.wantLine)
			}
			if _, written := typedBrief(t, f.fixture, "task-7"); !strings.Contains(written, "hold its package managers to releases uploaded at least 7 days ago") || !strings.Contains(written, "Never unset or override them") {
				t.Errorf("instruction = %q, want the cooldown named and kept", written)
			}
		})
	}
}

// A relaunch builds its terminal's environment again, so the cooldown is
// there only if the relaunch sets it too.
func TestSwitchHoldsTheGoblinsPackageManagersToReleasesAWeekOld(t *testing.T) {
	// Arrange
	f := newSwitchFixture(t, harness.Control{StopCommand: "/exit"})

	// Act
	if _, err := f.service.Switch(context.Background(), SwitchRequest{ID: f.meta.ID, Model: "gpt-9"}); err != nil {
		t.Fatalf("Switch: %v", err)
	}

	// Assert
	env := named(f.events(t), "env")[0].Env
	if value(env["UV_EXCLUDE_NEWER"]) != "7 days" || value(env["PIP_UPLOADED_PRIOR_TO"]) != "P7D" || value(env["npm_config_before"]) == "" {
		t.Errorf("the new harness started with UV_EXCLUDE_NEWER = %q, PIP_UPLOADED_PRIOR_TO = %q, npm_config_before = %q, want the cooldown", value(env["UV_EXCLUDE_NEWER"]), value(env["PIP_UPLOADED_PRIOR_TO"]), value(env["npm_config_before"]))
	}
}

// An install that resolves at a package index is not the goblin's first
// step: the spawn says why on its dependencies line, and the goblin is told
// to ask for a lock rather than resolve the ranges itself.
func TestSpawnGivesTheGoblinNoInstallStepThatResolvesVersions(t *testing.T) {
	// Arrange
	f := newQuickFixture(t)
	writeWorktreeManifest(t, f.dataDir, f.project, worktree.Manifest{Project: "primary", Dependencies: worktree.Dependencies{Install: []string{"uv venv", "uv pip install -r requirements.txt"}}})

	// Act
	result, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if !strings.Contains(result.Output, "\ndependencies: none installed, since worktree.json names the install \"uv pip install -r requirements.txt\", which resolves versions at a package index instead of installing a lock with hashes. Lock the project") || strings.Contains(result.Output, "installs them as its first step") {
		t.Errorf("output = %q, want the refused install named", result.Output)
	}
	if _, written := typedBrief(t, f.fixture, "task-7"); !strings.Contains(written, "the spawn gave you no step that installs them: worktree.json names the install") || !strings.Contains(written, "Never install them by resolving versions at a package index yourself") || strings.Contains(written, "before you build or test anything, run") {
		t.Errorf("instruction = %q, want no install step and the refusal", written)
	}
	if status, err := state.TailStatus(f.stateDir, "task-7", 20); err != nil || strings.Contains(strings.Join(status, "\n"), "installing its dependencies first") {
		t.Errorf("status = %q, %v, want no install named on the card", status, err)
	}
}

func value(variable *string) string {
	if variable == nil {
		return ""
	}
	return *variable
}
