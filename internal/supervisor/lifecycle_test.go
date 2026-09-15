package supervisor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/watch"
)

func TestDetachedSupervisorHelper(t *testing.T) {
	if os.Getenv("CFO_TEST_SUPERVISOR_CHILD") != "1" {
		return
	}
	h := home.Home{Root: os.Getenv("CFO_HOME"), State: os.Getenv("CFO_STATE_OVERRIDE")}
	h.Data = filepath.Join(h.Root, "data")
	fmt.Println("retained child diagnostic")
	service := Service{Home: h, Config: func() watch.Config {
		return watch.Config{Home: h, Poll: 10 * time.Millisecond, SignalGrace: time.Millisecond}
	}, Deliver: func(context.Context) error {
		return writeDeliveryFile(h.State, "delivery.json", Delivery{CheckedAt: time.Now().UTC(), State: "ready"})
	}}
	if err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDetachedProcessDeathRestartAndBoundedLog(t *testing.T) {
	t.Setenv("CFO_TEST_SUPERVISOR_CHILD", "1")
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	if err := os.MkdirAll(h.State, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Stop(h.State); time.Sleep(150 * time.Millisecond) })
	process, err := lock.VerifiedProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := writeDeliveryFile(h.State, "primary.json", Primary{Target: herdr.Target{Session: "isolated", Pane: "fixture"}, Terminal: "fixture", Process: process}); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	launch := func() Health {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := startProcess(ctx, h, exec.Command(exe, "-test.run=^TestDetachedSupervisorHelper$")); err != nil {
			log, _ := os.ReadFile(filepath.Join(h.State, "supervisor.log"))
			t.Fatalf("%v status=%+v log=%s", err, Status(h.State), log)
		}
		return Status(h.State)
	}
	first := launch()
	again := launch()
	if first.PID == 0 || again.PID != first.PID {
		t.Fatalf("duplicate start %+v %+v", first, again)
	}
	child, err := os.FindProcess(first.PID)
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Release()
	deadline := time.Now().Add(3 * time.Second)
	for Status(h.State).Observing && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := Status(h.State); got.Observing || got.Reason != "supervisor_down" {
		t.Fatalf("death hidden: %+v", got)
	}
	// More than one MiB of synthetic crash output retains the tail only.
	if err := os.WriteFile(filepath.Join(h.State, "supervisor.log"), []byte(strings.Repeat("x", 2<<20)+"last crash evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	second := launch()
	if second.PID == first.PID {
		t.Fatal("unexpected PID reuse in fixture")
	}
	previous, err := os.ReadFile(filepath.Join(h.State, "supervisor.previous.log"))
	if err != nil || len(previous) != 1<<20 || !strings.HasSuffix(string(previous), "last crash evidence") {
		t.Fatalf("lost bounded crash log: %d %v", len(previous), err)
	}
	if err := Stop(h.State); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for Status(h.State).Observing && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if Status(h.State).Observing {
		t.Fatal("cooperative stop failed")
	}
}
