package supervisor

import (
	"errors"
	"testing"
)

func TestMachineGPUReadsThisMachine(t *testing.T) {
	// Act
	gpu, err := MachineGPU()

	// Assert
	if errors.Is(err, ErrNoGPU) {
		// CI's virtual machines have no graphics adapter Windows counts, so
		// this is proven on a machine that has one: the Overlord's read its
		// Intel adapter 79% busy and its NVIDIA adapter idle on 2026-10-09.
		t.Skipf("this machine has no graphics adapter to read: %v", err)
	}
	if err != nil {
		t.Fatalf("MachineGPU: %v", err)
	}
	t.Logf("this machine reads %+v", gpu)
	if len(gpu.Adapters) == 0 {
		t.Fatal("MachineGPU names no adapter on a machine whose engines Windows counts")
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
