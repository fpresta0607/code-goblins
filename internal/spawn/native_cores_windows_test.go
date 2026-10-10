package spawn

import (
	"context"
	"os"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/processor"
)

// A goblin's builds and tests took every processor core at the priority of
// the Overlord's own apps (processor.fleetThreads has the measurement). The
// terminal a spawn starts a goblin in keeps to the fleet's threads, which
// leaves his apps a quarter of the performance cores.
func TestANativeGoblinKeepsOffTheCoresLeftToTheOverlordsApps(t *testing.T) {
	// Arrange
	own, err := processor.ProcessThreads(windows.CurrentProcess())
	if err != nil {
		t.Fatal(err)
	}
	fleet, err := processor.MachineFleetThreads()
	if err != nil {
		t.Fatal(err)
	}
	if fleet == 0 || fleet == own {
		if os.Getenv("CI") == "true" {
			t.Fatalf("CI runs this test on the processor threads %#x, where the fleet's work keeps to %#x, so a goblin kept off the apps' cores went untested", own, fleet)
		}
		t.Skipf("this test runs on the processor threads %#x, where the fleet's work keeps to %#x, as inside a goblin's terminal, so what a spawn sets cannot be told from what it inherits", own, fleet)
	}
	f := newQuickFixture(t)

	// Act
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Assert
	terminal, found := f.terminal()
	if !found {
		t.Fatal("the spawn recorded no terminal")
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(terminal.ChildPID))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process)
	got, err := processor.ProcessThreads(process)
	if err != nil {
		t.Fatal(err)
	}
	if got != fleet {
		t.Fatalf("the goblin's harness may run on the processor threads %#x, want the fleet's, %#x, of this test's %#x", got, fleet, own)
	}
}
