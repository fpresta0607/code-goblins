package supervisor

import (
	"reflect"
	"runtime"
	"testing"
)

func TestMachineProcessorsReadsThisMachine(t *testing.T) {
	// Act
	first, err := MachineProcessors()
	second, secondErr := MachineProcessors()

	// Assert
	t.Logf("this machine reads %+v", first)
	if err != nil || secondErr != nil {
		t.Fatalf("MachineProcessors: %v, then %v", err, secondErr)
	}
	if first.PerformanceCores < 1 || first.EfficiencyCores < 0 || first.PerformanceCores+first.EfficiencyCores > runtime.NumCPU() {
		t.Fatalf("cores = %d performance and %d efficiency on a machine of %d threads, want this machine's cores", first.PerformanceCores, first.EfficiencyCores, runtime.NumCPU())
	}
	if first.Free < 0 || first.Free > 1 {
		t.Fatalf("free share of the performance cores = %v, want a share", first.Free)
	}
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("a reading straight after the first = %+v, want the first again, %+v, and not a judgment over a few milliseconds", second, first)
	}
}
