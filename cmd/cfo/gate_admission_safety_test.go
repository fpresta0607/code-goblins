package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

func TestGateAdmissionFailureStartsNoCommand(t *testing.T) {
	for _, cause := range []string{"memory", "missing_reader", "floor_expired", "store", "capacity", "slot_override", "corrupt_custody", "corrupt_second_slot"} {
		t.Run(cause, func(t *testing.T) {
			// Arrange
			dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
			t.Chdir(dir)
			runtime := standIn()
			switch cause {
			case "memory":
				runtime.availableMemory = func() (supervisor.Memory, error) { return supervisor.Memory{}, errors.New("memory unavailable") }
			case "missing_reader":
				runtime.availableMemory = nil
			case "floor_expired":
				runtime.availableMemory = func() (supervisor.Memory, error) {
					return supervisor.Memory{Available: supervisor.MemoryFloor - 1, CommitAvailable: 16 << 30}, nil
				}
				runtime.gateWaitLimit = 20 * time.Millisecond
			case "store":
				file := filepath.Join(os.Getenv("CFO_VERIFY_DIR"), "slots")
				if err := os.WriteFile(file, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "capacity":
				store := filepath.Join(os.Getenv("CFO_VERIFY_DIR"), "slots")
				if err := os.MkdirAll(store, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(store, "capacity.json"), []byte(`{"capacity":3}`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "slot_override":
				t.Setenv("CFO_VERIFY_SLOTS", "2")
			case "corrupt_custody", "corrupt_second_slot":
				store := filepath.Join(os.Getenv("CFO_VERIFY_DIR"), "slots")
				if err := os.MkdirAll(store, 0o755); err != nil {
					t.Fatal(err)
				}
				slot := "slot-1"
				if cause == "corrupt_second_slot" {
					slot = "slot-2"
					if err := os.WriteFile(filepath.Join(store, "capacity.json"), []byte(`{"capacity":2}`), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(store, slot), []byte(`{}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			started := 0
			runtime.gateRun = func([]string, string, []string, io.Writer, io.Writer) (int, error) {
				started++
				return 0, nil
			}

			// Act
			var stdout, stderr bytes.Buffer
			exit := gateTestWith(runtime, &stdout, &stderr)

			// Assert
			if exit == 0 || started != 0 {
				t.Fatalf("admission failure launched %d commands: exit=%d stdout=%s stderr=%s", started, exit, &stdout, &stderr)
			}
			report, _ := lastReport(t)
			if report.Status == "passed" || len(report.Checks) != 2 {
				t.Fatalf("failed admission report lost required scope: %+v", report)
			}
			for _, check := range report.Checks {
				if !check.Start.IsZero() || check.DurationSeconds != 0 || check.ExitCode != -1 || check.Status != "not_run" {
					t.Fatalf("unstarted command claimed execution: %+v", check)
				}
			}
			if report.QueueNote == "" || (cause == "floor_expired" && report.QueueSeconds < runtime.gateWaitLimit.Seconds()) {
				t.Fatalf("failed admission lost its reason or queue duration: %+v", report)
			}
		})
	}
}

func TestGateAdmissionRequiresBothMemoryFloors(t *testing.T) {
	for _, capacity := range []int{1, 2} {
		t.Run(fmt.Sprint(capacity), func(t *testing.T) {
			floor := uint64(capacity) * supervisor.MemoryFloor
			for _, sample := range []struct {
				name             string
				physical, commit uint64
				shouldRun        bool
			}{
				{"both_at_floor", floor, floor, true},
				{"physical_one_byte_short", floor - 1, 16 << 30, false},
				{"commit_one_byte_short", 16 << 30, floor - 1, false},
				{"both_one_byte_short", floor - 1, floor - 1, false},
				{"physical_zero", 0, 16 << 30, false},
				{"commit_zero", 16 << 30, 0, false},
			} {
				t.Run(sample.name, func(t *testing.T) {
					// Arrange
					dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
					t.Chdir(dir)
					runtime := standIn()
					runtime.gateWaitLimit = 20 * time.Millisecond
					runtime.availableMemory = func() (supervisor.Memory, error) {
						return supervisor.Memory{Available: sample.physical, CommitAvailable: sample.commit}, nil
					}
					store := filepath.Join(os.Getenv("CFO_VERIFY_DIR"), "slots")
					if err := os.MkdirAll(store, 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(store, "capacity.json"), []byte(fmt.Sprintf(`{"capacity":%d}`, capacity)), 0o600); err != nil {
						t.Fatal(err)
					}
					started := 0
					runtime.gateRun = func([]string, string, []string, io.Writer, io.Writer) (int, error) { started++; return 0, nil }

					// Act
					var stdout, stderr bytes.Buffer
					exit := gateTestWith(runtime, &stdout, &stderr)

					// Assert
					if sample.shouldRun {
						if exit != 0 || started != 2 {
							t.Fatalf("paired boundary refused: exit=%d commands=%d %s", exit, started, &stderr)
						}
					} else if exit != 1 || started != 0 || !strings.Contains(stderr.String(), "memory floor wait expired") {
						t.Fatalf("paired shortage admitted: exit=%d commands=%d %s", exit, started, &stderr)
					}
				})
			}
		})
	}
}

func TestGateAdmissionRechecksMemoryBeforeEachCommand(t *testing.T) {
	// Arrange
	dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	runtime := standIn()
	started := 0
	runtime.gateRun = func([]string, string, []string, io.Writer, io.Writer) (int, error) { started++; return 0, nil }
	runtime.availableMemory = func() (supervisor.Memory, error) {
		if started > 0 {
			return supervisor.Memory{}, errors.New("second reading unavailable")
		}
		return plenty()
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := gateTestWith(runtime, &stdout, &stderr)

	// Assert
	report, _ := lastReport(t)
	if exit != 1 || started != 1 || len(report.Checks) != 2 || report.Checks[0].Status != "passed" || report.Checks[1].Status != "not_run" || !report.Checks[1].Start.IsZero() {
		t.Fatalf("second admission lost its scope: exit=%d commands=%d report=%+v", exit, started, report)
	}
}

func TestGateTurnsReportsUnavailableCustodyOrMemory(t *testing.T) {
	for _, cause := range []string{"memory", "missing_reader", "corrupt_custody", "capacity"} {
		t.Run(cause, func(t *testing.T) {
			// Arrange
			t.Setenv("CFO_VERIFY_DIR", t.TempDir())
			available := plenty
			switch cause {
			case "memory":
				available = func() (supervisor.Memory, error) { return supervisor.Memory{}, errors.New("unavailable") }
			case "missing_reader":
				available = nil
			case "corrupt_custody":
				store := filepath.Join(os.Getenv("CFO_VERIFY_DIR"), "slots")
				if err := os.MkdirAll(store, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(store, "slot-1"), []byte(`{}`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "capacity":
				store := filepath.Join(os.Getenv("CFO_VERIFY_DIR"), "slots")
				if err := os.MkdirAll(store, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(store, "capacity.json"), []byte(`{"capacity":3}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			var stdout, stderr bytes.Buffer
			exit := gateTurns(available, &stdout, &stderr)

			// Assert
			if exit != 2 || stderr.Len() == 0 {
				t.Fatalf("unavailable state reported success: exit=%d stdout=%s stderr=%s", exit, &stdout, &stderr)
			}
			if (cause == "corrupt_custody" || cause == "capacity") && strings.Contains(stdout.String(), "none waits") {
				t.Fatal("unreadable admission reported empty")
			}
		})
	}
}

func TestGateTurnsUsesSharedCapacityFloor(t *testing.T) {
	for _, capacity := range []int{1, 2} {
		for _, sample := range []struct {
			name             string
			physical, commit uint64
			shouldReport     bool
		}{
			{"exact", uint64(capacity) * supervisor.MemoryFloor, uint64(capacity) * supervisor.MemoryFloor, false},
			{"physical_short", uint64(capacity)*supervisor.MemoryFloor - 1, 16 << 30, true},
			{"commit_short", 16 << 30, uint64(capacity)*supervisor.MemoryFloor - 1, true},
		} {
			t.Run(fmt.Sprint(capacity)+"/"+sample.name, func(t *testing.T) {
				// Arrange
				t.Setenv("CFO_VERIFY_DIR", t.TempDir())
				store := filepath.Join(os.Getenv("CFO_VERIFY_DIR"), "slots")
				if err := os.MkdirAll(store, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(store, "capacity.json"), []byte(fmt.Sprintf(`{"capacity":%d}`, capacity)), 0o600); err != nil {
					t.Fatal(err)
				}
				available := func() (supervisor.Memory, error) {
					return supervisor.Memory{Available: sample.physical, CommitAvailable: sample.commit}, nil
				}

				// Act
				var stdout, stderr bytes.Buffer
				exit := gateTurns(available, &stdout, &stderr)

				// Assert
				floor := uint64(capacity) * supervisor.MemoryFloor
				reported := strings.Contains(stdout.String(), "both require")
				if exit != 0 || stderr.Len() != 0 || reported != sample.shouldReport || (reported && !strings.Contains(stdout.String(), fmt.Sprintf("%.1f GB floor", float64(floor)/(1<<30)))) {
					t.Fatalf("shared floor report is wrong: exit=%d stdout=%s stderr=%s", exit, &stdout, &stderr)
				}
			})
		}
	}
}
