package watch

import (
	"testing"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/reap"
)

// The monitor reads Herdr every minute (the session snapshot, the agent list,
// each task's pane capture and a stalled goblin's process info) and the orphan
// sweep every ten; each read without a socket starts a herdr process, about a
// second on a loaded machine. Every Herdr client ConfigFromEnv builds reads
// over the session's socket, through one cache that asks Herdr's status once.
func TestConfigFromEnvReadsHerdrOverOneSocketCache(t *testing.T) {
	// Arrange and Act
	cfg := ConfigFromEnv(home.Home{State: t.TempDir()})
	if cfg.Cleanup != nil {
		defer cfg.Cleanup()
	}

	// Assert
	probe := cfg.Monitor.Probe.(monitor.BackendProber).Herdr.(*monitor.HerdrProber).Client.(*herdr.Client)
	progress := cfg.Monitor.Progress.(*monitor.HostProgress).Panes.(*herdr.Client)
	sweep := cfg.Reap.Inventory.(reap.Collector).Panes.(*herdr.Client)
	if probe.Sockets == nil || progress.Sockets != probe.Sockets || sweep.Sockets != probe.Sockets {
		t.Errorf("socket caches: monitor probe %p, progress %p, sweep %p; want one shared cache", probe.Sockets, progress.Sockets, sweep.Sockets)
	}
}
