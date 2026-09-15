package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/watch"
)

func TestStatusSeparatesProcessFromFreshObservation(t *testing.T) {
	dir := t.TempDir()
	if got := Status(dir); got.Healthy || got.Reason != "supervisor_down" {
		t.Fatalf("missing: %+v", got)
	}
	if _, err := lock.AcquireExclusiveNamed(dir, LockName); err != nil {
		t.Fatal(err)
	}
	defer lock.ReleaseExclusiveNamed(dir, LockName)
	if got := Status(dir); got.Healthy || got.Reason != "stale_evidence" {
		t.Fatalf("no heartbeat: %+v", got)
	}
	if err := monitor.TouchHeartbeat(dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := Status(dir); got.Healthy || !got.Observing || got.Reason != "notification_failed" {
		t.Fatalf("observation must not imply delivery: %+v", got)
	}
	if err := writeDeliveryFile(dir, "delivery.json", Delivery{CheckedAt: time.Now().UTC(), State: "ready"}); err != nil {
		t.Fatal(err)
	}
	if got := Status(dir); !got.Healthy {
		t.Fatalf("verified observer and delivery: %+v", got)
	}
}

func TestRunRetainsWakeAndStopsOnlyItsOwnGeneration(t *testing.T) {
	dir := t.TempDir()
	h := home.Home{Root: dir, State: dir, Data: filepath.Join(dir, "data")}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	svc := Service{Home: h, Config: func() watch.Config {
		return watch.Config{Home: h, Poll: time.Millisecond, SignalGrace: time.Millisecond, Sleep: time.Sleep}
	}}
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()
	deadline := time.Now().Add(time.Second)
	status := Status(dir)
	for !status.Observing && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		status = Status(dir)
	}
	if !status.Observing {
		t.Fatalf("supervisor did not become ready: %+v", status)
	}
	if err := os.WriteFile(filepath.Join(dir, "task.status"), []byte("blocked: decision\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Stop(dir); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if Status(dir).Healthy {
		t.Fatal("exited supervisor remains healthy")
	}
}
