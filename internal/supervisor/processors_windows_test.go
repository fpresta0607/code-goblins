package supervisor

import (
	"bytes"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/processor"
)

func TestMachineProcessorsReadsThisMachine(t *testing.T) {
	// Arrange
	threads := int(windows.GetActiveProcessorCount(0))

	// Act
	first, err := MachineProcessors()
	second, secondErr := MachineProcessors()

	// Assert
	t.Logf("this machine reads %+v", first)
	if err != nil || secondErr != nil {
		t.Fatalf("MachineProcessors: %v, then %v", err, secondErr)
	}
	if first.PerformanceCores < 1 || first.EfficiencyCores < 0 || first.PerformanceCores+first.EfficiencyCores > threads {
		t.Fatalf("cores = %d performance and %d efficiency on a first processor group of %d threads, want that group's cores", first.PerformanceCores, first.EfficiencyCores, threads)
	}
	if first.Free < 0 || first.Free > 1 {
		t.Fatalf("free share of the performance cores = %v, want a share", first.Free)
	}
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("a reading straight after the first = %+v, want the first again, %+v, and not a judgment over a few milliseconds", second, first)
	}
}

// A goblin's terminal keeps every process it starts to the fleet's processor
// threads (#2167), so the reading is also tested from a process kept to one
// thread, which still reads the machine's cores and not the few it may use.
func TestMachineProcessorsReadsThisMachineFromAProcessKeptToOneThread(t *testing.T) {
	// Arrange
	own, err := processor.ProcessThreads(windows.CurrentProcess())
	if err != nil {
		t.Fatal(err)
	}
	oneThread := own & -own
	reading := exec.Command(os.Args[0], "-test.run=^TestMachineProcessorsReadsThisMachine$", "-test.v")
	var output bytes.Buffer
	reading.Stdout, reading.Stderr = &output, &output
	reading.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_SUSPENDED}
	if err := reading.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if reading.ProcessState == nil {
			reading.Process.Kill()
			reading.Wait()
		}
	})
	process, err := windows.OpenProcess(windows.PROCESS_SET_INFORMATION, false, uint32(reading.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process)
	if result, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("SetProcessAffinityMask").Call(uintptr(process), oneThread); result == 0 {
		t.Fatalf("keep the reading process to the processor thread %#x: %v", oneThread, err)
	}

	// Act
	if err := proc.Resume(reading.Process.Pid); err != nil {
		t.Fatal(err)
	}
	err = reading.Wait()

	// Assert
	if err != nil || !strings.Contains(output.String(), "--- PASS: TestMachineProcessorsReadsThisMachine ") {
		t.Fatalf("the reading from a process kept to the processor thread %#x ended with %v, want it to pass:\n%s", oneThread, err, output.String())
	}
}
