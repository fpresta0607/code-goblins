package watch

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func TestContinuousWatchSurvivesTwoDecisionsAndCancels(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	appendFile(t, filepath.Join(dir, "first.status"), "needs-decision: first question\n")
	delivered := 0
	cfg := Config{Home: home.Home{State: dir}, Poll: time.Millisecond, SignalGrace: time.Millisecond, Sleep: time.Sleep}
	cfg.Continuous = true
	cfg.OnEvent = func(reason string) {
		delivered++
		if delivered == 1 {
			appendFile(t, filepath.Join(dir, "second.status"), "needs-decision: second question\n")
		} else {
			cancel()
		}
	}
	_, err := RunContext(ctx, cfg)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
	if delivered != 2 {
		t.Fatalf("delivered %d decisions, want 2", delivered)
	}
	records, err := wake.Pending(dir)
	if err != nil || len(records) != 2 {
		t.Fatalf("durable wakes = %+v, %v", records, err)
	}
}
