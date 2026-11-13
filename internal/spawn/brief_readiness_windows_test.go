package spawn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
)

func TestSpawnRechecksTheBriefAfterWaitingForItsLaunchTurn(t *testing.T) {
	// Arrange
	fixture := newFixture(t)
	if _, err := lock.AcquireExclusiveNamed(fixture.stateDir, spawnLockName); err != nil {
		t.Fatal(err)
	}
	isHeld := true
	defer func() {
		if isHeld {
			if err := lock.ReleaseExclusiveNamed(fixture.stateDir, spawnLockName); err != nil {
				t.Error(err)
			}
		}
	}()
	progress := lineWriter{lines: make(chan string, 1)}
	fixture.service.Progress = progress
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := fixture.service.Spawn(ctx, fixture.request); done <- err }()

	// Act: an author changes finalized choices while this start waits.
	select {
	case <-progress.lines:
	case <-ctx.Done():
		t.Fatal("spawn never waited for the held launch turn")
	}
	writeFile(t, fixture.request.BriefPath, strings.ReplaceAll(completedSpawnBrief, "mode: no-mistakes", "mode: local-only"))
	if err := lock.ReleaseExclusiveNamed(fixture.stateDir, spawnLockName); err != nil {
		t.Fatal(err)
	}
	isHeld = false

	// Assert
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "brief changed during admission") {
			t.Fatalf("waiting spawn = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("waiting spawn did not finish")
	}
	if _, err := os.Stat(filepath.Join(fixture.stateDir, fixture.request.ID+".meta")); !os.IsNotExist(err) {
		t.Fatalf("waiting spawn published stale choices: %v", err)
	}
}
