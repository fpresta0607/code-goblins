package supervisor

import (
	"slices"
	"testing"
)

func TestMachineMemoryReadsThisMachine(t *testing.T) {
	// Act
	memory, err := MachineMemory()

	// Assert
	if err != nil {
		t.Fatalf("MachineMemory: %v", err)
	}
	if memory.Total < 1<<30 || memory.Available == 0 || memory.Available > memory.Total {
		t.Fatalf("available %d of %d bytes, want some of a real machine's memory", memory.Available, memory.Total)
	}
	if memory.CommitLimit < 1<<30 || memory.CommitAvailable == 0 || memory.CommitAvailable > memory.CommitLimit {
		t.Fatalf("commit %d of %d bytes, want some of a real machine's commit", memory.CommitAvailable, memory.CommitLimit)
	}
	if memory.PagedPool == 0 || memory.NonpagedPool == 0 || memory.PagedPool+memory.NonpagedPool > memory.Total {
		t.Fatalf("paged pool %d and nonpaged pool %d bytes, want a real kernel's pools", memory.PagedPool, memory.NonpagedPool)
	}
}

func TestCommitHoldersReadsThisMachine(t *testing.T) {
	// Act
	holders, err := CommitHolders()

	// Assert
	if err != nil {
		t.Fatalf("CommitHolders: %v", err)
	}
	if len(holders) != commitHolderCount {
		t.Fatalf("holders = %+v, want the %d apps holding the most commit", holders, commitHolderCount)
	}
	if !slices.IsSortedFunc(holders, func(a, b CommitHolder) int { return int(b.Commit>>20) - int(a.Commit>>20) }) || holders[0].Commit == 0 || holders[0].Name == "" || holders[0].Processes == 0 {
		t.Fatalf("holders = %+v, want named apps, most commit first", holders)
	}
}
