package supervisor

import (
	"errors"
	"testing"
)

// A machine reads as what it has. One with graphics adapters names each with
// a share in use: the Overlord's read its Intel adapter 61% to 82% busy and
// its NVIDIA adapter idle on 2026-10-09. One with none, as CI's virtual
// machines, which count graphics engines yet have no adapter besides
// Windows' software renderer, reads ErrNoGPU and no adapters. On 2026-10-09
// CI's read as a GPU of no adapters and failed this test, which then took any
// machine whose engines Windows counts to have an adapter.
func TestMachineGPUReadsThisMachine(t *testing.T) {
	// Act
	gpu, err := MachineGPU()

	// Assert
	if errors.Is(err, ErrNoGPU) {
		if len(gpu.Adapters) != 0 {
			t.Fatalf("MachineGPU = %+v with %v, want no adapters beside that error", gpu, err)
		}
		t.Logf("this machine has no graphics adapter: %v", err)
		return
	}
	if err != nil {
		t.Fatalf("MachineGPU: %v", err)
	}
	t.Logf("this machine reads %+v", gpu)
	if len(gpu.Adapters) == 0 {
		t.Fatal("MachineGPU read a GPU of no adapters, want ErrNoGPU for a machine with none")
	}
	for _, adapter := range gpu.Adapters {
		if adapter.Name == "" || adapter.Busy < 0 || adapter.Busy > 1 {
			t.Errorf("adapter = %+v, want a named adapter with a share in use", adapter)
		}
	}
	if free := gpu.Free(); free < 0 || free > 1 {
		t.Errorf("Free = %v, want a share", free)
	}
}
