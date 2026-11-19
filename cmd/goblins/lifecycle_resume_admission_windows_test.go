package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/disk"
	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

func TestResumeAdmissionPricesTheResumeByTheEngineItResumesOnAboveTheHomesFloor(t *testing.T) {
	// The task paused on claude, which has no measured cost, and resumes on
	// codex, which measured 3 GB. The home keeps a floor of 6 GB, so the resume
	// needs 9 GB; the default floor of 4 GB with an unmeasured 1 GB start, or
	// the floor of 6 GB with the paused harness's 1 GB, would both admit 8 GB.
	tests := []struct {
		name         string
		fleetJSON    string
		availableGB  uint64
		wantRefusal  []string
		wantAdmitted bool
	}{
		{
			name:         "refuses free memory the default floor and an unmeasured start would admit",
			fleetJSON:    `{"memory_floor_gb":6}`,
			availableGB:  8,
			wantRefusal:  []string{"Only 8.0 GB of memory", "a codex start needs 9.0 GB", "floor 6.0", "start 3.0"},
			wantAdmitted: false,
		},
		{
			name:         "admits free memory above the home's floor and the resume engine's measured cost",
			fleetJSON:    `{"memory_floor_gb":6}`,
			availableGB:  10,
			wantAdmitted: true,
		},
		{
			name:         "admits free memory exactly at the home's floor and the measured cost",
			fleetJSON:    `{"memory_floor_gb":6}`,
			availableGB:  9,
			wantAdmitted: true,
		},
		{
			name:         "refuses when the home's settings cannot be read",
			fleetJSON:    `not json`,
			availableGB:  64,
			wantRefusal:  []string{"the room for a start cannot be read, so nothing resumes", "the memory floor cannot be read"},
			wantAdmitted: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h := testHome(t)
			writeTestFile(t, filepath.Join(h.Root, "config", "fleet.json"), test.fleetJSON)
			writeTestFile(t, filepath.Join(h.State, "start-costs.json"), `{"schema":"ceo-start-costs.v1","harnesses":{"codex":[{"bytes":3221225472,"at":"2026-11-01T00:00:00Z","task":"measured"}]}}`)
			meta := state.TaskMeta{ID: "paused", SpawnGen: "generation-1", Backend: "native", Harness: "claude"}
			if err := state.WriteEngineChoice(h.State, state.EngineChoice{ID: meta.ID, Generation: meta.SpawnGen, Harness: "codex", When: "resume", Requested: time.Now()}); err != nil {
				t.Fatal(err)
			}
			memory := func() (supervisor.Memory, error) {
				return supervisor.Memory{Available: test.availableGB << 30, CommitAvailable: 64 << 30}, nil
			}
			processes := func() ([]fleettree.Process, error) { return nil, nil }
			plentyOfDisk := func(home.Home) (supervisor.Disk, error) {
				return supervisor.Disk{Reading: disk.Reading{Drive: "C:", Free: 500 << 30, Total: 1000 << 30}, Floor: 20 << 30}, nil
			}

			// Act
			err := resumeAdmission(h, meta, memory, processes, plentyOfDisk, time.Now().UTC())

			// Assert
			if test.wantAdmitted {
				if err != nil {
					t.Fatalf("resumeAdmission refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("resumeAdmission admitted, want a refusal")
			}
			for _, want := range test.wantRefusal {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not contain %q", err, want)
				}
			}
		})
	}
}

func TestResumeAdmissionReportsAMachineReaderThatFails(t *testing.T) {
	// Arrange
	h := testHome(t)
	meta := state.TaskMeta{ID: "paused", SpawnGen: "generation-1", Backend: "native", Harness: "codex"}
	roomy := func() (supervisor.Memory, error) {
		return supervisor.Memory{Available: 64 << 30, CommitAvailable: 64 << 30}, nil
	}
	unreadableMemory := func() (supervisor.Memory, error) { return supervisor.Memory{}, errors.New("memory is unreadable") }
	unreadableDisk := func(home.Home) (supervisor.Disk, error) { return supervisor.Disk{}, errors.New("disk is unreadable") }
	noProcesses := func() ([]fleettree.Process, error) { return nil, nil }
	plentyOfDisk := func(home.Home) (supervisor.Disk, error) {
		return supervisor.Disk{Reading: disk.Reading{Drive: "C:", Free: 500 << 30, Total: 1000 << 30}, Floor: 20 << 30}, nil
	}

	// Act
	memoryErr := resumeAdmission(h, meta, unreadableMemory, noProcesses, plentyOfDisk, time.Now().UTC())
	diskErr := resumeAdmission(h, meta, roomy, noProcesses, unreadableDisk, time.Now().UTC())

	// Assert
	if memoryErr == nil || memoryErr.Error() != "memory is unreadable" {
		t.Errorf("memory failure = %v, want the memory reader's error", memoryErr)
	}
	if diskErr == nil || !strings.Contains(diskErr.Error(), "free disk cannot be read, so nothing resumes: disk is unreadable") {
		t.Errorf("disk failure = %v, want the disk refusal naming the reader's error", diskErr)
	}
}
