package supervisor

import "testing"

func TestMachineMemoryReadsThisMachine(t *testing.T) {
	// Act
	available, total, err := MachineMemory()

	// Assert
	if err != nil {
		t.Fatalf("MachineMemory: %v", err)
	}
	if total < 1<<30 || available == 0 || available > total {
		t.Fatalf("available %d of %d bytes, want some of a real machine's memory", available, total)
	}
}
