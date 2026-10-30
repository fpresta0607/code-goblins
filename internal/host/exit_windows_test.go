package host

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestALaunchRecordsItsHostsExitAfterStartup(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		code     uint32
		isForced bool
	}{
		{name: "terminal exits", code: 0},
		{name: "host is terminated with zero", code: 0, isForced: true},
		{name: "host is terminated", code: 23, isForced: true},
		{name: "Windows status is retained", code: 0xc000013a, isForced: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			stateDir, record := launch(t)
			viewer := connect(t, record)
			viewer.waitFor(t, "ready")
			before := time.Now().UTC()

			// Act
			if testCase.isForced {
				process, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(record.HostPID))
				if err != nil {
					t.Fatal(err)
				}
				err = windows.TerminateProcess(process, testCase.code)
				windows.CloseHandle(process)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				typeLine(t, viewer, "exit 3")
				viewer.waitForExit(t)
			}
			if !exited(record.HostPID) {
				t.Fatal("the host did not end")
			}

			// Assert
			want := fmt.Sprintf("host: pid %d ended with exit code %d (0x%08x)", record.HostPID, testCase.code, testCase.code)
			var logged string
			for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(10 * time.Millisecond) {
				data, err := os.ReadFile(filepath.Join(stateDir, "hosts", "g1.log"))
				if err != nil {
					t.Fatal(err)
				}
				logged = string(data)
				if strings.Contains(logged, want) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("host log = %q, want %q after Launch returned", logged, want)
				}
			}
			for line := range strings.Lines(logged) {
				if strings.Contains(line, want) {
					stamp, _, _ := strings.Cut(line, " ")
					at, err := time.Parse(time.RFC3339Nano, stamp)
					if err != nil || at.Before(before) || at.After(time.Now().UTC()) {
						t.Errorf("host exit timestamp = %q (%v), want its UTC exit time", stamp, err)
					}
					if testCase.code != 0 && !strings.Contains(line, "exit status") {
						t.Errorf("host exit = %q, want the wait error", line)
					}
				}
			}
			if testCase.isForced {
				if _, err := ReadEnd(stateDir, "g1"); !os.IsNotExist(err) {
					t.Errorf("terminal end = %v, want the host exit kept without inventing a terminal exit", err)
				}
			} else if ended, err := ReadEnd(stateDir, "g1"); err != nil || ended.Code != 3 {
				t.Errorf("terminal end = %+v, %v; want terminal exit code 3", ended, err)
			}
			t.Logf("host lifecycle:\n%s", logged)
		})
	}
}

func TestTheHostsUnreadInputDiagnosticHasAUTCDate(t *testing.T) {
	// Arrange
	stateDir, record := launch(t)
	viewer := connect(t, record)
	viewer.waitFor(t, "ready")
	readNow := filepath.Join(t.TempDir(), "read-now")
	typeLine(t, viewer, "wait-file "+readNow)
	viewer.waitFor(t, "waiting for the file")
	before := time.Now().UTC()

	// Act
	typeLine(t, viewer, "input left unread")

	// Assert
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		data, err := os.ReadFile(filepath.Join(stateDir, "hosts", "g1.log"))
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.Lines(string(data)) {
			if strings.Contains(line, "input records are still unread") {
				stamp, _, _ := strings.Cut(line, " ")
				at, err := time.Parse(time.RFC3339Nano, stamp)
				if err != nil || at.Before(before) || at.After(time.Now().UTC()) {
					t.Fatalf("unread-input diagnostic = %q, want its UTC date (%v)", line, err)
				}
				t.Logf("unread-input diagnostic: %s", line)
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("host log = %q, want the unread-input diagnostic", data)
		}
	}
}
