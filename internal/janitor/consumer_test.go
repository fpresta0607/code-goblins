package janitor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/reap"
)

func TestExecutionCustodyProtectsDetachedWorkersInBothSweeps(t *testing.T) {
	for _, hasMatchingIdentity := range []bool{true, false} {
		t.Run(fmt.Sprintf("matching-identity=%t", hasMatchingIdentity), func(t *testing.T) {
			// Arrange
			directory := t.TempDir()
			started := time.Date(2026, 10, 24, 4, 5, 32, 0, time.UTC)
			controller := lifecycle.Identity{PID: 30, Started: started}
			if !hasMatchingIdentity {
				controller.Started = started.Add(-time.Millisecond)
			}
			processes := []Process{
				{Process: lifecycle.Process{PID: 30, ParentPID: 900, Started: started, Name: "codex.exe", Directory: directory, Arguments: []string{"codex", "--dangerously-bypass-approvals-and-sandbox"}}},
				{Process: lifecycle.Process{PID: 31, ParentPID: 30, Started: started.Add(time.Second), Name: "codex.exe", Directory: directory, Arguments: []string{"codex", "--dangerously-bypass-approvals-and-sandbox"}}},
			}
			owners := []Owner{{ID: "supplier", Directories: []string{directory}}}
			processes = withControllers(processes, []lifecycle.Identity{controller})
			running := []proc.Entry{{PID: 30, Start: started}, {PID: 31, Start: started.Add(time.Second)}}
			rows := listed{
				{PID: 30, ParentPID: 900, Start: started, Name: "codex.exe", CommandLine: "codex --dangerously-bypass-approvals-and-sandbox"},
				{PID: 31, ParentPID: 30, Start: started.Add(time.Second), Name: "codex.exe", CommandLine: "codex --dangerously-bypass-approvals-and-sandbox"},
			}

			// Act
			ending, left, _, kept := planProcesses(processes, owners, nil, started.Add(time.Minute))
			inv, _, err := (reap.Collector{Home: home.Home{Root: directory, State: filepath.Join(directory, "state")}, Session: "default", Processes: rows, Sightings: func(context.Context) ([]reap.Sighting, []string, error) {
				return sightings(running, processes, owners), nil, nil
			}}).Collect(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			var orphans []int
			for _, finding := range reap.Classify(inv) {
				if finding.Class == reap.OrphanProcess {
					orphans = append(orphans, finding.PID)
				}
			}

			// Assert
			if hasMatchingIdentity {
				if len(ending) != 0 || len(left) != 0 || len(kept) != 2 || len(orphans) != 0 {
					t.Fatalf("active execution treated as orphan: end=%v left=%v keep=%v orphan=%v", ending, left, kept, orphans)
				}
			} else if len(ending) != 2 || len(orphans) == 0 {
				t.Fatalf("stale controller identity sheltered orphans: end=%v orphan=%v", ending, orphans)
			}
		})
	}
}

func TestUnreadableExecutionCustodyStopsTheProcessPlan(t *testing.T) {
	// Arrange
	want := errors.New("unreadable custody")
	cfg := Config{Processes: func(context.Context) ([]Process, error) { return nil, nil }, Controllers: func(context.Context) ([]lifecycle.Identity, error) { return nil, want }}

	// Act
	_, err := cfg.PlanProcesses(t.Context())

	// Assert
	if !errors.Is(err, want) {
		t.Fatalf("unreadable custody was treated as absent: %v", err)
	}
}

func TestBorrowedExecutableBelongsToItsConsumerAfterLauncherExits(t *testing.T) {
	for _, isSupplierFirst := range []bool{true, false} {
		for _, consumerState := range []string{"live", "gone", "unproven"} {
			t.Run(fmt.Sprintf("supplier-first=%t/consumer=%s", isSupplierFirst, consumerState), func(t *testing.T) {
				// Arrange: the supplier is paused, the consumer works, and pid 900 is gone.
				started := time.Date(2026, 10, 24, 4, 5, 32, 0, time.UTC)
				scratch := t.TempDir()
				consumer := lifecycle.Mark{Terminal: "consumer", ProofSum: "consumer-proof"}
				processes := []Process{
					{Process: lifecycle.Process{PID: 20, Started: started.Add(-time.Hour)}},
					{Process: lifecycle.Process{PID: 30, ParentPID: 900, Started: started, Name: "cfo-candidate.exe", Directory: t.TempDir(), Arguments: []string{filepath.Join(scratch, "cfo-candidate.exe"), "gate", "turn", "--detached"}, Mark: consumer}},
					{Process: lifecycle.Process{PID: 31, ParentPID: 30, Started: started.Add(time.Millisecond), Name: "conhost.exe", Directory: scratch}},
				}
				owners := []Owner{{ID: "supplier", Directories: []string{scratch}}, {ID: "consumer", HostPID: 20}}
				if consumerState != "unproven" {
					owners[1].Marks = []lifecycle.Mark{consumer}
				}
				if consumerState == "gone" {
					owners[1].HostPID = 0
				}
				if !isSupplierFirst {
					slices.Reverse(owners)
				}

				// Act
				ending, _, _, kept := planProcesses(processes, owners, nil, started.Add(time.Minute))
				running := []proc.Entry{{PID: 30, Start: started}, {PID: 31, Start: started.Add(time.Millisecond)}}
				sighted := sightings(running, processes, owners)

				// Assert: an unproven mark must not shelter a true orphan.
				if consumerState != "live" {
					wantOwner := "supplier"
					if consumerState == "gone" {
						wantOwner = "consumer"
					}
					if len(ending) != 2 || ending[0].Owner != wantOwner || ending[1].Owner != wantOwner {
						t.Fatalf("true orphan was not reclaimed: %+v", ending)
					}
					return
				}
				if len(ending) != 0 {
					t.Fatalf("supplier claims live consumer's detached controller: %+v", ending)
				}
				for _, pid := range []int{30, 31} {
					if !slices.ContainsFunc(kept, func(item ProcessItem) bool { return item.PID == pid && item.Owner == "consumer" }) {
						t.Errorf("consumer's pid %d not kept: %+v", pid, kept)
					}
					if !slices.ContainsFunc(sighted, func(item reap.Sighting) bool { return item.PID == pid && item.Owner == "consumer" }) {
						t.Errorf("orphan sweep lost consumer's pid %d: %+v", pid, sighted)
					}
				}
			})
		}
	}
}
